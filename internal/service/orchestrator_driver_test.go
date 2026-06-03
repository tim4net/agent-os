package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// WP-O5 (#42): Driver integration tests (real PG, fake gate stubs)
// ---------------------------------------------------------------------------

// stubGateConductor is a fake GateConductor for tests.
type stubGateConductor struct {
	results    map[int32]*GateResult
	err        error   // if set, all gates return this error
	invocations []int32 // gates actually invoked
}

func (s *stubGateConductor) ConductGate(_ context.Context, _ *db.WorkUnit, gateNum int32, _ string) (*GateResult, error) {
	s.invocations = append(s.invocations, gateNum)
	if s.err != nil {
		return nil, s.err
	}
	if r, ok := s.results[gateNum]; ok {
		return r, nil
	}
	// Default: pass
	return &GateResult{
		Gate:     gateNum,
		Model:    "test-model",
		Pass:     true,
		Severity: "info",
		Class:    "test",
		Summary:  "gate passed",
	}, nil
}

// getDriverTestDB returns a test DB pool for driver tests, cleaning up state.
func getDriverTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"TRUNCATE work_units CASCADE; DELETE FROM control_state; INSERT INTO control_state (mode, cadence_seconds) VALUES ('stopped', 60);",
		)
		pool.Close()
	})
	return pool
}

// seedDriverUnit enqueues a work unit for driver tests.
func seedDriverUnit(t *testing.T, queries *db.Queries, wpRef string) *db.WorkUnit {
	t.Helper()
	unit, err := queries.EnqueueWorkUnit(context.Background(), db.EnqueueWorkUnitParams{
		WpRef:   wpRef,
		Payload: json.RawMessage(fmt.Sprintf(`{"wp_ref":"%s","task":"test"}`, wpRef)),
	})
	if err != nil {
		t.Fatalf("seedDriverUnit %s: %v", wpRef, err)
	}
	return &unit
}

// ---------------------------------------------------------------------------
// Test: driver claims + dispatches one unit → unit goes in_flight then done
// ---------------------------------------------------------------------------

func TestDriver_ClaimsAndDispatchesOneUnit(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)
	ledger := NewLedgerService(queries)

	// Set tick mode so RunLoop dispatches exactly one unit and returns.
	_, err := queries.SetControlMode(context.Background(), db.ControlModeTick)
	if err != nil {
		t.Fatalf("set tick mode: %v", err)
	}

	unit := seedDriverUnit(t, queries, "wp-o5-dispatch-test")

	conductor := &stubGateConductor{
		results: map[int32]*GateResult{
			2: {Gate: 2, Model: "xai/grok-4", Pass: true, Severity: "info", Class: "review", Summary: "LGTM"},
			3: {Gate: 3, Model: "openrouter/anthropic/claude-sonnet-4", Pass: true, Severity: "info", Class: "review", Summary: "LGTM"},
		},
	}

	driver := NewDriver(queries, ledger, DriverConfig{
		ShadowMode: false,
		GateModelFamilies: map[int32]string{
			2: "xai/grok-4",
			3: "openrouter/anthropic/claude-sonnet-4",
		},
	}, slog.Default())

	err = driver.Run(context.Background(), conductor)
	if err != nil {
		t.Fatalf("driver.Run: %v", err)
	}

	// Verify unit went to done.
	var status string
	err = pool.QueryRow(context.Background(), "SELECT status FROM work_units WHERE id = $1", unit.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read unit status: %v", err)
	}
	if status != "done" {
		t.Fatalf("expected status 'done', got '%s'", status)
	}

	// Verify gates were invoked.
	if len(conductor.invocations) != 2 {
		t.Fatalf("expected 2 gate invocations, got %d", len(conductor.invocations))
	}
}

// ---------------------------------------------------------------------------
// Test: stop flag mid-run → driver stops dispatching within one iteration
// ---------------------------------------------------------------------------

