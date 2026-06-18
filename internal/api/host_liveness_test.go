package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-N: Host-liveness API handler tests (real-PG httptest route tests)
// Each test uses a unique tenant slug to avoid cross-contamination.
// ---------------------------------------------------------------------------

// hostLivenessTestTenant returns a unique tenant name for a test.
func hostLivenessTestTenant(t *testing.T, suffix string) string {
	t.Helper()
	return "test-hl-" + suffix + "-" + uuid.NewString()[:8]
}

// newTestAPIWithDBForHostLiveness creates a test API for host-liveness tests.
func newTestAPIWithDBForHostLiveness(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := getTestDB(t)
	queries := db.New(pool)
	bus := service.NewEventBus()
	a := &API{
		queries: queries,
		bus:     bus,
	}
	return a, pool
}

// ensureHostLivenessTable creates the host_liveness table by reading and
// executing the actual migration file (N3: real migration, no hand-rolled DDL).
func ensureHostLivenessTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	migrationPath := filepath.Join("..", "..", "internal", "migrations", "000020_host_liveness.up.sql")
	// Resolve relative to the test binary's working directory
	absPath, err := filepath.Abs(migrationPath)
	if err != nil {
		t.Fatalf("resolve migration path: %v", err)
	}
	sqlBytes, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("read migration file %s: %v", absPath, err)
	}
	_, err = pool.Exec(ctx, string(sqlBytes))
	if err != nil {
		t.Fatalf("execute host_liveness migration: %v", err)
	}
}

// seedHostLiveness inserts a host_liveness row directly into the DB for tests.
func seedHostLiveness(t *testing.T, pool *pgxpool.Pool, host string, pid int32, sessionID, harness, cwd, tenant string, alive bool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO host_liveness (host, pid, session_id, harness, cwd, tenant, alive)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (host, pid) DO UPDATE SET alive = EXCLUDED.alive, seen_at = NOW(), tenant = EXCLUDED.tenant, session_id = EXCLUDED.session_id, harness = EXCLUDED.harness, cwd = EXCLUDED.cwd`,
		host, pid, sessionID, harness, cwd, tenant, alive,
	)
	if err != nil {
		t.Fatalf("seedHostLiveness: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	})
}

// ---------------------------------------------------------------------------
// POST /api/host/liveness (create/update)
// ---------------------------------------------------------------------------

func TestHTTPHostLiveness_PostNew_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "post-new")
	body := LivenessReportRequest{
		Host:    "testhost",
		PID:     12345,
		Alive:   true,
		Tenant:  tenant,
		Harness: "claude",
		CWD:     "/home/tim/work/agent-os",
	}

	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp HostLivenessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Host != "testhost" {
		t.Fatalf("expected host testhost, got %s", resp.Host)
	}
	if resp.PID != 12345 {
		t.Fatalf("expected pid 12345, got %d", resp.PID)
	}
	if !resp.Alive {
		t.Fatalf("expected alive true, got false")
	}
	if resp.Tenant != tenant {
		t.Fatalf("expected tenant %q, got %q", tenant, resp.Tenant)
	}
}

func TestHTTPHostLiveness_PostMissingHost_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	body := LivenessReportRequest{
		PID:   12345,
		Alive: true,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHostLiveness_PostInvalidPID_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	body := LivenessReportRequest{
		Host:  "testhost",
		PID:   0,
		Alive: true,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHostLiveness_PostBadJSON_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHostLiveness_PostUpsert_UpdatesExisting(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "upsert")

	// Seed an alive record
	seedHostLiveness(t, pool, "uphost", 99999, "sess-1", "claude", "/work", tenant, true)

	// POST a not-alive update
	body := LivenessReportRequest{
		Host:   "uphost",
		PID:    99999,
		Alive:  false,
		Tenant: tenant,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp HostLivenessResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Alive {
		t.Fatalf("expected alive false after upsert, got true")
	}
}

// ---------------------------------------------------------------------------
// GET /api/host/liveness (list)
// ---------------------------------------------------------------------------

func TestHTTPHostLiveness_ListEmpty_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "list-empty")
	req := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp HostLivenessListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(resp.Records))
	}
	if resp.Total != 0 {
		t.Fatalf("expected total 0, got %d", resp.Total)
	}
}

func TestHTTPHostLiveness_ListWithSeededData(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "list-data")
	seedHostLiveness(t, pool, "host-a", 1001, "sess-a", "claude", "/work/a", tenant, true)
	seedHostLiveness(t, pool, "host-b", 1002, "sess-b", "hermes", "/work/b", tenant, false)

	req := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp HostLivenessListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records, got %d; body: %s", len(resp.Records), rec.Body.String())
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}
}

func TestHTTPHostLiveness_TenantIsolation(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenantA := hostLivenessTestTenant(t, "iso-a")
	tenantB := hostLivenessTestTenant(t, "iso-b")
	seedHostLiveness(t, pool, "host-a", 2001, "sess-a", "claude", "/work/a", tenantA, true)
	seedHostLiveness(t, pool, "host-b", 2002, "sess-b", "hermes", "/work/b", tenantB, true)

	// Query tenantA → only tenantA records
	req := httptest.NewRequest("GET", "/?tenant="+tenantA, nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var respA HostLivenessListResponse
	json.Unmarshal(rec.Body.Bytes(), &respA)
	if len(respA.Records) != 1 {
		t.Fatalf("expected 1 record for tenantA, got %d", len(respA.Records))
	}
	if respA.Records[0].Tenant != tenantA {
		t.Fatalf("expected tenant %q, got %q", tenantA, respA.Records[0].Tenant)
	}
	if respA.Records[0].Host != "host-a" {
		t.Fatalf("expected host host-a, got %q", respA.Records[0].Host)
	}
}

func TestHTTPHostLiveness_ListPagination(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "pagination")
	// Seed 3 records
	seedHostLiveness(t, pool, "phost-1", 3001, "", "", "", tenant, true)
	seedHostLiveness(t, pool, "phost-2", 3002, "", "", "", tenant, true)
	seedHostLiveness(t, pool, "phost-3", 3003, "", "", "", tenant, true)

	// Page 1: limit=2, offset=0
	req := httptest.NewRequest("GET", "/?tenant="+tenant+"&limit=2&offset=0", nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp HostLivenessListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records on page 1, got %d", len(resp.Records))
	}
	if resp.Total != 3 {
		t.Fatalf("expected total 3, got %d", resp.Total)
	}
	if resp.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Fatalf("expected offset 0, got %d", resp.Offset)
	}

	// Page 2: limit=2, offset=2
	req2 := httptest.NewRequest("GET", "/?tenant="+tenant+"&limit=2&offset=2", nil)
	req2 = req2.WithContext(withTestOwner(req2.Context()))
	rec2 := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var resp2 HostLivenessListResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if len(resp2.Records) != 1 {
		t.Fatalf("expected 1 record on page 2, got %d", len(resp2.Records))
	}
}

// ---------------------------------------------------------------------------
// WP-N AC2: Feed→stale derivation — helpers
// ---------------------------------------------------------------------------

// seedBoundedSession inserts a work_events session.start for a bounded session.
func seedBoundedSession(t *testing.T, pool *pgxpool.Pool, harness, sessionID, host string, pid int32, tenant string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO work_events (
			event_id, schema_version, harness, session_id, host, pid,
			kind, status, liveness_mode, tenant, received_at, ts, payload
		) VALUES (
			$1, 'agentos.work_event/v1', $2, $3, $4, $5,
			'session.start', 'running', 'bounded', $6, $7, $8, '{}'
		) ON CONFLICT (event_id) DO NOTHING
	`,
		uuid.NewString(),
		harness,
		sessionID,
		host,
		pid,
		tenant,
		time.Now().UTC(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("seedBoundedSession: %v", err)
	}
}

