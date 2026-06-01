package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

// ensureHostLivenessTable creates the host_liveness table if it doesn't exist.
// Needed for tests running before the migration runner has been applied.
func ensureHostLivenessTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// Try to create — if already exists, it's a no-op (IF NOT EXISTS).
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS host_liveness (
			id          BIGSERIAL       PRIMARY KEY,
			host        TEXT            NOT NULL,
			pid         INT             NOT NULL,
			session_id  TEXT            NOT NULL DEFAULT '',
			harness     TEXT            NOT NULL DEFAULT '',
			cwd         TEXT            NOT NULL DEFAULT '',
			tenant      TEXT            NOT NULL DEFAULT 'personal',
			alive       BOOLEAN         NOT NULL DEFAULT TRUE,
			seen_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			UNIQUE (host, pid)
		)
	`)
	if err != nil {
		t.Fatalf("ensureHostLivenessTable: %v", err)
	}
	_, err = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_host_liveness_host ON host_liveness (host)`)
	if err != nil {
		t.Fatalf("ensureHostLivenessTable: %v", err)
	}
	_, err = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_host_liveness_tenant ON host_liveness (tenant)`)
	if err != nil {
		t.Fatalf("ensureHostLivenessTable: %v", err)
	}
}

// seedHostLiveness inserts a host_liveness row directly into the DB for tests.
func seedHostLiveness(t *testing.T, pool *pgxpool.Pool, host string, pid int32, sessionID, harness, cwd, tenant string, alive bool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO host_liveness (host, pid, session_id, harness, cwd, tenant, alive)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (host, pid) DO UPDATE SET alive = EXCLUDED.alive, seen_at = NOW()`,
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
		Host:  "uphost",
		PID:   99999,
		Alive: false,
		Tenant: tenant,
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bodyBytes))
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
