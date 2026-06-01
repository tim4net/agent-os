package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// Fleet handler tests — real-PG httptest route tests
// ---------------------------------------------------------------------------

// TestHTTPFleet_MissingTenant_Returns400 proves:
// GET /api/fleet without tenant query param returns 400 (ADR-002).
func TestHTTPFleet_MissingTenant_Returns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "tenant query parameter is required" {
		t.Fatalf("unexpected error: %s", resp["error"])
	}
}

// TestHTTPFleet_EmptyFleet_ReturnsEmpty proves:
// GET /api/fleet?tenant=<unique> returns 200 with empty sessions array.
func TestHTTPFleet_EmptyFleet_ReturnsEmpty(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	tenant := "fleet-empty-" + uuid.NewString()[:8]
	req := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var fleet service.FleetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("failed to parse fleet response: %v", err)
	}
	if fleet.Total != 0 {
		t.Fatalf("expected 0 sessions, got %d", fleet.Total)
	}
	if fleet.Sessions == nil {
		t.Fatal("sessions should be empty array, not null")
	}
}

// TestHTTPFleet_SupervisedRunning_Returns200 proves:
// A supervised session with recent heartbeat returns status "running".
func TestHTTPFleet_SupervisedRunning_Returns200(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	sessionID := "fleet-ht-" + uuid.NewString()[:8]
	seedSessionStart(t, pool, sessionID, "supervised", time.Now().UTC().Add(-30*time.Second))
	seedSessionEvent(t, pool, sessionID, "session.heartbeat", "running", "supervised",
		time.Now().UTC().Add(-10*time.Second))
	t.Cleanup(func() {
		cleanupSession(t, pool, sessionID)
	})

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var fleet service.FleetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("parse fleet: %v", err)
	}

	found := findSession(fleet, sessionID)
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "running" {
		t.Fatalf("expected 'running', got %q", found.Status)
	}
	if found.Harness != "claude" {
		t.Fatalf("expected harness 'claude', got %q", found.Harness)
	}
}

// TestHTTPFleet_StaleSupervisor_ReturnsStale proves:
// A supervised session with expired heartbeat returns status "stale".
func TestHTTPFleet_StaleSupervisor_ReturnsStale(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	sessionID := "fleet-stale-ht-" + uuid.NewString()[:8]
	// Session started 10 minutes ago, no heartbeat since → stale.
	seedSessionStart(t, pool, sessionID, "supervised", time.Now().UTC().Add(-10*time.Minute))
	t.Cleanup(func() {
		cleanupSession(t, pool, sessionID)
	})

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var fleet service.FleetResponse
	json.Unmarshal(rec.Body.Bytes(), &fleet)

	found := findSession(fleet, sessionID)
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "stale" {
		t.Fatalf("expected 'stale' (heartbeat expired), got %q", found.Status)
	}
}

// TestHTTPFleet_TerminalDone proves:
// A session with session.end returns the terminal status.
func TestHTTPFleet_TerminalDone(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	sessionID := "fleet-done-ht-" + uuid.NewString()[:8]
	seedSessionStart(t, pool, sessionID, "supervised", time.Now().UTC().Add(-2*time.Minute))
	seedSessionEvent(t, pool, sessionID, "session.end", "done", "", time.Now().UTC().Add(-30*time.Second))
	t.Cleanup(func() {
		cleanupSession(t, pool, sessionID)
	})

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var fleet service.FleetResponse
	json.Unmarshal(rec.Body.Bytes(), &fleet)

	found := findSession(fleet, sessionID)
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "done" {
		t.Fatalf("expected terminal 'done', got %q", found.Status)
	}
}

// TestHTTPFleet_TenantIsolation proves:
// A dayjob session never appears in a personal tenant's fleet (ADR-002).
func TestHTTPFleet_TenantIsolation(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	dayjobSession := "fleet-dayjob-ht-" + uuid.NewString()[:8]
	seedSessionStartTenant(t, pool, dayjobSession, "supervised", "dayjob",
		time.Now().UTC().Add(-15*time.Second))
	t.Cleanup(func() {
		cleanupSession(t, pool, dayjobSession)
	})

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.FleetRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var fleet service.FleetResponse
	json.Unmarshal(rec.Body.Bytes(), &fleet)

	for _, s := range fleet.Sessions {
		if s.SessionID == dayjobSession {
			t.Fatalf("ADR-002 VIOLATION: dayjob session %s leaked into personal fleet", dayjobSession)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers for fleet handler tests
// ---------------------------------------------------------------------------

func seedSessionStart(t *testing.T, pool *pgxpool.Pool, sessionID, livenessMode string, receivedAt time.Time) {
	t.Helper()
	seedSessionStartTenant(t, pool, sessionID, livenessMode, "personal", receivedAt)
}

func seedSessionStartTenant(t *testing.T, pool *pgxpool.Pool, sessionID, livenessMode, tenant string, receivedAt time.Time) {
	t.Helper()
	ctx := t.Context()
	_, err := pool.Exec(ctx, `
		INSERT INTO work_events (
			event_id, schema_version, harness, session_id, host, pid,
			kind, status, liveness_mode, tenant, received_at, ts, payload
		) VALUES (
			$1, 'agentos.work_event/v1', 'claude', $2, 'test-host', 99992,
			'session.start', 'running', $3, $4, $5, $5, '{}'
		) ON CONFLICT (event_id) DO NOTHING
	`, uuid.NewString(), sessionID, livenessMode, tenant, receivedAt)
	if err != nil {
		t.Fatalf("seed session.start: %v", err)
	}
}

func seedSessionEvent(t *testing.T, pool *pgxpool.Pool, sessionID, kind, status, livenessMode string, receivedAt time.Time) {
	t.Helper()
	ctx := t.Context()
	_, err := pool.Exec(ctx, `
		INSERT INTO work_events (
			event_id, schema_version, harness, session_id, host, pid,
			kind, status, liveness_mode, tenant, received_at, ts, payload
		) VALUES (
			$1, 'agentos.work_event/v1', 'claude', $2, 'test-host', 99992,
			$3, $4, $5, 'personal', $6, $6, '{}'
		) ON CONFLICT (event_id) DO NOTHING
	`, uuid.NewString(), sessionID, kind, status, livenessMode, receivedAt)
	if err != nil {
		t.Fatalf("seed %s: %v", kind, err)
	}
}

func cleanupSession(t *testing.T, pool *pgxpool.Pool, sessionID string) {
	t.Helper()
	ctx := t.Context()
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

func findSession(fleet service.FleetResponse, sessionID string) *service.SessionStatus {
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			return &s
		}
	}
	return nil
}

// getTestDBNoSkip returns a pool or panics (used in test helpers).
func getTestDBNoSkip(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set")
	}
	ctx, cancel := t.Context(), func() {}
	_ = cancel
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	return pool
}
