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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-I: Instance API handler tests (real-PG httptest route tests)
// Each test uses a unique tenant slug to avoid cross-contamination.
// ---------------------------------------------------------------------------

// instanceTestTenant returns a unique tenant name for a test.
func instanceTestTenant(t *testing.T, suffix string) string {
	t.Helper()
	return "test-inst-" + suffix + "-" + t.Name()[:16]
}

// seedAppInstance inserts an app_instance row directly into the DB for tests.
func seedAppInstance(t *testing.T, pool *pgxpool.Pool, host, healthURL, tenant, status string, opts ...seedInstanceOpt) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()

	cfg := seedInstanceConfig{
		harness:  "claude",
		label:    "test-instance",
		branch:   "main",
		sha:      "abc123",
		cwd:      "/tmp/test",
		sessionID: uuid.NewString(),
		pid:       12345,
	}
	for _, o := range opts {
		o(&cfg)
	}

	var lastProbedAt interface{}
	if cfg.lastProbedAt != nil {
		lastProbedAt = *cfg.lastProbedAt
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO app_instances (id, harness, session_id, host, pid, label, health_url, branch, sha, cwd, tenant, status, last_probed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id.String(), cfg.harness, cfg.sessionID, host, cfg.pid, cfg.label, healthURL,
		cfg.branch, cfg.sha, cfg.cwd, tenant, status, lastProbedAt,
	)
	if err != nil {
		t.Fatalf("seedAppInstance: %v", err)
	}
	return id.String()
}

type seedInstanceConfig struct {
	harness      string
	sessionID   string
	pid          int32
	label        string
	branch       string
	sha          string
	cwd          string
	lastProbedAt *time.Time
}

type seedInstanceOpt func(*seedInstanceConfig)

func withInstanceHarness(h string) seedInstanceOpt {
	return func(c *seedInstanceConfig) { c.harness = h }
}

func withInstancePID(pid int32) seedInstanceOpt {
	return func(c *seedInstanceConfig) { c.pid = pid }
}

func withInstanceLastProbedAt(t time.Time) seedInstanceOpt {
	return func(c *seedInstanceConfig) { c.lastProbedAt = &t }
}

func withInstanceLabel(l string) seedInstanceOpt {
	return func(c *seedInstanceConfig) { c.label = l }
}

// newTestAPIWithDBForInstances creates a test API for instance tests.
// This is a separate function from the workevents one to allow isolated compilation.
func newTestAPIWithDBForInstances(t *testing.T) (*API, *pgxpool.Pool) {
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

// ---------------------------------------------------------------------------
// GET /api/instances (list)
// ---------------------------------------------------------------------------

func TestHTTPInstances_ListEmpty_Returns200(t *testing.T) {
	tenant := instanceTestTenant(t, "list-empty")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp InstancesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Instances) != 0 {
		t.Fatalf("expected 0 instances, got %d", len(resp.Instances))
	}
	if resp.Total != 0 {
		t.Fatalf("expected total 0, got %d", resp.Total)
	}
	if resp.Limit != 50 {
		t.Fatalf("expected default limit 50, got %d", resp.Limit)
	}
}

func TestHTTPInstances_ListWithSeededData(t *testing.T) {
	tenant := instanceTestTenant(t, "list-data")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	// Seed 2 instances
	seedAppInstance(t, pool, "host1", "http://localhost:8080/health", tenant, "up", withInstanceLabel("server-1"))
	seedAppInstance(t, pool, "host2", "http://localhost:8081/health", tenant, "down", withInstanceLabel("server-2"))

	req := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp InstancesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d; body: %s", len(resp.Instances), rec.Body.String())
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}
}