func TestDriver_StopFlagStopsDispatch(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)

	// Set continuous mode.
	_, err := queries.SetControlMode(context.Background(), db.ControlModeContinuous)
	if err != nil {
		t.Fatalf("set continuous mode: %v", err)
	}

	// Enqueue 5 units.
	for i := 0; i < 5; i++ {
		seedDriverUnit(t, queries, fmt.Sprintf("wp-o5-stop-%d", i))
	}

	var dispatchedCount int64

	// We use RunLoop directly with a dispatch function that flips mode to
	// stopped after 1 dispatch, verifying the stop flag is honored.
	err = RunLoop(context.Background(), queries, func(ctx context.Context, u *db.WorkUnit) error {
		n := atomic.AddInt64(&dispatchedCount, 1)
		if n >= 1 {
			// Flip to stopped — the loop should notice and exit.
			_, _ = queries.SetControlMode(ctx, db.ControlModeStopped)
		}
		// Complete the unit.
		_, _ = queries.CompleteWorkUnit(ctx, u.ID)
		return nil
	}, slog.Default())

	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	// Should have dispatched exactly 1 (the callback set stopped after 1st).
	if atomic.LoadInt64(&dispatchedCount) != 1 {
		t.Fatalf("expected exactly 1 dispatch before stop, got %d", atomic.LoadInt64(&dispatchedCount))
	}
}

// ---------------------------------------------------------------------------
// Test: gate-independence guard → driver refuses same model family for G2/G3
// ---------------------------------------------------------------------------

func TestDriver_GateIndependenceViolation(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)
	ledger := NewLedgerService(queries)

	_, err := queries.SetControlMode(context.Background(), db.ControlModeTick)
	if err != nil {
		t.Fatalf("set tick mode: %v", err)
	}

	unit := seedDriverUnit(t, queries, "wp-o5-gate-indep")

	conductor := &stubGateConductor{}

	// Same model family for both gates — should fail.
	driver := NewDriver(queries, ledger, DriverConfig{
		ShadowMode: false,
		GateModelFamilies: map[int32]string{
			2: "xai/grok-4",
			3: "xai/grok-3", // same family "xai"
		},
	}, slog.Default())

	err = driver.Run(context.Background(), conductor)
	if err != nil {
		t.Fatalf("driver.Run: %v", err)
	}

	// Unit should be failed, not done.
	var status string
	err = pool.QueryRow(context.Background(), "SELECT status FROM work_units WHERE id = $1", unit.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read unit status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected status 'failed' after gate independence violation, got '%s'", status)
	}

	// No gates should have been invoked (validation happens before).
	if len(conductor.invocations) != 0 {
		t.Fatalf("expected 0 gate invocations (pre-check should block), got %d", len(conductor.invocations))
	}
}

// ---------------------------------------------------------------------------
// Test: shadow no-double-merge → in shadow, driver does not merge
// ---------------------------------------------------------------------------

func TestDriver_ShadowModeNoMerge(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)
	ledger := NewLedgerService(queries)

	_, err := queries.SetControlMode(context.Background(), db.ControlModeTick)
	if err != nil {
		t.Fatalf("set tick mode: %v", err)
	}

	unit := seedDriverUnit(t, queries, "wp-o5-shadow")

	conductor := &stubGateConductor{
		results: map[int32]*GateResult{
			2: {Gate: 2, Model: "xai/grok-4", Pass: true, Severity: "info", Class: "review", Summary: "LGTM"},
			3: {Gate: 3, Model: "openrouter/anthropic/claude-sonnet-4", Pass: true, Severity: "info", Class: "review", Summary: "LGTM"},
		},
	}

	// Shadow mode ON.
	driver := NewDriver(queries, ledger, DriverConfig{
		ShadowMode: true,
		GateModelFamilies: map[int32]string{
			2: "xai/grok-4",
			3: "openrouter/anthropic/claude-sonnet-4",
		},
	}, slog.Default())

	err = driver.Run(context.Background(), conductor)
	if err != nil {
		t.Fatalf("driver.Run: %v", err)
	}

	// Unit should be done (completed in shadow without merge).
	var status string
	err = pool.QueryRow(context.Background(), "SELECT status FROM work_units WHERE id = $1", unit.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read unit status: %v", err)
	}
	if status != "done" {
		t.Fatalf("expected status 'done' in shadow mode, got '%s'", status)
	}

	// Verify run_log records shadow dispatch.
	logs, err := ledger.ListRunLog(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list run log: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.WpRef == "wp-o5-shadow" && l.Summary.String == "dispatched (shadow)" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected run_log entry with shadow dispatch summary")
	}
}

// ---------------------------------------------------------------------------
// Test: rollback → flipping mode=tick returns to one-per-interval behavior
// ---------------------------------------------------------------------------

