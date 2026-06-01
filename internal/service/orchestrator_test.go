package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

func getOrchestratorTestDB(t *testing.T) *pgxpool.Pool {
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

// TestOrchestratorEnqueueClaimComplete verifies the full happy-path lifecycle:
// enqueue → queued, claim → in_flight (claimed_at set), complete → done (completed_at set).
func TestOrchestratorEnqueueClaimComplete(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Enqueue
	unit, err := orch.Enqueue(ctx, "wp-42", json.RawMessage(`{"task":"build"}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if unit.ID == 0 {
		t.Fatal("expected non-zero ID after enqueue")
	}
	if unit.Status != db.WorkUnitStatusQueued {
		t.Fatalf("expected status queued, got %s", unit.Status)
	}

	// Claim
	claimed, err := orch.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected a claimed unit, got nil")
	}
	if claimed.ID != unit.ID {
		t.Fatalf("claimed ID mismatch: got %d, want %d", claimed.ID, unit.ID)
	}
	if claimed.Status != db.WorkUnitStatusInFlight {
		t.Fatalf("expected status in_flight after claim, got %s", claimed.Status)
	}
	if !claimed.ClaimedAt.Valid {
		t.Fatal("expected claimed_at to be set after claim")
	}

	// Complete
	completed, err := orch.Complete(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Status != db.WorkUnitStatusDone {
		t.Fatalf("expected status done after complete, got %s", completed.Status)
	}
	if !completed.CompletedAt.Valid {
		t.Fatal("expected completed_at to be set after complete")
	}
}

// TestOrchestratorConcurrentClaim proves SKIP LOCKED prevents double-claiming:
// enqueue N units, launch M goroutines all claiming concurrently, assert every
// claimed unit ID is unique and all N units were claimed.
func TestOrchestratorConcurrentClaim(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	const N = 20 // units
	const M = 20 // goroutines

	// Enqueue N units
	var ids []int64
	for i := 0; i < N; i++ {
		unit, err := orch.Enqueue(ctx, fmt.Sprintf("wp-concurrent-%d", i), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
		ids = append(ids, unit.ID)
	}

	// M goroutines each claim one unit, returning the claimed ID (or -1).
	type result struct {
		id  int64
		err error
	}
	ch := make(chan result, M)
	var wg sync.WaitGroup

	for i := 0; i < M; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unit, err := orch.ClaimNext(ctx)
			if err != nil {
				ch <- result{-1, err}
				return
			}
			if unit == nil {
				ch <- result{-1, nil} // no unit available
				return
			}
			ch <- result{unit.ID, nil}
		}()
	}
	wg.Wait()
	close(ch)

	// Collect all claimed IDs into a slice, then count duplicates.
	var claimedIDs []int64
	for r := range ch {
		if r.err != nil {
			t.Fatalf("claim error: %v", r.err)
		}
		if r.id != -1 {
			claimedIDs = append(claimedIDs, r.id)
		}
	}

	// Build occurrence map — every ID must appear exactly once.
	counts := make(map[int64]int)
	for _, id := range claimedIDs {
		counts[id]++
	}

	// Assert no double-claims.
	for id, cnt := range counts {
		if cnt > 1 {
			t.Fatalf("double-claim of unit %d (appeared %d times)", id, cnt)
		}
	}

	// Assert all N units were claimed.
	if len(counts) != N {
		t.Fatalf("claimed %d unique units but expected all %d enqueued units", len(counts), N)
	}

	// Each claimed ID must be from the original set.
	originalSet := make(map[int64]bool)
	for _, id := range ids {
		originalSet[id] = true
	}
	for id := range counts {
		if !originalSet[id] {
			t.Fatalf("claimed ID %d was not in the original set", id)
		}
	}
}

// TestOrchestratorStoppedMode proves that RunLoop in stopped mode does NOT claim
// any work units.
func TestOrchestratorStoppedMode(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Ensure stopped
	_, err := orch.SetMode(ctx, ModeStopped)
	if err != nil {
		t.Fatalf("SetMode stopped: %v", err)
	}

	// Enqueue a unit
	unit, err := orch.Enqueue(ctx, "wp-stopped", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Track if dispatchFn was called
	var dispatched int64

	err = RunLoop(ctx, queries, func(ctx context.Context, u *db.WorkUnit) error {
		atomic.AddInt64(&dispatched, 1)
		return nil
	}, slog.Default())
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	if atomic.LoadInt64(&dispatched) != 0 {
		t.Fatal("expected no dispatches in stopped mode")
	}

	// Verify the unit is still queued
	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM work_units WHERE id = $1", unit.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read unit status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("expected status 'queued' after stopped RunLoop, got '%s'", status)
	}
}

// TestOrchestratorTickModeDispatchesOne proves that tick mode claims and dispatches
// exactly one unit per RunLoop invocation, leaving remaining units queued.
func TestOrchestratorTickModeDispatchesOne(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Set tick mode
	_, err := orch.SetMode(ctx, ModeTick)
	if err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}

	// Enqueue 2 units
	unit1, err := orch.Enqueue(ctx, "wp-tick-1", json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("Enqueue 1: %v", err)
	}
	_, err = orch.Enqueue(ctx, "wp-tick-2", json.RawMessage(`{"n":2}`))
	if err != nil {
		t.Fatalf("Enqueue 2: %v", err)
	}

	// RunLoop once in tick mode — should dispatch exactly one unit.
	var dispatchedCount int64
	err = RunLoop(ctx, queries, func(ctx context.Context, u *db.WorkUnit) error {
		atomic.AddInt64(&dispatchedCount, 1)
		return nil
	}, slog.Default())
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	if dispatchedCount != 1 {
		t.Fatalf("tick should dispatch exactly 1 unit, dispatched %d", dispatchedCount)
	}

	// Verify unit1 (oldest) was dispatched (status=in_flight), unit2 still queued.
	var status1, status2 string
	err = pool.QueryRow(ctx, "SELECT status FROM work_units WHERE id = $1", unit1.ID).Scan(&status1)
	if err != nil {
		t.Fatalf("read unit1 status: %v", err)
	}
	if status1 != "in_flight" {
		t.Fatalf("expected first unit status 'in_flight' after tick, got '%s'", status1)
	}
	err = pool.QueryRow(ctx, "SELECT status FROM work_units WHERE id > $1 ORDER BY id LIMIT 1", unit1.ID).Scan(&status2)
	if err != nil {
		t.Fatalf("read unit2 status: %v", err)
	}
	if status2 != "queued" {
		t.Fatalf("expected second unit still 'queued' after tick, got '%s'", status2)
	}
}

// TestOrchestratorContinuousStopsOnModeChange proves that continuous mode
// re-checks the stop flag each iteration: the dispatchFn flips mode to stopped
// after 2 dispatches and the loop terminates without dispatching more.
func TestOrchestratorContinuousStopsOnModeChange(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Set continuous mode
	_, err := orch.SetMode(ctx, ModeContinuous)
	if err != nil {
		t.Fatalf("SetMode continuous: %v", err)
	}

	// Enqueue 5 units
	for i := 0; i < 5; i++ {
		_, err := orch.Enqueue(ctx, fmt.Sprintf("wp-continuous-%d", i), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	// dispatchFn flips to stopped after the 2nd dispatch.
	var dispatchedCount int64
	err = RunLoop(ctx, queries, func(ctx context.Context, u *db.WorkUnit) error {
		n := atomic.AddInt64(&dispatchedCount, 1)
		if n >= 2 {
			// Switch to stopped — the loop must notice and exit.
			_, _ = orch.SetMode(ctx, ModeStopped)
		}
		return nil
	}, slog.Default())
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	// The loop should have dispatched exactly 2 (the 2nd callback set stopped,
	// then the loop re-reads mode and exits before claiming the 3rd).
	if dispatchedCount != 2 {
		t.Fatalf("continuous loop should stop after mode change at dispatch 2, got %d", dispatchedCount)
	}
}

// TestOrchestratorModeTransitionPersists proves mode changes survive a reload
// (GetState returns the persisted value).
func TestOrchestratorModeTransitionPersists(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Set continuous
	_, err := orch.SetMode(ctx, ModeContinuous)
	if err != nil {
		t.Fatalf("SetMode continuous: %v", err)
	}
	state, err := orch.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if string(state.Mode) != string(ModeContinuous) {
		t.Fatalf("expected mode continuous, got %s", state.Mode)
	}

	// Set stopped
	_, err = orch.SetMode(ctx, ModeStopped)
	if err != nil {
		t.Fatalf("SetMode stopped: %v", err)
	}
	state, err = orch.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if string(state.Mode) != string(ModeStopped) {
		t.Fatalf("expected mode stopped, got %s", state.Mode)
	}

	// Set tick
	_, err = orch.SetMode(ctx, ModeTick)
	if err != nil {
		t.Fatalf("SetMode tick: %v", err)
	}
	state, err = orch.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if string(state.Mode) != string(ModeTick) {
		t.Fatalf("expected mode tick, got %s", state.Mode)
	}
}

// TestOrchestratorFailRecordsError proves that Fail records the real error message
// in the error column — NOT a swallowed sentinel.
func TestOrchestratorFailRecordsError(t *testing.T) {
	pool := getOrchestratorTestDB(t)
	queries := db.New(pool)
	orch := NewOrchestrator(queries)

	ctx := context.Background()

	// Enqueue and claim
	unit, err := orch.Enqueue(ctx, "wp-fail", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := orch.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claimed == nil || claimed.ID != unit.ID {
		t.Fatal("claim did not return the expected unit")
	}

	// Fail with a real error message
	realErrMsg := "connection refused: dial tcp 127.0.0.1:5432: connect: connection refused"
	failed, err := orch.Fail(ctx, claimed.ID, realErrMsg)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if failed.Status != db.WorkUnitStatusFailed {
		t.Fatalf("expected status failed, got %s", failed.Status)
	}
	if !failed.CompletedAt.Valid {
		t.Fatal("expected completed_at to be set after Fail")
	}
	if !failed.Error.Valid {
		t.Fatal("expected error column to be set")
	}
	if failed.Error.String != realErrMsg {
		t.Fatalf("error column mismatch:\n  got:  %q\n  want: %q", failed.Error.String, realErrMsg)
	}
}