func TestHTTPInstances_TenantIsolation(t *testing.T) {
	tenantA := instanceTestTenant(t, "iso-a")
	tenantB := instanceTestTenant(t, "iso-b")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	seedAppInstance(t, pool, "host1", "http://localhost:8080/health", tenantA, "up")
	seedAppInstance(t, pool, "host2", "http://localhost:8081/health", tenantB, "up")

	// Query tenantA → only tenantA instances
	req := httptest.NewRequest("GET", "/?tenant="+tenantA, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var respA InstancesListResponse
	json.Unmarshal(rec.Body.Bytes(), &respA)
	if len(respA.Instances) != 1 {
		t.Fatalf("expected 1 instance for tenantA, got %d", len(respA.Instances))
	}
	if respA.Instances[0].Tenant != tenantA {
		t.Fatalf("expected tenant %q, got %q", tenantA, respA.Instances[0].Tenant)
	}

	// Query tenantB → only tenantB instances
	req2 := httptest.NewRequest("GET", "/?tenant="+tenantB, nil)
	rec2 := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var respB InstancesListResponse
	json.Unmarshal(rec2.Body.Bytes(), &respB)
	if len(respB.Instances) != 1 {
		t.Fatalf("expected 1 instance for tenantB, got %d", len(respB.Instances))
	}
}

func TestHTTPInstances_ListPagination(t *testing.T) {
	tenant := instanceTestTenant(t, "pagination")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	// Seed 3 instances
	seedAppInstance(t, pool, "host1", "http://host1:8080/health", tenant, "up")
	seedAppInstance(t, pool, "host2", "http://host2:8080/health", tenant, "up")
	seedAppInstance(t, pool, "host3", "http://host3:8080/health", tenant, "up")

	// limit=1 offset=0 → first page
	req := httptest.NewRequest("GET", "/?tenant="+tenant+"&limit=1&offset=0", nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp InstancesListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(resp.Instances))
	}
	if resp.Total != 3 {
		t.Fatalf("expected total 3, got %d", resp.Total)
	}
	if resp.Limit != 1 {
		t.Fatalf("expected limit 1, got %d", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Fatalf("expected offset 0, got %d", resp.Offset)
	}
}

// ---------------------------------------------------------------------------
// GET /api/instances/{id} (single)
// ---------------------------------------------------------------------------

func TestHTTPInstances_GetByID(t *testing.T) {
	tenant := instanceTestTenant(t, "get")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	id := seedAppInstance(t, pool, "host1", "http://localhost:8080/health", tenant, "up")

	req := httptest.NewRequest("GET", "/"+id, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp InstanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.ID != id {
		t.Fatalf("expected id %q, got %q", id, resp.ID)
	}
	if resp.Status != "up" {
		t.Fatalf("expected status 'up', got %q", resp.Status)
	}
	if resp.Host != "host1" {
		t.Fatalf("expected host 'host1', got %q", resp.Host)
	}
}

func TestHTTPInstances_GetNotFound_Returns404(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("GET", "/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "instance not found") {
		t.Fatalf("expected 'instance not found' error, got: %s", resp["error"])
	}
}

func TestHTTPInstances_GetInvalidID_Returns400(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("GET", "/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/instances (create/manual add)
// ---------------------------------------------------------------------------

func TestHTTPInstances_Create_Returns201(t *testing.T) {
	tenant := instanceTestTenant(t, "create")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	body := `{"host":"myhost","health_url":"http://myhost:3000/health","tenant":"` + tenant + `","label":"my-server","harness":"claude"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp InstanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Host != "myhost" {
		t.Fatalf("expected host 'myhost', got %q", resp.Host)
	}
	if resp.HealthURL != "http://myhost:3000/health" {
		t.Fatalf("expected health_url 'http://myhost:3000/health', got %q", resp.HealthURL)
	}
	if resp.Status != "unknown" {
		t.Fatalf("expected initial status 'unknown', got %q", resp.Status)
	}
	if resp.Tenant != tenant {
		t.Fatalf("expected tenant %q, got %q", tenant, resp.Tenant)
	}
	if resp.ID == "" {
		t.Fatal("expected non-empty id")
	}
}

func TestHTTPInstances_CreateMissingHost_Returns400(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	body := `{"health_url":"http://myhost:3000/health","tenant":"test"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "host is required") {
		t.Fatalf("expected 'host is required' error, got: %s", resp["error"])
	}
}

func TestHTTPInstances_CreateMissingHealthURL_Returns400(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	body := `{"host":"myhost","tenant":"test"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "health_url is required") {
		t.Fatalf("expected 'health_url is required' error, got: %s", resp["error"])
	}
}

func TestHTTPInstances_CreateInvalidJSON_Returns400(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHTTPInstances_CreateUpsert_Deduplicates(t *testing.T) {
	tenant := instanceTestTenant(t, "upsert")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	body := `{"host":"myhost","health_url":"http://myhost:3000/health","tenant":"` + tenant + `","label":"server-v1","harness":"claude"}`

	// First create
	req1 := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d; body: %s", rec1.Code, rec1.Body.String())
	}

	var resp1 InstanceResponse
	json.Unmarshal(rec1.Body.Bytes(), &resp1)
	firstID := resp1.ID

	// Second create with same host+url+tenant → upsert (same ID)
	body2 := `{"host":"myhost","health_url":"http://myhost:3000/health","tenant":"` + tenant + `","label":"server-v2","harness":"claude"}`
	req2 := httptest.NewRequest("POST", "/", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("upsert: expected 201, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp2 InstanceResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp2)

	// Should be the same instance (upsert updates, not creates new)
	if resp2.ID != firstID {
		t.Fatalf("upsert should return same id: %s vs %s", resp2.ID, firstID)
	}
	// Label should be updated
	if resp2.Label != "server-v2" {
		t.Fatalf("expected updated label 'server-v2', got %q", resp2.Label)
	}

	// Verify only 1 instance in DB
	req3 := httptest.NewRequest("GET", "/?tenant="+tenant, nil)
	rec3 := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec3, req3)
	var resp3 InstancesListResponse
	json.Unmarshal(rec3.Body.Bytes(), &resp3)
	if resp3.Total != 1 {
		t.Fatalf("expected 1 instance after upsert, got %d", resp3.Total)
	}
}

// ---------------------------------------------------------------------------
// POST /api/instances/{id}/probe
// ---------------------------------------------------------------------------

func TestHTTPInstances_ProbeRunningServer_ReturnsUp(t *testing.T) {
	tenant := instanceTestTenant(t, "probe-up")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	// Start a real HTTP server to probe
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	id := seedAppInstance(t, pool, "testhost", srv.URL, tenant, "unknown")

	req := httptest.NewRequest("POST", "/"+id+"/probe", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ProbeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Status != "up" {
		t.Fatalf("expected status 'up', got %q", resp.Status)
	}
	if resp.ProbedAt == "" {
		t.Fatal("expected non-empty probed_at")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected status_code 200, got %d", resp.StatusCode)
	}

	// Verify DB was updated — fetch the instance again
	reqGet := httptest.NewRequest("GET", "/"+id, nil)
	recGet := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(recGet, reqGet)
	var instResp InstanceResponse
	json.Unmarshal(recGet.Body.Bytes(), &instResp)
	if instResp.Status != "up" {
		t.Fatalf("DB status should be 'up' after probe, got %q", instResp.Status)
	}
	if instResp.LastProbedAt == nil {
		t.Fatal("DB last_probed_at should be set after probe")
	}
}

func TestHTTPInstances_ProbeStoppedServer_ReturnsDown(t *testing.T) {
	tenant := instanceTestTenant(t, "probe-down")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	// Use a port that's not listening
	id := seedAppInstance(t, pool, "deadhost", "http://localhost:1/health", tenant, "up",
		withInstanceLastProbedAt(time.Now().Add(-1*time.Hour)))

	req := httptest.NewRequest("POST", "/"+id+"/probe", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ProbeResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "down" {
		t.Fatalf("expected status 'down', got %q", resp.Status)
	}

	// Verify DB was updated
	reqGet := httptest.NewRequest("GET", "/"+id, nil)
	recGet := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(recGet, reqGet)
	var instResp InstanceResponse
	json.Unmarshal(recGet.Body.Bytes(), &instResp)
	if instResp.Status != "down" {
		t.Fatalf("DB status should be 'down' after probe, got %q", instResp.Status)
	}
}

func TestHTTPInstances_ProbeNotFound_Returns404(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("POST", "/"+uuid.NewString()+"/probe", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHTTPInstances_ProbeInvalidID_Returns400(t *testing.T) {
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	req := httptest.NewRequest("POST", "/bad-id/probe", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Anti-fake-status: unknown is the initial state, never "up" without a probe
// ---------------------------------------------------------------------------

func TestHTTPInstances_InitialStatusIsUnknown(t *testing.T) {
	// A never-reached URL shows 'unknown' (never "up").
	tenant := instanceTestTenant(t, "initial")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	id := seedAppInstance(t, pool, "neverprobed", "http://localhost:1/health", tenant, "unknown")

	req := httptest.NewRequest("GET", "/"+id, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp InstanceResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "unknown" {
		t.Fatalf("expected initial status 'unknown', got %q", resp.Status)
	}
	if resp.LastProbedAt != nil {
		t.Fatalf("expected nil last_probed_at for unprobed instance, got %v", *resp.LastProbedAt)
	}
}

// ---------------------------------------------------------------------------
// Response shape validation
// ---------------------------------------------------------------------------

func TestHTTPInstances_ResponseShape(t *testing.T) {
	tenant := instanceTestTenant(t, "shape")
	a, pool := newTestAPIWithDBForInstances(t)
	defer pool.Close()

	probeTime := time.Now().Add(-5 * time.Minute)
	id := seedAppInstance(t, pool, "host1", "http://localhost:8080/health", tenant, "up",
		withInstanceLastProbedAt(probeTime),
		withInstanceBranch("wp-i"),
		withInstanceLabel("test-server"))

	req := httptest.NewRequest("GET", "/"+id, nil)
	rec := httptest.NewRecorder()
	a.InstanceRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp InstanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify all expected fields are present
	if resp.ID == "" {
		t.Fatal("missing id")
	}
	if resp.CreatedAt == "" {
		t.Fatal("missing created_at")
	}
	if resp.UpdatedAt == "" {
		t.Fatal("missing updated_at")
	}
	if resp.Label != "test-server" {
		t.Fatalf("expected label 'test-server', got %q", resp.Label)
	}
	if resp.Branch == nil || *resp.Branch != "wp-i" {
		t.Fatalf("expected branch 'wp-i', got %v", resp.Branch)
	}
	if resp.LastProbedAt == nil {
		t.Fatal("expected last_probed_at to be set")
	}
}

func withInstanceBranch(b string) seedInstanceOpt {
	return func(c *seedInstanceConfig) { c.branch = b }
}

// Suppress unused import
var _ = os.Getenv