func TestDriver_RollbackToTickMode(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)

	// Start in continuous mode.
	_, err := queries.SetControlMode(context.Background(), db.ControlModeContinuous)
	if err != nil {
		t.Fatalf("set continuous mode: %v", err)
	}

	// Enqueue 3 units.
	for i := 0; i < 3; i++ {
		seedDriverUnit(t, queries, fmt.Sprintf("wp-o5-rollback-%d", i))
	}

	var dispatchedCount int64

	// Run in a goroutine — will dispatch until we flip to tick.
	done := make(chan error, 1)
	go func() {
		done <- RunLoop(context.Background(), queries, func(ctx context.Context, u *db.WorkUnit) error {
			n := atomic.AddInt64(&dispatchedCount, 1)
			// After 2 dispatches, flip to tick mode.
			if n == 2 {
				queries.SetControlMode(ctx, db.ControlModeTick)
			}
			_, _ = queries.CompleteWorkUnit(ctx, u.ID)
			return nil
		}, slog.Default())
	}()

	// Wait for RunLoop to finish (it should exit when mode != continuous).
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLoop didn't exit within 10s")
	}

	// Should have dispatched exactly 2 in continuous mode.
	if atomic.LoadInt64(&dispatchedCount) != 2 {
		t.Fatalf("expected 2 dispatches before rollback to tick, got %d", atomic.LoadInt64(&dispatchedCount))
	}

	// Now verify tick mode dispatches exactly one per invocation.
	state, err := queries.GetControlState(context.Background())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if db.ControlMode(state.Mode) != db.ControlModeTick {
		t.Fatalf("expected mode tick after rollback, got %s", state.Mode)
	}

	// RunLoop in tick mode should dispatch exactly 1 more.
	var tickDispatches int64
	err = RunLoop(context.Background(), queries, func(ctx context.Context, u *db.WorkUnit) error {
		atomic.AddInt64(&tickDispatches, 1)
		_, _ = queries.CompleteWorkUnit(ctx, u.ID)
		return nil
	}, slog.Default())
	if err != nil {
		t.Fatalf("RunLoop tick: %v", err)
	}

	if tickDispatches != 1 {
		t.Fatalf("expected exactly 1 dispatch in tick mode, got %d", tickDispatches)
	}
}

// ---------------------------------------------------------------------------
// Test: halt sentinel file stops dispatch
// ---------------------------------------------------------------------------

func TestDriver_HaltSentinelStopsDispatch(t *testing.T) {
	pool := getDriverTestDB(t)
	queries := db.New(pool)
	ledger := NewLedgerService(queries)

	_, err := queries.SetControlMode(context.Background(), db.ControlModeTick)
	if err != nil {
		t.Fatalf("set tick mode: %v", err)
	}

	// Create a halt sentinel file.
	sentinelPath := fmt.Sprintf("/tmp/aos-test-halt-%d", time.Now().UnixNano())
	t.Cleanup(func() { os.Remove(sentinelPath) })

	err = os.WriteFile(sentinelPath, []byte("autonomy:halt\n"), 0644)
	if err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	unit := seedDriverUnit(t, queries, "wp-o5-halt-sentinel")

	conductor := &stubGateConductor{}

	driver := NewDriver(queries, ledger, DriverConfig{
		ShadowMode:        false,
		HaltSentinelPath:  sentinelPath,
		GateModelFamilies: map[int32]string{2: "xai/grok-4"},
	}, slog.Default())

	err = driver.Run(context.Background(), conductor)
	if err != nil {
		t.Fatalf("driver.Run: %v", err)
	}

	// Unit should still be in_flight (claimed but NOT dispatched since halt was detected).
	var status string
	err = pool.QueryRow(context.Background(), "SELECT status FROM work_units WHERE id = $1", unit.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read unit status: %v", err)
	}
	// The unit was already claimed by RunLoop's ClaimNextWorkUnit, so it's in_flight.
	// The dispatchFn returned nil (halt sentinel), so the unit is NOT completed.
	if status != "in_flight" {
		t.Fatalf("expected status 'in_flight' after halt sentinel, got '%s'", status)
	}

	// No gates should have been invoked.
	if len(conductor.invocations) != 0 {
		t.Fatalf("expected 0 gate invocations when halted, got %d", len(conductor.invocations))
	}
}