// postLivenessReport POSTs a liveness report via the API handler.
func postLivenessReport(t *testing.T, a *API, req LivenessReportRequest) HostLivenessResponse {
	t.Helper()
	bodyBytes, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
	httpReq = httpReq.WithContext(withTestOwner(httpReq.Context()))
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HostLivenessRoutes().ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/host/liveness returned %d: %s", rec.Code, rec.Body.String())
	}
	var resp HostLivenessResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp
}

// ---------------------------------------------------------------------------
// WP-N AC2: Query-level derivation tests (prove the SQL join works)
// These exercise GetBoundedSessionHostLiveness directly to validate the
// data model. The production-path tests below drive GetFleet.
// ---------------------------------------------------------------------------

// queryBoundedSessionLiveness calls GetBoundedSessionHostLiveness via the queries interface.
func queryBoundedSessionLiveness(t *testing.T, queries *db.Queries, harness, sessionID, tenant string) bool {
	t.Helper()
	alive, err := queries.GetBoundedSessionHostLiveness(context.Background(), db.GetBoundedSessionHostLivenessParams{
		OwnerID:   testOwnerID(),
		Harness:   harness,
		SessionID: sessionID,
		Tenant:    tenant,
	})
	if err != nil {
		t.Fatalf("GetBoundedSessionHostLiveness: %v", err)
	}
	return alive
}

