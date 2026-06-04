package api

import (
	"context"
	"encoding/json"
	"math"
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

// epsilon for float64 cost comparisons.
const testEpsilon = 1e-6

// testFeq returns true if a and b are within testEpsilon of each other.
func testFeq(a, b float64) bool { return math.Abs(a-b) < testEpsilon }

// testTenant returns a unique tenant name for a test, e.g. "test-spend-agent-abc123".
func testTenant(t *testing.T, suffix string) string {
	t.Helper()
	return "test-spend-" + suffix + "-" + t.Name()[:16]
}

// seedWorkEvent inserts a work_event row directly into the DB for spend tests.
// sessionID: if non-empty, uses that value; otherwise generates a new UUID.
// This allows seeding multiple events for the SAME session to test cumulative
// cost dedup (contract §5: latest received_at wins per session).
func seedWorkEvent(t *testing.T, pool *pgxpool.Pool, harness, tenant string, costUsd float64, turns int, ts time.Time, opts ...seedOpt) string {
	t.Helper()
	ctx := context.Background()
	eventID := uuid.New()
	var pgEID pgtype.UUID
	_ = pgEID.Scan(eventID.String())

	cfg := seedConfig{sessionID: uuid.NewString()}
	for _, o := range opts {
		o(&cfg)
	}

	// Build payload with telemetry.turns and optional telemetry.tokens_used.
	var teleParts []string
	if turns > 0 {
		teleParts = append(teleParts, `"turns":`+strconv.Itoa(turns))
	}
	if cfg.tokens > 0 {
		teleParts = append(teleParts, `"tokens_used":`+strconv.Itoa(cfg.tokens))
	}
	if cfg.model != "" {
		teleParts = append(teleParts, `"model":"`+cfg.model+`"`)
	}
	payload := "{}"
	if len(teleParts) > 0 {
		payload = `{"telemetry":{` + strings.Join(teleParts, ",") + `}}`
	}

	projectFilter := "NULL"
	args := []interface{}{pgEID, harness, cfg.sessionID, tenant, costUsd, payload, ts.UTC().Format(time.RFC3339)}
	if cfg.projectID != "" {
		// Wrap as pgtype.UUID for consistent pgx uuid encoding (same as event_id above).
		var pgPID pgtype.UUID
		if err := pgPID.Scan(cfg.projectID); err != nil {
			t.Fatalf("invalid project UUID %q: %v", cfg.projectID, err)
		}
		projectFilter = "$8"
		args = append(args, pgPID)
	}
	if cfg.externalRef != "" {
		externalIdx := len(args) + 1
		args = append(args, cfg.externalRef)
		// Adjust the external_ref position in the query
		_, err := pool.Exec(ctx,
			`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, project_id, tenant, external_ref, branch, sha, cwd, title, cost_usd, payload, ts, received_at)
			 VALUES ($1, 'agentos.work_event/v1', $2, $3, 'testhost', 12345, 'session.end', 'done', 'supervised', `+projectFilter+`, $4, $`+strconv.Itoa(externalIdx)+`, NULL, NULL, NULL, NULL, $5, $6::jsonb, $7, NOW())`,
			args...,
		)
		if err != nil {
			t.Fatalf("seedWorkEvent: %v", err)
		}
		return eventID.String()
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, project_id, tenant, external_ref, branch, sha, cwd, title, cost_usd, payload, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', $2, $3, 'testhost', 12345, 'session.end', 'done', 'supervised', `+projectFilter+`, $4, NULL, NULL, NULL, NULL, NULL, $5, $6::jsonb, $7, NOW())`,
		args...,
	)
	if err != nil {
		t.Fatalf("seedWorkEvent: %v", err)
	}
	return eventID.String()
}

// seedConfig holds optional overrides for seedWorkEvent.
type seedConfig struct {
	sessionID   string
	projectID   string
	externalRef string
	tokens      int
	model       string
}

// seedOpt is a functional option for seedWorkEvent.
type seedOpt func(*seedConfig)

// withTokens sets telemetry.tokens_used for the event (usage metric).
func withTokens(n int) seedOpt {
	return func(c *seedConfig) { c.tokens = n }
}

// withModel sets telemetry.model for the event. Drives provider/billing
// resolution (e.g. model "gpt-5.5" → openai → metered) independent of harness.
func withModel(m string) seedOpt {
	return func(c *seedConfig) { c.model = m }
}

// costVal dereferences a nullable cost pointer, failing the test if nil.
// Use for METERED-harness rows where a real dollar cost is expected.
func costVal(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatalf("expected a non-nil cost (metered harness), got nil")
	}
	return *p
}

