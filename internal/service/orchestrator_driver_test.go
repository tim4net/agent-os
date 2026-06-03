package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

func getOrchestratorDriverTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("AOS_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("AOS_TEST_DSN or AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	reset := func() {
		_, _ = pool.Exec(context.Background(),
			"TRUNCATE work_units, run_log, findings CASCADE; DELETE FROM control_state; INSERT INTO control_state (mode, cadence_seconds) VALUES ('stopped', 60);",
		)
	}
	reset()
	t.Cleanup(func() {
		reset()
		pool.Close()
	})
	return pool
}

type fakeGatePipeline struct {
	mu            sync.Mutex
	calls         int
	mergeAttempts int
	onRun         func(ctx context.Context, unit *db.WorkUnit) (GateResult, error)
}

func (f *fakeGatePipeline) Run(ctx context.Context, unit *db.WorkUnit) (GateResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.onRun != nil {
		return f.onRun(ctx, unit)
	}
	return GateResult{
		Summary:     "fake gate success",
		Gate2Model:  "claude-3-5-sonnet",
		Gate3Model:  "gpt-5.5",
		Gate2Passed: true,
		Gate3Passed: true,
	}, nil
}

func (f *fakeGatePipeline) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeGatePipeline) RecordMergeAttempt() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mergeAttempts++
}

func (f *fakeGatePipeline) MergeAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mergeAttempts
}

func newTestDriver(queries *db.Queries, orch *Orchestrator, ledger *LedgerService, gate GatePipeline) *OrchestratorDriver {
	return NewOrchestratorDriver(
		queries,
		orch,
		ledger,
		gate,
		WithDriverLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithDriverHaltCheckInterval(0),
	)
}