// TestBoundedSessionDerivation_AliveTrue_Running proves:
// AC2 part 1: A bounded session with host-reporter alive=true is derived as "running".
// Seeds a bounded session, POSTs alive=true, queries derivation → running.
func TestBoundedSessionDerivation_AliveTrue_Running(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	queries := db.New(pool)
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "derive-alive")
	sessionID := "sess-derive-alive-" + uuid.NewString()[:8]
	host := "derive-host"
	pid := int32(54321)

	// Seed a bounded session in work_events.
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// No liveness row yet → derivation should be false (no proof → stale).
	aliveBefore := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if aliveBefore {
		t.Fatal("expected alive=false when no host_liveness row exists (no proof → not alive)")
	}

	// POST alive=true via the API.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})

	// Now derivation should be true (positive proof → running).
	aliveAfter := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if !aliveAfter {
		t.Fatal("expected alive=true after POST alive=true (positive proof → running)")
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

// TestBoundedSessionDerivation_AliveFalse_Stale proves:
// AC2 part 2: A bounded session flips to stale when the reporter POSTs alive=false.
// Seeds a bounded session, POSTs alive=true, then alive=false → derivation should be stale.
func TestBoundedSessionDerivation_AliveFalse_Stale(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	queries := db.New(pool)
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "derive-stale")
	sessionID := "sess-derive-stale-" + uuid.NewString()[:8]
	host := "derive-host-stale"
	pid := int32(54322)

	// Seed a bounded session.
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// POST alive=true first (session is running).
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})
	aliveRunning := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if !aliveRunning {
		t.Fatal("expected alive=true after initial POST (running)")
	}

	// POST alive=false (process killed/crashed).
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  false,
		Tenant: tenant,
	})

	// Now derivation should be false (killed → stale).
	aliveKilled := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if aliveKilled {
		t.Fatal("expected alive=false after POST alive=false (killed → stale)")
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

// TestBoundedSessionDerivation_NotBefore proves:
// AC2 part 3: A bounded session does NOT flip to stale while alive=true is current.
// Seeds a bounded session, POSTs alive=true, re-queries twice → stays running.
func TestBoundedSessionDerivation_NotBefore(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	queries := db.New(pool)
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "derive-notbefore")
	sessionID := "sess-derive-notbefore-" + uuid.NewString()[:8]
	host := "derive-host-notbefore"
	pid := int32(54323)

	// Seed a bounded session.
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// POST alive=true.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})

	// Query immediately — should be running.
	alive1 := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if !alive1 {
		t.Fatal("expected alive=true (not before: session should NOT be stale while alive)")
	}

	// Query again after a tiny delay — should still be running.
	time.Sleep(10 * time.Millisecond)
	alive2 := queryBoundedSessionLiveness(t, queries, "claude", sessionID, tenant)
	if !alive2 {
		t.Fatal("expected alive=true to persist (not before: session stays running)")
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

// ---------------------------------------------------------------------------
// WP-N AC2: Production-path derivation tests (drive GetFleet)
//
// These tests exercise the ACTUAL production derivation path:
//   GetFleet → deriveSessionStatus → deriveBoundedStatus
//
// They drive the full session-liveness service, which calls the bounded-status
// derivation function. The off-limits wiring (deriveBoundedStatus consuming
// host_liveness) is proposed as a PR-body diff below.
//
// ⚠️ IMPORTANT: These tests are EXPECTED TO FAIL against the current code
// because deriveBoundedStatus (in session_liveness.go, off-limits) unconditionally
// returns "stale" without consulting host_liveness. This is by design — the
// test failure proves it exercises the gap. Once the integrator applies the
// PR-body diff to wire deriveBoundedStatus → host_liveness, these tests turn GREEN.
//
// Mutation self-check: reverting the integrator diff (restore unconditional
// "stale" return) causes these tests to go RED.
// ---------------------------------------------------------------------------

// findSessionInFleet finds a session by ID in a FleetResponse.
func findSessionInFleet(t *testing.T, fleet *service.FleetResponse, sessionID string) *service.SessionStatus {
	t.Helper()
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			return &s
		}
	}
	t.Fatalf("session %s not found in fleet (got %d sessions)", sessionID, len(fleet.Sessions))
	return nil
}