// withSessionID sets the session_id to a specific value (for multi-event-per-session tests).
func withSessionID(id string) seedOpt {
	return func(c *seedConfig) { c.sessionID = id }
}

// withProjectID sets the project_id for the event.
func withProjectID(id string) seedOpt {
	return func(c *seedConfig) { c.projectID = id }
}

// withExternalRef sets the external_ref for the event.
func withExternalRef(ref string) seedOpt {
	return func(c *seedConfig) { c.externalRef = ref }
}

func TestHTTPSpend_GroupByAgent_RollsUpCorrectly(t *testing.T) {
	tenant := testTenant(t, "agent")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed: claude $0.05 + 10 turns/1000 tok, claude $0.02 + 3 turns/300 tok
	//   → claude 13 turns, 1300 tokens (claude=anthropic=SUBSCRIPTION → cost suppressed)
	// hermes $0.03 + 5 turns, hermes $0.01 + 2 turns → hermes 7 turns (subscription)
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 10, time.Now(), withTokens(1000))
	seedWorkEvent(t, pool, "claude", tenant, 0.02, 3, time.Now(), withTokens(300))
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

	// claude: subscription → cost nil; usage rolls up: 2 sessions, 13 turns, 1300 tokens.
	claude, ok := byKey["claude"]
	if !ok {
		t.Fatalf("expected claude row, got keys: %v", keys(byKey))
	}
	if claude.TotalCostUsd != nil {
		t.Fatalf("claude is subscription: expected nil cost, got %v", *claude.TotalCostUsd)
	}
	if claude.BillingMode != "subscription" {
		t.Fatalf("claude billing_mode: expected subscription, got %q", claude.BillingMode)
	}
	if claude.SessionCount != 2 {
		t.Fatalf("claude sessions: expected 2, got %d", claude.SessionCount)
	}
	if claude.TotalTurns != 13 {
		t.Fatalf("claude turns: expected 13, got %d", claude.TotalTurns)
	}
	if claude.TotalTokens != 1300 {
		t.Fatalf("claude tokens: expected 1300, got %d", claude.TotalTokens)
	}

	// hermes: subscription → cost nil; 7 turns.
	hermes, ok := byKey["hermes"]
	if !ok {
		t.Fatalf("expected hermes row, got keys: %v", keys(byKey))
	}
	if hermes.TotalCostUsd != nil {
		t.Fatalf("hermes is subscription: expected nil cost, got %v", *hermes.TotalCostUsd)
	}
	if hermes.TotalTurns != 7 {
		t.Fatalf("hermes turns: expected 7, got %d", hermes.TotalTurns)
	}
}

// Blocking #1: cost_usd is cumulative per session; latest received_at wins.
// A single session emitting start=$0.05 then end=$0.07 must contribute $0.07,
// NOT $0.12.
func TestHTTPSpend_CumulativeCost_LatestWinsPerSession(t *testing.T) {
	tenant := testTenant(t, "cumul")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	sessID := uuid.NewString()

	// First event: cumulative cost = $0.05 (session start)
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 3, time.Now(), withSessionID(sessID))
	// Second event: cumulative cost = $0.07 (session end, later received_at via small sleep)
	time.Sleep(10 * time.Millisecond) // ensure later received_at
	seedWorkEvent(t, pool, "claude", tenant, 0.07, 7, time.Now(), withSessionID(sessID))

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

	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row (one session), got %d", len(resp.Rows))
	}

	// Must be 7 turns (latest), NOT 10 (sum of both events) — per-session dedup
	// (latest received_at wins) applies to the usage rollup just as it does to cost.
	if resp.Rows[0].TotalTurns != 7 {
		t.Fatalf("cumulative turns: expected 7 (latest per session), got %d — query may be SUM-ing all events", resp.Rows[0].TotalTurns)
	}
	// Session count = 1 (one session deduped).
	if resp.Rows[0].SessionCount != 1 {
		t.Fatalf("session_count: expected 1 (one session), got %d", resp.Rows[0].SessionCount)
	}
}