func TestOrchestratorDriverClaimsDispatchesOneUnit(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	unit, err := orch.Enqueue(ctx, "wp-driver-one", json.RawMessage(`{"pr_ref":"PR-101"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeTick); err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	var observedStatus string
	fake := &fakeGatePipeline{onRun: func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if got.ID != unit.ID {
			t.Fatalf("pipeline received unit ID %d, want %d", got.ID, unit.ID)
		}
		if err := pool.QueryRow(ctx, "SELECT status::text FROM work_units WHERE id=$1", got.ID).Scan(&observedStatus); err != nil {
			t.Fatalf("read status during dispatch: %v", err)
		}
		return GateResult{
			Summary:     "driver dispatched one unit",
			Gate2Model:  "claude-3-5-sonnet",
			Gate3Model:  "gpt-5.5",
			Gate2Passed: true,
			Gate3Passed: true,
		}, nil
	}}
	driver := newTestDriver(queries, orch, ledger, fake)

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("driver.Run: %v", err)
	}
	if observedStatus != string(db.WorkUnitStatusInFlight) {
		t.Fatalf("expected unit in_flight during dispatch, got %q", observedStatus)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected pipeline invoked once, got %d", fake.Calls())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusDone)
	if count := countRows(t, pool, "run_log", "wp_ref", unit.WpRef); count != 1 {
		t.Fatalf("expected 1 run_log row for %s, got %d", unit.WpRef, count)
	}
}

func TestOrchestratorDriverStopFlagMidRunStopsWithinOneIteration(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "UPDATE control_state SET cadence_seconds = 0"); err != nil {
		t.Fatalf("set cadence: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeContinuous); err != nil {
		t.Fatalf("SetMode continuous: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := orch.Enqueue(ctx, fmt.Sprintf("wp-stop-%d", i), json.RawMessage(`{}`)); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	fake := &fakeGatePipeline{onRun: func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if _, err := orch.SetMode(ctx, ModeStopped); err != nil {
			t.Fatalf("SetMode stopped from fake: %v", err)
		}
		return GateResult{
			Summary:     "stop requested",
			Gate2Model:  "claude-3-5-sonnet",
			Gate3Model:  "gpt-5.5",
			Gate2Passed: true,
			Gate3Passed: true,
		}, nil
	}}
	driver := newTestDriver(queries, orch, ledger, fake)

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("driver.Run: %v", err)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected exactly one dispatch before stop, got %d", fake.Calls())
	}
	assertStatusCount(t, pool, db.WorkUnitStatusDone, 1)
	assertStatusCount(t, pool, db.WorkUnitStatusQueued, 2)
}

func TestOrchestratorDriverGateIndependenceGuard(t *testing.T) {
	if err := GateModelsIndependent("claude-3-5-sonnet", "gpt-5.5"); err != nil {
		t.Fatalf("different model families should be independent: %v", err)
	}
	if err := GateModelsIndependent("anthropic/claude-3-5-sonnet", "claude-3-5-haiku"); !errors.Is(err, ErrGateModelFamilyNotIndependent) {
		t.Fatalf("same model family should fail guard, got %v", err)
	}

	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	unit, err := orch.Enqueue(ctx, "wp-same-family", json.RawMessage(`{"pr_ref":"PR-102","gate2_model":"anthropic/claude-3-5-sonnet","gate3_model":"claude-3-5-haiku"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeTick); err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	fake := &fakeGatePipeline{}
	driver := newTestDriver(queries, orch, ledger, fake)
	err = driver.Run(ctx)
	if !errors.Is(err, ErrGateModelFamilyNotIndependent) {
		t.Fatalf("driver.Run error = %v, want ErrGateModelFamilyNotIndependent", err)
	}
	if fake.Calls() != 0 {
		t.Fatalf("pipeline should not be invoked when planned Gate 3 is same-family, got %d calls", fake.Calls())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusFailed)
	if count := countRows(t, pool, "findings", "class", "gate_independence_degraded"); count != 1 {
		t.Fatalf("expected 1 gate_independence_degraded finding, got %d", count)
	}
	payload := latestRunLogPayload(t, pool, unit.WpRef)
	var result GateResult
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("decode failed run_log payload: %v", err)
	}
	if !result.Degraded {
		t.Fatalf("expected degraded=true in failed run_log payload: %s", payload)
	}
}

func TestOrchestratorDriverShadowNoDoubleMerge(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	unit, err := orch.Enqueue(ctx, "wp-shadow", json.RawMessage(`{"pr_ref":"PR-103"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeTick); err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	fake := &fakeGatePipeline{}
	fake.onRun = func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if mergeAllowed(got.Payload) {
			fake.RecordMergeAttempt()
			return GateResult{Merged: true, MergeAttempted: true, Gate2Model: "claude-3-5-sonnet", Gate3Model: "gpt-5.5"}, nil
		}
		return GateResult{
			Summary:     "shadow dispatch without merge",
			Gate2Model:  "claude-3-5-sonnet",
			Gate3Model:  "gpt-5.5",
			Gate2Passed: true,
			Gate3Passed: true,
		}, nil
	}
	driver := newTestDriver(queries, orch, ledger, fake)

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("driver.Run: %v", err)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected one shadow dispatch, got %d", fake.Calls())
	}
	if fake.MergeAttempts() != 0 {
		t.Fatalf("shadow mode must not attempt merge, got %d attempts", fake.MergeAttempts())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusDone)
}

func TestOrchestratorDriverShadowFailsClosedOnAdapterReportedMerge(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	unit, err := orch.Enqueue(ctx, "wp-shadow-guard", json.RawMessage(`{"pr_ref":"PR-104"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeTick); err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	fake := &fakeGatePipeline{onRun: func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if mergeAllowed(got.Payload) {
			t.Fatalf("shadow dispatch payload should set merge_allowed=false")
		}
		return GateResult{
			Summary:        "malicious adapter reported merge",
			Gate2Model:     "claude-3-5-sonnet",
			Gate3Model:     "gpt-5.5",
			Gate2Passed:    true,
			Gate3Passed:    true,
			Merged:         true,
			MergeAttempted: true,
		}, nil
	}}
	driver := newTestDriver(queries, orch, ledger, fake)

	err = driver.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "shadow-mode merge guard") {
		t.Fatalf("driver.Run error = %v, want shadow-mode merge guard", err)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected one shadow dispatch, got %d", fake.Calls())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusFailed)
	if count := countRows(t, pool, "findings", "class", "shadow_merge_guard"); count != 1 {
		t.Fatalf("expected 1 shadow_merge_guard finding, got %d", count)
	}
}

func TestOrchestratorDriverMidDispatchHaltDoesNotStrandUnit(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "UPDATE control_state SET cadence_seconds = 0"); err != nil {
		t.Fatalf("set cadence: %v", err)
	}
	unit, err := orch.Enqueue(ctx, "wp-mid-dispatch-halt", json.RawMessage(`{"pr_ref":"PR-105"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeContinuous); err != nil {
		t.Fatalf("SetMode continuous: %v", err)
	}

	var dispatchStarted atomic.Bool
	fake := &fakeGatePipeline{onRun: func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if got.ID != unit.ID {
			t.Fatalf("pipeline received unit ID %d, want %d", got.ID, unit.ID)
		}
		dispatchStarted.Store(true)
		select {
		case <-ctx.Done():
			return GateResult{
				Summary:     "halt canceled dispatch",
				Gate2Model:  "claude-3-5-sonnet",
				Gate3Model:  "gpt-5.5",
				Gate2Passed: false,
				Gate3Passed: false,
			}, ctx.Err()
		case <-time.After(2 * time.Second):
			return GateResult{
				Summary:    "gate did not observe halt cancellation",
				Gate2Model: "claude-3-5-sonnet",
				Gate3Model: "gpt-5.5",
			}, errors.New("gate did not observe halt cancellation")
		}
	}}
	driver := newTestDriver(queries, orch, ledger, fake)
	driver.Halt = func(ctx context.Context) (bool, error) {
		return dispatchStarted.Load(), nil
	}
	driver.HaltCheckInterval = 5 * time.Millisecond

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("driver.Run: %v", err)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected one dispatch before halt, got %d", fake.Calls())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusFailed)
	assertStatusCount(t, pool, db.WorkUnitStatusInFlight, 0)
}

func TestOrchestratorDriverRollbackTickOnePerInterval(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "UPDATE control_state SET cadence_seconds = 0"); err != nil {
		t.Fatalf("set cadence: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeContinuous); err != nil {
		t.Fatalf("SetMode continuous: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := orch.Enqueue(ctx, fmt.Sprintf("wp-rollback-%d", i), json.RawMessage(`{}`)); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	fake := &fakeGatePipeline{onRun: func(ctx context.Context, got *db.WorkUnit) (GateResult, error) {
		if _, err := orch.SetMode(ctx, ModeTick); err != nil {
			t.Fatalf("SetMode tick from fake: %v", err)
		}
		return GateResult{
			Summary:     "rollback to tick",
			Gate2Model:  "claude-3-5-sonnet",
			Gate3Model:  "gpt-5.5",
			Gate2Passed: true,
			Gate3Passed: true,
		}, nil
	}}
	driver := newTestDriver(queries, orch, ledger, fake)

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("continuous driver.Run: %v", err)
	}
	if fake.Calls() != 1 {
		t.Fatalf("expected one dispatch before rollback mode switch, got %d", fake.Calls())
	}
	assertStatusCount(t, pool, db.WorkUnitStatusDone, 1)
	assertStatusCount(t, pool, db.WorkUnitStatusQueued, 2)

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("tick driver.Run: %v", err)
	}
	if fake.Calls() != 2 {
		t.Fatalf("tick mode should dispatch exactly one more unit, got %d total calls", fake.Calls())
	}
	assertStatusCount(t, pool, db.WorkUnitStatusDone, 2)
	assertStatusCount(t, pool, db.WorkUnitStatusQueued, 1)
}

func mergeAllowed(payload []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return true
	}
	v, ok := m["merge_allowed"].(bool)
	if !ok {
		return true
	}
	return v
}

func assertWorkUnitStatus(t *testing.T, pool *pgxpool.Pool, id int64, want db.WorkUnitStatus) {
	t.Helper()
	var got db.WorkUnitStatus
	if err := pool.QueryRow(context.Background(), "SELECT status FROM work_units WHERE id=$1", id).Scan(&got); err != nil {
		t.Fatalf("read work unit %d status: %v", id, err)
	}
	if got != want {
		t.Fatalf("unit %d status = %s, want %s", id, got, want)
	}
}

func assertStatusCount(t *testing.T, pool *pgxpool.Pool, status db.WorkUnitStatus, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM work_units WHERE status=$1", status).Scan(&got); err != nil {
		t.Fatalf("count status %s: %v", status, err)
	}
	if got != want {
		t.Fatalf("status %s count = %d, want %d", status, got, want)
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, table, column, value string) int64 {
	t.Helper()
	var got int64
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s=$1", table, column)
	if err := pool.QueryRow(context.Background(), query, value).Scan(&got); err != nil {
		t.Fatalf("%s count where %s=%q: %v", table, column, value, err)
	}
	return got
}

func latestRunLogPayload(t *testing.T, pool *pgxpool.Pool, wpRef string) []byte {
	t.Helper()
	var payload []byte
	if err := pool.QueryRow(context.Background(), "SELECT payload FROM run_log WHERE wp_ref=$1 ORDER BY id DESC LIMIT 1", wpRef).Scan(&payload); err != nil {
		t.Fatalf("read latest run_log payload for %s: %v", wpRef, err)
	}
	return payload
}

func TestOrchestratorDriverExternalHaltPredicate(t *testing.T) {
	pool := getOrchestratorDriverTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)
	ledger := NewLedgerService(queries)
	ctx := context.Background()

	unit, err := orch.Enqueue(ctx, "wp-halt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := orch.SetMode(ctx, ModeTick); err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	fake := &fakeGatePipeline{}
	driver := newTestDriver(queries, orch, ledger, fake)
	driver.Halt = func(ctx context.Context) (bool, error) { return true, nil }
	driver.HaltCheckInterval = time.Hour

	if err := driver.Run(ctx); err != nil {
		t.Fatalf("driver.Run with halt: %v", err)
	}
	if fake.Calls() != 0 {
		t.Fatalf("halt predicate should stop before dispatch, got %d pipeline calls", fake.Calls())
	}
	assertWorkUnitStatus(t, pool, unit.ID, db.WorkUnitStatusQueued)
	state, err := orch.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Mode != db.ControlModeStopped {
		t.Fatalf("halt predicate should force stopped mode, got %s", state.Mode)
	}
}