// TestBoundedSessionDerivation_ProductionPath_AliveTrueRunning proves:
// AC2 production-path part 1: A bounded session with host-reporter alive=true
// is derived as "running" through the full GetFleet → deriveBoundedStatus path.
//
// This test seeds a bounded session.start, POSTs alive=true via the host-liveness
// API, then calls GetFleet (the production endpoint) and asserts the session's
// derived status is "running" — not "stale".
//
// ⚠️ EXPECTED TO FAIL: deriveBoundedStatus currently returns "stale" unconditionally.
// Green after integrator applies the session_liveness.go wiring diff.
func TestBoundedSessionDerivation_ProductionPath_AliveTrueRunning(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "prod-alive")
	sessionID := "sess-prod-alive-" + uuid.NewString()[:8]
	host := "prod-host"
	pid := int32(64321)

	// Seed a bounded session in work_events (recent, within BoundedMaxAge).
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// No liveness row yet → GetFleet should report "stale" (no proof).
	svc := service.NewSessionLivenessService(pool)
	fleetBefore, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet before report: %v", err)
	}
	sessBefore := findSessionInFleet(t, fleetBefore, sessionID)
	if sessBefore.Status != "stale" {
		t.Fatalf("expected stale before report (no proof), got %q", sessBefore.Status)
	}

	// POST alive=true via the API.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})

	// Now GetFleet should report "running" (positive proof from host reporter).
	fleetAfter, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet after report: %v", err)
	}
	sessAfter := findSessionInFleet(t, fleetAfter, sessionID)
	if sessAfter.Status != "running" {
		t.Fatalf("expected 'running' after alive=true report (positive proof), got %q — "+
			"this FAIL is expected until integrator applies the session_liveness.go wiring diff", sessAfter.Status)
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

// TestBoundedSessionDerivation_ProductionPath_AliveFalseStale proves:
// AC2 production-path part 2: A bounded session flips to "stale" when the
// reporter POSTs alive=false, through the full GetFleet path.
//
// Seeds bounded session.start, POSTs alive=true (assert running), then
// POSTs alive=false (assert stale). Proves the killed→stale flip.
//
// ⚠️ EXPECTED TO FAIL: deriveBoundedStatus currently returns "stale" unconditionally.
// Green after integrator applies the session_liveness.go wiring diff.
func TestBoundedSessionDerivation_ProductionPath_AliveFalseStale(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "prod-stale")
	sessionID := "sess-prod-stale-" + uuid.NewString()[:8]
	host := "prod-host-stale"
	pid := int32(64322)

	// Seed a bounded session.
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// POST alive=true first → should be running.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})
	svc := service.NewSessionLivenessService(pool)
	fleetRunning, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet while running: %v", err)
	}
	sessRunning := findSessionInFleet(t, fleetRunning, sessionID)
	if sessRunning.Status != "running" {
		t.Fatalf("expected 'running' after alive=true, got %q — "+
			"this FAIL is expected until integrator applies the wiring diff", sessRunning.Status)
	}

	// POST alive=false (process killed) → should flip to stale.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  false,
		Tenant: tenant,
	})
	fleetStale, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet after kill: %v", err)
	}
	sessStale := findSessionInFleet(t, fleetStale, sessionID)
	if sessStale.Status != "stale" {
		t.Fatalf("expected 'stale' after alive=false, got %q — "+
			"this FAIL is expected until integrator applies the wiring diff", sessStale.Status)
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}

// TestBoundedSessionDerivation_ProductionPath_NotBefore proves:
// AC2 production-path part 3: A bounded session does NOT flip to stale
// while alive=true is current, through the full GetFleet path.
//
// Seeds bounded session.start, POSTs alive=true, queries GetFleet twice
// with a small delay → stays "running" both times. Proves the "not before"
// guarantee: the session is running until the explicit alive=false report.
//
// ⚠️ EXPECTED TO FAIL: deriveBoundedStatus currently returns "stale" unconditionally.
// Green after integrator applies the session_liveness.go wiring diff.
func TestBoundedSessionDerivation_ProductionPath_NotBefore(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIWithDBForHostLiveness(t)
	defer pool.Close()
	ensureHostLivenessTable(t, pool)

	tenant := hostLivenessTestTenant(t, "prod-notbefore")
	sessionID := "sess-prod-notbefore-" + uuid.NewString()[:8]
	host := "prod-host-notbefore"
	pid := int32(64323)

	// Seed a bounded session.
	seedBoundedSession(t, pool, "claude", sessionID, host, pid, tenant)

	// POST alive=true.
	postLivenessReport(t, a, LivenessReportRequest{
		Host:   host,
		PID:    pid,
		Alive:  true,
		Tenant: tenant,
	})

	// Query immediately — should be running.
	svc := service.NewSessionLivenessService(pool)
	fleet1, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet query 1: %v", err)
	}
	sess1 := findSessionInFleet(t, fleet1, sessionID)
	if sess1.Status != "running" {
		t.Fatalf("expected 'running' immediately after alive=true (not before), got %q — "+
			"this FAIL is expected until integrator applies the wiring diff", sess1.Status)
	}

	// Query again after a small delay — should still be running.
	time.Sleep(50 * time.Millisecond)
	fleet2, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet query 2: %v", err)
	}
	sess2 := findSessionInFleet(t, fleet2, sessionID)
	if sess2.Status != "running" {
		t.Fatalf("expected 'running' to persist (not before: stays running), got %q — "+
			"this FAIL is expected until integrator applies the wiring diff", sess2.Status)
	}

	// Cleanup
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM host_liveness WHERE host = $1 AND pid = $2", host, pid)
	pool.Exec(ctx, "DELETE FROM work_events WHERE session_id = $1", sessionID)
}
