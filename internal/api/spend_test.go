package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// WP-K: Spend API handler tests (real-PG httptest route tests)
// Each test uses a unique tenant slug to avoid cross-contamination with
// existing rows or other concurrent tests.
// ---------------------------------------------------------------------------

// testTenant returns a unique tenant name for a test, e.g. "test-spend-agent-abc123".
func testTenant(t *testing.T, suffix string) string {
	t.Helper()
	return "test-spend-" + suffix + "-" + t.Name()[:16]
}

// seedWorkEvent inserts a work_event row directly into the DB for spend tests.
func seedWorkEvent(t *testing.T, pool *pgxpool.Pool, harness, tenant string, costUsd float64, turns int, ts time.Time) string {
	t.Helper()
	ctx := context.Background()
	eventID := uuid.New()
	var pgEID pgtype.UUID
	_ = pgEID.Scan(eventID.String())

	// Build payload with telemetry.turns.
	payload := "{}"
	if turns > 0 {
		payload = `{"telemetry":{"turns":` + strconv.Itoa(turns) + `}}`
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, project_id, tenant, external_ref, branch, sha, cwd, title, cost_usd, payload, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', $2, $3, 'testhost', 12345, 'session.end', 'done', 'supervised', NULL, $4, NULL, NULL, NULL, NULL, NULL, $5, $6::jsonb, $7, NOW())`,
		pgEID,
		harness,
		uuid.NewString(),
		tenant,
		costUsd,
		payload,
		ts.UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("seedWorkEvent: %v", err)
	}
	return eventID.String()
}

func TestHTTPSpend_GroupByAgent_RollsUpCorrectly(t *testing.T) {
	tenant := testTenant(t, "agent")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed: claude $0.05 + 10 turns, claude $0.02 + 3 turns → claude total $0.07, 13 turns
	// hermes $0.03 + 5 turns, hermes $0.01 + 2 turns → hermes total $0.04, 7 turns
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 10, time.Now())
	seedWorkEvent(t, pool, "claude", tenant, 0.02, 3, time.Now())
	seedWorkEvent(t, pool, "hermes", tenant, 0.03, 5, time.Now())
	seedWorkEvent(t, pool, "hermes", tenant, 0.01, 2, time.Now())

	req := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Build a map by dimension_key for easy lookup.
	byKey := map[string]SpendRow{}
	for _, r := range resp.Rows {
		byKey[r.DimensionKey] = r
	}

	if len(byKey) != 2 {
		t.Fatalf("expected 2 rows, got %d; keys: %v", len(byKey), keys(byKey))
	}

	// claude: $0.07, 2 events, 13 turns
	claude, ok := byKey["claude"]
	if !ok {
		t.Fatalf("expected claude row, got keys: %v", keys(byKey))
	}
	if claude.TotalCostUsd != 0.07 {
		t.Fatalf("claude cost: expected 0.07, got %f", claude.TotalCostUsd)
	}
	if claude.EventCount != 2 {
		t.Fatalf("claude events: expected 2, got %d", claude.EventCount)
	}
	if claude.TotalTurns != 13 {
		t.Fatalf("claude turns: expected 13, got %d", claude.TotalTurns)
	}

	// hermes: $0.04, 2 events, 7 turns
	hermes, ok := byKey["hermes"]
	if !ok {
		t.Fatalf("expected hermes row, got keys: %v", keys(byKey))
	}
	if hermes.TotalCostUsd != 0.04 {
		t.Fatalf("hermes cost: expected 0.04, got %f", hermes.TotalCostUsd)
	}
	if hermes.TotalTurns != 7 {
		t.Fatalf("hermes turns: expected 7, got %d", hermes.TotalTurns)
	}
}

func TestHTTPSpend_GroupByTenant_TenantIsolation(t *testing.T) {
	tenantA := testTenant(t, "tenant-a")
	tenantB := testTenant(t, "tenant-b")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed: tenantA $0.10, tenantB $0.20
	seedWorkEvent(t, pool, "claude", tenantA, 0.10, 5, time.Now())
	seedWorkEvent(t, pool, "claude", tenantB, 0.20, 10, time.Now())

	// Query with tenant=tenantA → only tenantA costs
	req := newTestGET("/?group_by=tenant&tenant=" + tenantA)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resp.Rows))
	}
	if resp.Rows[0].DimensionKey != tenantA {
		t.Fatalf("expected %q, got %s", tenantA, resp.Rows[0].DimensionKey)
	}
	if resp.Rows[0].TotalCostUsd != 0.10 {
		t.Fatalf("expected 0.10, got %f", resp.Rows[0].TotalCostUsd)
	}

	// Query with tenant=tenantB → only tenantB costs (tenant isolation)
	req2 := newTestGET("/?group_by=tenant&tenant=" + tenantB)
	rec2 := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec2, req2)

	if rec2.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp2 SpendResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp2.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resp2.Rows))
	}
	if resp2.Rows[0].DimensionKey != tenantB {
		t.Fatalf("expected %q, got %s", tenantB, resp2.Rows[0].DimensionKey)
	}
	if resp2.Rows[0].TotalCostUsd != 0.20 {
		t.Fatalf("expected 0.20, got %f", resp2.Rows[0].TotalCostUsd)
	}
}

