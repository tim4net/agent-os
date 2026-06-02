package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// WP-O2: Control-plane HTTP API handler tests (real-PG httptest route tests)
// ---------------------------------------------------------------------------

// newTestAPIForControl creates a test API for control-plane tests.
func newTestAPIForControl(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := getTestDB(t)
	queries := db.New(pool)
	a := &API{
		queries: queries,
		pool:    pool,
	}
	return a, pool
}

// seedWorkUnit inserts a work_unit directly into the DB for tests.
func seedWorkUnit(t *testing.T, pool *pgxpool.Pool, wpRef string, status string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO work_units (wp_ref, status, payload) VALUES ($1, $2::work_unit_status, '{}') RETURNING id`,
		wpRef, status,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedWorkUnit: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM work_units WHERE id = $1", id)
	})
	return id
}

// ---------------------------------------------------------------------------
// GET /api/control/state
// ---------------------------------------------------------------------------

func TestControl_GetState_WithSeededUnits_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	// Seed some work units in different states.
	seedWorkUnit(t, pool, "WP-O2-test-queued", "queued")
	seedWorkUnit(t, pool, "WP-O2-test-done", "done")
	seedWorkUnit(t, pool, "WP-O2-test-failed", "failed")

	req := httptest.NewRequest("GET", "/state", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ControlStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Mode should be one of the valid modes.
	if !validModes[resp.Mode] {
		t.Fatalf("expected valid mode, got %q", resp.Mode)
	}
	if resp.CadenceSeconds <= 0 {
		t.Fatalf("expected positive cadence_seconds, got %d", resp.CadenceSeconds)
	}
	if resp.QueueCounts == nil {
		t.Fatal("expected queue_counts to be non-nil")
	}

	// Should have at least the statuses we seeded.
	if resp.QueueCounts["queued"] < 1 {
		t.Fatalf("expected at least 1 queued, got %d", resp.QueueCounts["queued"])
	}
	if resp.QueueCounts["done"] < 1 {
		t.Fatalf("expected at least 1 done, got %d", resp.QueueCounts["done"])
	}
	if resp.QueueCounts["failed"] < 1 {
		t.Fatalf("expected at least 1 failed, got %d", resp.QueueCounts["failed"])
	}
}

// ---------------------------------------------------------------------------
// POST /api/control/mode — valid
// ---------------------------------------------------------------------------

func TestControl_SetMode_Valid_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	body := SetModeRequest{Mode: "continuous"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mode", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ControlStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Mode != "continuous" {
		t.Fatalf("expected mode continuous, got %q", resp.Mode)
	}

	// Verify it's persisted by reading state again.
	state, err := a.queries.GetControlState(context.Background())
	if err != nil {
		t.Fatalf("failed to read state from DB: %v", err)
	}
	if string(state.Mode) != "continuous" {
		t.Fatalf("expected DB mode continuous, got %q", state.Mode)
	}

	// Reset to stopped for other tests.
	a.queries.SetControlMode(context.Background(), db.ControlModeStopped)
}

// ---------------------------------------------------------------------------
// POST /api/control/mode — invalid enum
// ---------------------------------------------------------------------------

func TestControl_SetMode_InvalidEnum_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	body := SetModeRequest{Mode: "invalid_mode"}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mode", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// F4: verify DB state is unchanged after invalid enum attempt.
	state, err := a.queries.GetControlState(context.Background())
	if err != nil {
		t.Fatalf("failed to read state from DB: %v", err)
	}
	if string(state.Mode) == "invalid_mode" {
		t.Fatalf("DB mode should be unchanged after invalid enum, got %q", state.Mode)
	}
}

// ---------------------------------------------------------------------------
// F2 regression: POST mode-only must NOT clobber existing cadence
// ---------------------------------------------------------------------------

func TestControl_SetMode_ModeOnly_DoesNotClobberCadence(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	// Set cadence to 30 first.
	cadence30 := int32(30)
	body := SetModeRequest{Mode: "tick", CadenceSeconds: &cadence30}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mode", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Now POST mode-only (no cadence) — must NOT reset cadence.
	body2 := SetModeRequest{Mode: "continuous"}
	bodyBytes2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest("POST", "/mode", bytes.NewReader(bodyBytes2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp ControlStateResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if resp.CadenceSeconds != 30 {
		t.Fatalf("F2 regression: cadence should still be 30 after mode-only POST, got %d", resp.CadenceSeconds)
	}

	// Reset
	a.queries.SetControlMode(context.Background(), db.ControlModeStopped)
}

// ---------------------------------------------------------------------------
// F3: requeue a done unit must return 404 (not allowed)
// ---------------------------------------------------------------------------

func TestControl_Requeue_DoneUnit_Returns404(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	id := seedWorkUnit(t, pool, "WP-O2-requeue-done-test", "done")

	req := httptest.NewRequest("POST", "/units/"+jsonNumber(id)+"/requeue", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for done unit requeue, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /api/control/mode — with cadence_seconds
// ---------------------------------------------------------------------------

func TestControl_SetMode_WithCadence_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	cadence := int32(30)
	body := SetModeRequest{Mode: "tick", CadenceSeconds: &cadence}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mode", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ControlStateResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Mode != "tick" {
		t.Fatalf("expected mode tick, got %q", resp.Mode)
	}
	if resp.CadenceSeconds != 30 {
		t.Fatalf("expected cadence_seconds 30, got %d", resp.CadenceSeconds)
	}

	// Reset
	a.queries.SetControlMode(context.Background(), db.ControlModeStopped)
}

// ---------------------------------------------------------------------------
// POST /api/control/units/{unknown}/requeue → 404
// ---------------------------------------------------------------------------

func TestControl_Requeue_UnknownID_Returns404(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	req := httptest.NewRequest("POST", "/units/99999999/requeue", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /api/control/units/{id}/requeue — valid (failed unit)
// ---------------------------------------------------------------------------

func TestControl_Requeue_FailedUnit_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	id := seedWorkUnit(t, pool, "WP-O2-requeue-test", "failed")

	req := httptest.NewRequest("POST", "/units/"+jsonNumber(id)+"/requeue", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp WorkUnitResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "queued" {
		t.Fatalf("expected status queued after requeue, got %q", resp.Status)
	}
	if resp.ID != id {
		t.Fatalf("expected id %d, got %d", id, resp.ID)
	}
}

// ---------------------------------------------------------------------------
// GET /api/control/units?status= — filter by status
// ---------------------------------------------------------------------------

func TestControl_ListUnits_FilterByStatus(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	seedWorkUnit(t, pool, "WP-O2-list-queued", "queued")
	seedWorkUnit(t, pool, "WP-O2-list-done", "done")

	req := httptest.NewRequest("GET", "/units?status=queued", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp UnitListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, u := range resp.Units {
		if u.Status != "queued" {
			t.Fatalf("expected all units to have status queued, found %q (id=%d)", u.Status, u.ID)
		}
	}
	if len(resp.Units) < 1 {
		t.Fatal("expected at least 1 queued unit")
	}
}

// ---------------------------------------------------------------------------
// GET /api/control/units — no filter, return all
// ---------------------------------------------------------------------------

func TestControl_ListUnits_NoFilter_ReturnsAll(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	seedWorkUnit(t, pool, "WP-O2-all-1", "queued")
	seedWorkUnit(t, pool, "WP-O2-all-2", "done")

	req := httptest.NewRequest("GET", "/units", nil)
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp UnitListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Units) < 2 {
		t.Fatalf("expected at least 2 units, got %d", len(resp.Units))
	}
}

// ---------------------------------------------------------------------------
// POST /api/control/units — enqueue a unit
// ---------------------------------------------------------------------------

func TestControl_EnqueueUnit_Returns201(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	body := EnqueueUnitRequest{
		WpRef:   "WP-O2",
		Payload: json.RawMessage(`{"test": true}`),
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/units", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp WorkUnitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.WpRef != "WP-O2" {
		t.Fatalf("expected wp_ref WP-O2, got %q", resp.WpRef)
	}
	if resp.Status != "queued" {
		t.Fatalf("expected status queued, got %q", resp.Status)
	}
	if resp.ID <= 0 {
		t.Fatalf("expected positive id, got %d", resp.ID)
	}

	// Cleanup
	pool.Exec(context.Background(), "DELETE FROM work_units WHERE id = $1", resp.ID)
}

// ---------------------------------------------------------------------------
// POST /api/control/units — missing wp_ref
// ---------------------------------------------------------------------------

func TestControl_EnqueueUnit_MissingWpRef_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForControl(t)
	defer pool.Close()

	body := EnqueueUnitRequest{
		Payload: json.RawMessage(`{}`),
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/units", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ControlRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// jsonNumber converts an int64 to its string representation for URL params.
func jsonNumber(n int64) string {
	return strconv.FormatInt(n, 10)
}