func TestHTTPSpend_GroupByTenant_TenantIsolation(t *testing.T) {
	tenantA := testTenant(t, "tenant-a")
	tenantB := testTenant(t, "tenant-b")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed: tenantA 5 turns, tenantB 10 turns (group_by=tenant spans providers →
	// cost suppressed; tenant ISOLATION is proven via the usage rollup, same
	// GROUP BY + WHERE path the cost would take).
	seedWorkEvent(t, pool, "claude", tenantA, 0.10, 5, time.Now())
	seedWorkEvent(t, pool, "claude", tenantB, 0.20, 10, time.Now())

	// Query with tenant=tenantA → only tenantA usage
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
	if resp.Rows[0].TotalTurns != 5 {
		t.Fatalf("tenantA turns: expected 5 (isolation), got %d", resp.Rows[0].TotalTurns)
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
	if resp2.Rows[0].TotalTurns != 10 {
		t.Fatalf("tenantB turns: expected 10 (isolation), got %d", resp2.Rows[0].TotalTurns)
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

	// day1: 5+3 = 8 turns combined; day2: 2 turns. (group_by=day → cost suppressed;
	// the date rollup invariant is identical via the usage sum.)
	d1, ok := byKey["2026-05-30"]
	if !ok {
		t.Fatalf("expected day 2026-05-30, got keys: %v", keys(byKey))
	}
	if d1.TotalTurns != 8 {
		t.Fatalf("day1 turns: expected 8, got %d", d1.TotalTurns)
	}
	// day2: 2 turns
	d2, ok := byKey["2026-05-31"]
	if !ok {
		t.Fatalf("expected day 2026-05-31, got keys: %v", keys(byKey))
	}
	if d2.TotalTurns != 2 {
		t.Fatalf("day2 turns: expected 2, got %d", d2.TotalTurns)
	}
}

// Blocking #3: group_by=project with ≥2 distinct non-NULL projects + one NULL.
// project_id is a UUID column with FK to projects(id), so we must seed real
// projects rows and use their UUIDs. The query's project_key = COALESCE(project_id::text, ''),
// so we assert dimension_key against the UUID string values.
func TestHTTPSpend_GroupByProject(t *testing.T) {
	tenant := testTenant(t, "project")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Seed real parent rows in projects to satisfy the FK constraint.
	projAlphaID := uuid.New()
	projBetaID := uuid.New()
	for _, p := range []struct{ id, slug, name string }{
		{projAlphaID.String(), "test-proj-alpha-" + uuid.NewString()[:8], "Test Project Alpha"},
		{projBetaID.String(), "test-proj-beta-" + uuid.NewString()[:8], "Test Project Beta"},
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO projects (id, slug, name, tenant) VALUES ($1, $2, $3, $4)`,
			p.id, p.slug, p.name, tenant)
		if err != nil {
			t.Fatalf("seed project %s: %v", p.slug, err)
		}
	}

	// Seed 3 events with distinct projects + one NULL project.
	// project-alpha: $0.06, 4 turns
	seedWorkEvent(t, pool, "claude", tenant, 0.06, 4, time.Now(), withProjectID(projAlphaID.String()))
	// project-beta: $0.03, 2 turns
	seedWorkEvent(t, pool, "claude", tenant, 0.03, 2, time.Now(), withProjectID(projBetaID.String()))
	// NULL project: $0.01, 1 turn (no withProjectID → NULL)
	seedWorkEvent(t, pool, "claude", tenant, 0.01, 1, time.Now())

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

	if len(resp.Rows) != 3 {
		t.Fatalf("expected 3 rows (2 projects + 1 NULL bucket), got %d", len(resp.Rows))
	}

	byKey := map[string]SpendRow{}
	for _, r := range resp.Rows {
		byKey[r.DimensionKey] = r
	}

	// proj-alpha (by UUID string): $0.06
	alpha, ok := byKey[projAlphaID.String()]
	if !ok {
		t.Fatalf("expected project %s, got keys: %v", projAlphaID.String(), keys(byKey))
	}
	if alpha.TotalTurns != 4 {
		t.Fatalf("proj-alpha turns: expected 4, got %d", alpha.TotalTurns)
	}
	// proj-beta (by UUID string): 2 turns
	beta, ok := byKey[projBetaID.String()]
	if !ok {
		t.Fatalf("expected project %s, got keys: %v", projBetaID.String(), keys(byKey))
	}
	if beta.TotalTurns != 2 {
		t.Fatalf("proj-beta turns: expected 2, got %d", beta.TotalTurns)
	}
	// NULL project → empty string key: 1 turn
	nullProj, ok := byKey[""]
	if !ok {
		t.Fatalf("expected empty-string key for NULL project, got keys: %v", keys(byKey))
	}
	if nullProj.TotalTurns != 1 {
		t.Fatalf("NULL-project turns: expected 1, got %d", nullProj.TotalTurns)
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

// Under the usage-first model the inclusion rule is "tokens OR turns OR cost > 0",
// not "cost > 0". A zero-cost session WITH usage (turns) must now appear (it's real
// work on a subscription account); a session with NO usage and NO cost must not.
func TestHTTPSpend_NoUsageNoCost_EventsNotIncluded(t *testing.T) {
	tenant := testTenant(t, "zero")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Event with cost 0 AND no turns/tokens → no activity → excluded.
	seedWorkEvent(t, pool, "claude", tenant, 0.00, 0, time.Now())

	req := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp SpendResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Rows) != 0 {
		t.Fatalf("expected 0 rows (no usage, no cost), got %d", len(resp.Rows))
	}
}

// A zero-cost session WITH usage (turns) is real subscription work and must appear,
// with cost suppressed to nil (claude=subscription).
func TestHTTPSpend_ZeroCostWithUsage_IsIncluded(t *testing.T) {
	tenant := testTenant(t, "zerouse")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	seedWorkEvent(t, pool, "claude", tenant, 0.00, 5, time.Now(), withTokens(800))

	req := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp SpendResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row (zero cost but real usage), got %d", len(resp.Rows))
	}
	if resp.Rows[0].TotalCostUsd != nil {
		t.Fatalf("claude subscription: expected nil cost, got %v", *resp.Rows[0].TotalCostUsd)
	}
	if resp.Rows[0].TotalTurns != 5 || resp.Rows[0].TotalTokens != 800 {
		t.Fatalf("expected 5 turns / 800 tokens, got %d turns / %d tokens",
			resp.Rows[0].TotalTurns, resp.Rows[0].TotalTokens)
	}
}

// A metered harness (codex=openai) keeps a real, non-nil dollar cost — proving the
// cost path still works end-to-end and the cumulative-latest-wins dedup holds.
func TestHTTPSpend_MeteredHarness_ReportsRealCost(t *testing.T) {
	tenant := testTenant(t, "metered")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	sessID := uuid.NewString()
	seedWorkEvent(t, pool, "codex", tenant, 0.05, 3, time.Now(), withSessionID(sessID))
	time.Sleep(10 * time.Millisecond)
	seedWorkEvent(t, pool, "codex", tenant, 0.07, 7, time.Now(), withSessionID(sessID))

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

	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resp.Rows))
	}
	row := resp.Rows[0]
	if row.BillingMode != "metered" {
		t.Fatalf("codex billing_mode: expected metered, got %q", row.BillingMode)
	}
	// cumulative-latest-wins: $0.07, not $0.12.
	if !testFeq(costVal(t, row.TotalCostUsd), 0.07) {
		t.Fatalf("metered cost: expected 0.07 (latest per session), got %f", *row.TotalCostUsd)
	}
	if row.SessionCount != 1 {
		t.Fatalf("session_count: expected 1 (one session deduped), got %d", row.SessionCount)
	}
}

// Regression (Gate-2 finding): a session whose harness is unmapped ("generic")
// but whose telemetry.model is an OpenAI model ("gpt-5.5") must classify as
// METERED with its real dollar cost surfaced. Before the fix the handler called
// ResolveProvider(harness, "") and ignored the model, so this returned
// billing_mode="unknown" and suppressed the cost — hiding real metered spend.
func TestHTTPSpend_GenericHarnessWithModel_ClassifiesMetered(t *testing.T) {
	tenant := testTenant(t, "genmodel")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	sessID := uuid.NewString()
	seedWorkEvent(t, pool, "generic", tenant, 0.42, 4, time.Now(),
		withSessionID(sessID), withTokens(1500), withModel("gpt-5.5"))

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
	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resp.Rows))
	}
	row := resp.Rows[0]
	if row.Provider != "openai" {
		t.Fatalf("provider: expected openai (from telemetry.model gpt-5.5), got %q", row.Provider)
	}
	if row.BillingMode != "metered" {
		t.Fatalf("billing_mode: expected metered, got %q", row.BillingMode)
	}
	if !testFeq(costVal(t, row.TotalCostUsd), 0.42) {
		t.Fatalf("metered cost: expected 0.42 surfaced, got %v", *row.TotalCostUsd)
	}
}

func TestHTTPSpend_ResponseShape(t *testing.T) {
	tenant := testTenant(t, "shape")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	seedWorkEvent(t, pool, "codex", tenant, 0.01, 1, time.Now())

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
	// Total should reflect the true group count (from COUNT(*) OVER() window),
	// not the page size.
	if resp.Total != 1 {
		t.Fatalf("expected Total=1 (one seeded group), got %d", resp.Total)
	}
	if len(resp.Rows) == 0 {
		t.Fatal("expected at least 1 row")
	}

	// Verify individual row fields. Use a metered harness (codex) so a positive
	// dollar cost is expected; also assert the always-on usage fields.
	row := resp.Rows[0]
	if row.DimensionKey == "" {
		t.Fatal("expected non-empty dimension_key")
	}
	if row.TotalCostUsd == nil || *row.TotalCostUsd <= 0 {
		t.Fatalf("expected positive cost for metered harness, got %v", row.TotalCostUsd)
	}
	if row.SessionCount <= 0 {
		t.Fatalf("expected positive session_count, got %d", row.SessionCount)
	}
	if row.TotalTurns <= 0 {
		t.Fatalf("expected positive total_turns, got %d", row.TotalTurns)
	}
	if row.BillingMode != "metered" {
		t.Fatalf("expected billing_mode=metered, got %q", row.BillingMode)
	}
}

// Blocking #2: Total reflects true group count, not page size.
func TestHTTPSpend_Total_IsGroupCount(t *testing.T) {
	tenant := testTenant(t, "total")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed 3 agents.
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 5, time.Now())
	seedWorkEvent(t, pool, "hermes", tenant, 0.03, 3, time.Now())
	seedWorkEvent(t, pool, "codex", tenant, 0.02, 2, time.Now())

	// Request with limit=2 → we get 2 rows, but Total should be 3.
	req := newTestGET("/?group_by=agent&tenant=" + tenant + "&limit=2&offset=0")
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
		t.Fatalf("expected 2 rows (limit=2), got %d", len(resp.Rows))
	}
	if resp.Total != 3 {
		t.Fatalf("expected Total=3 (true group count), got %d — Total is just the page size", resp.Total)
	}
}

// Blocking #4: external_ref filter scoping to a single work item.
func TestHTTPSpend_ExternalRef_ScopesToWorkItem(t *testing.T) {
	tenant := testTenant(t, "extref")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed two different external_refs under the same agent + tenant. Use a metered
	// harness (codex) so external_ref scoping is verified via real dollar costs.
	seedWorkEvent(t, pool, "codex", tenant, 0.05, 3, time.Now(), withExternalRef("SC-91130"))
	seedWorkEvent(t, pool, "codex", tenant, 0.03, 2, time.Now(), withExternalRef("SC-99999"))

	// Query scoped to SC-91130 → only that cost.
	req := newTestGET("/?group_by=agent&tenant=" + tenant + "&external_ref=SC-91130")
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
	if !testFeq(costVal(t, resp.Rows[0].TotalCostUsd), 0.05) {
		t.Fatalf("SC-91130 cost: expected 0.05, got %v", resp.Rows[0].TotalCostUsd)
	}

	// Query scoped to SC-99999 → only that cost.
	req2 := newTestGET("/?group_by=agent&tenant=" + tenant + "&external_ref=SC-99999")
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
	if !testFeq(costVal(t, resp2.Rows[0].TotalCostUsd), 0.03) {
		t.Fatalf("SC-99999 cost: expected 0.03, got %v", resp2.Rows[0].TotalCostUsd)
	}

	// Without external_ref → total = $0.08.
	req3 := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec3 := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec3, req3)

	if rec3.Code != 200 {
		t.Fatalf("expected 200, got %d", rec3.Code)
	}

	var resp3 SpendResponse
	json.Unmarshal(rec3.Body.Bytes(), &resp3)

	if !testFeq(costVal(t, resp3.Rows[0].TotalCostUsd), 0.08) {
		t.Fatalf("unscoped cost: expected 0.08, got %v", resp3.Rows[0].TotalCostUsd)
	}
}

// Non-blocking #7: tenant="" returns all tenants (admin-wide).
func TestHTTPSpend_EmptyTenant_ReturnsAllTenants(t *testing.T) {
	tenantA := testTenant(t, "all-a")
	tenantB := testTenant(t, "all-b")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	seedWorkEvent(t, pool, "claude", tenantA, 0.10, 5, time.Now())
	seedWorkEvent(t, pool, "claude", tenantB, 0.20, 10, time.Now())

	// Query with no tenant → empty string → all tenants.
	req := newTestGET("/?group_by=tenant")
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should have both tenants (plus potentially pre-existing test data, so check >= 2).
	byKey := map[string]SpendRow{}
	for _, r := range resp.Rows {
		byKey[r.DimensionKey] = r
	}
	if _, ok := byKey[tenantA]; !ok {
		t.Fatalf("expected tenant %q in admin-wide results, got keys: %v", tenantA, keys(byKey))
	}
	if _, ok := byKey[tenantB]; !ok {
		t.Fatalf("expected tenant %q in admin-wide results, got keys: %v", tenantB, keys(byKey))
	}
}

// Non-blocking #8: mixed-cost group — zero-cost event inside a non-zero group
// does not inflate event_count beyond the deduped session count.
func TestHTTPSpend_MixedCostGroup(t *testing.T) {
	tenant := testTenant(t, "mixed")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Session A: cost=0, turns=1 (zero-cost event)
	sessA := uuid.NewString()
	seedWorkEvent(t, pool, "codex", tenant, 0.00, 1, time.Now(), withSessionID(sessA))

	// Session B: cost=0.05, turns=5 (non-zero)
	sessB := uuid.NewString()
	seedWorkEvent(t, pool, "codex", tenant, 0.05, 5, time.Now(), withSessionID(sessB))

	req := newTestGET("/?group_by=agent&tenant=" + tenant)
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row (one agent), got %d", len(resp.Rows))
	}
	// codex is metered → cost is the sum across both deduped sessions = $0.05
	// (A contributes 0). The zero-cost session is NOT dropped; it's a real session.
	if !testFeq(costVal(t, resp.Rows[0].TotalCostUsd), 0.05) {
		t.Fatalf("expected 0.05, got %v", resp.Rows[0].TotalCostUsd)
	}
	// session_count = 2: both deduped sessions are counted (A=$0 + B=$0.05). Usage
	// rolls up across both: 1 + 5 = 6 turns.
	if resp.Rows[0].SessionCount != 2 {
		t.Fatalf("session_count: expected 2 (both sessions), got %d", resp.Rows[0].SessionCount)
	}
	if resp.Rows[0].TotalTurns != 6 {
		t.Fatalf("total_turns: expected 6 (1+5), got %d", resp.Rows[0].TotalTurns)
	}
}

// Non-blocking #6: 500 DB-error branch — forced-error via closed pool.
func TestHTTPSpend_DatabaseError_Returns500(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	// Close the pool to force all queries to fail with a connection error.
	pool.Close()

	req := newTestGET("/?group_by=agent&tenant=some-tenant")
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Fatalf("expected 500 on DB error, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// Review R3 finding #2: Total reports correct group count when offset ≥ group count.
func TestHTTPSpend_Total_AccurateWhenOffsetPastEnd(t *testing.T) {
	tenant := testTenant(t, "offset-end")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed 3 agents.
	seedWorkEvent(t, pool, "claude", tenant, 0.05, 5, time.Now())
	seedWorkEvent(t, pool, "hermes", tenant, 0.03, 3, time.Now())
	seedWorkEvent(t, pool, "codex", tenant, 0.02, 2, time.Now())

	// Request with offset=5 (past the 3 groups) → 0 rows, but Total should be 3.
	req := newTestGET("/?group_by=agent&tenant=" + tenant + "&limit=50&offset=5")
	rec := httptest.NewRecorder()
	a.SpendRoutes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp SpendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Rows) != 0 {
		t.Fatalf("expected 0 rows (offset past end), got %d", len(resp.Rows))
	}
	if resp.Total != 3 {
		t.Fatalf("expected Total=3 (true group count despite offset past end), got %d", resp.Total)
	}
	if resp.Offset != 5 {
		t.Fatalf("expected Offset=5, got %d", resp.Offset)
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