func TestHTTPSpend_GroupByDay_RollsUpByDate(t *testing.T) {
	tenant := testTenant(t, "day")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	day1 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	seedWorkEvent(t, pool, "claude", tenant, 0.05, 5, day1)
	seedWorkEvent(t, pool, "claude", tenant, 0.03, 3, day1) // same day → combined
	seedWorkEvent(t, pool, "claude", tenant, 0.04, 2, day2)

	req := newTestGET("/?group_by=day&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Rows) != 2 {
		t.Fatalf("expected 2 rows (one per day), got %d", len(resp.Rows))
	}

	byKey := map[string]SpendRow{}
	for _, r := range resp.Rows {
		byKey[r.DimensionKey] = r
	}

	// day1: $0.08
	d1, ok := byKey["2026-05-30"]
	if !ok {
		t.Fatalf("expected day 2026-05-30, got keys: %v", keys(byKey))
	}
	if d1.TotalCostUsd != 0.08 {
		t.Fatalf("day1 cost: expected 0.08, got %f", d1.TotalCostUsd)
	}
	// day2: $0.04
	d2, ok := byKey["2026-05-31"]
	if !ok {
		t.Fatalf("expected day 2026-05-31, got keys: %v", keys(byKey))
	}
	if d2.TotalCostUsd != 0.04 {
		t.Fatalf("day2 cost: expected 0.04, got %f", d2.TotalCostUsd)
	}
}

func TestHTTPSpend_GroupByProject(t *testing.T) {
	tenant := testTenant(t, "project")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed events with no project (project_id = NULL) → dimension_key = "" (empty).
	// We test that the handler returns data grouped by project.
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 5, time.Now())

	req := newTestGET("/?group_by=project&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
	// Events without project_id should group under empty string key.
	if resp.Rows[0].DimensionKey != "" {
		t.Fatalf("expected empty project_key (no project), got %q", resp.Rows[0].DimensionKey)
	}
	if resp.Rows[0].TotalCostUsd != 0.05 {
		t.Fatalf("expected 0.05, got %f", resp.Rows[0].TotalCostUsd)
	}
}

func TestHTTPSpend_InvalidGroupBy_Returns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	req := newTestGET("/?group_by=invalid")
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid group_by") {
		t.Fatalf("expected 'invalid group_by' error, got: %s", resp["error"])
	}
}

func TestHTTPSpend_DefaultGroupBy_IsAgent(t *testing.T) {
	tenant := testTenant(t, "default")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	seedWorkEvent(t, pool, "claude", tenant, 0.05, 5, time.Now())

	// No group_by parameter → defaults to agent
	req := newTestGET("/?tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
	if resp.Rows[0].DimensionKey != "claude" {
		t.Fatalf("expected 'claude' (default group_by=agent), got %s", resp.Rows[0].DimensionKey)
	}
}

func TestHTTPSpend_ZeroCost_EventsNotIncluded(t *testing.T) {
	tenant := testTenant(t, "zero")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed event with cost_usd = 0 → HAVING SUM(cost_usd) > 0 excludes it
	seedWorkEvent(t, pool, "claude", tenant, 0.00, 5, time.Now())

	req := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp SpendResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Rows) != 0 {
		t.Fatalf("expected 0 rows (zero cost excluded), got %d", len(resp.Rows))
	}
}

func TestHTTPSpend_ResponseShape(t *testing.T) {
	tenant := testTenant(t, "shape")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	seedWorkEvent(t, pool, "claude", tenant, 0.01, 1, time.Now())

	req := newTestGET("/?group_by=agent&tenant=" + tenant + "&limit=5&offset=0")
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	// Verify top-level fields
	if resp.Limit != 5 {
		t.Fatalf("expected limit=5, got %d", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Fatalf("expected offset=0, got %d", resp.Offset)
	}
	if len(resp.Rows) == 0 {
		t.Fatal("expected at least 1 row")
	}

	// Verify individual row fields
	row := resp.Rows[0]
	if row.DimensionKey == "" {
		t.Fatal("expected non-empty dimension_key")
	}
	if row.TotalCostUsd <= 0 {
		t.Fatalf("expected positive cost, got %f", row.TotalCostUsd)
	}
	if row.EventCount <= 0 {
		t.Fatalf("expected positive event_count, got %d", row.EventCount)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestGET(path string) *http.Request {
	return httptest.NewRequest("GET", path, nil)
}

func keys(m map[string]SpendRow) []string {
	k := make([]string, 0, len(m))
	for k2 := range m {
		k = append(k, k2)
	}
	return k
}
