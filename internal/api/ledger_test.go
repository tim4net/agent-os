package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-O3: Ledger API handler tests (real-PG httptest route tests)
// ---------------------------------------------------------------------------

func ledgerTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func newTestAPIForLedger(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := ledgerTestDB(t)
	queries := db.New(pool)
	bus := service.NewEventBus()
	a := &API{
		queries: queries,
		bus:     bus,
		pool:    pool,
	}
	return a, pool
}

func ensureLedgerTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	migrationPath := filepath.Join("..", "..", "internal", "migrations", "000022_ledger.up.sql")
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
		t.Fatalf("execute ledger migration: %v", err)
	}
}

// uniqueClass generates a unique class name for test isolation.
func uniqueClass(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// ---------------------------------------------------------------------------
// POST /api/ledger/runs
// ---------------------------------------------------------------------------

func TestLedger_PostRunLog_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	wpRef := "WP-O3-TEST-" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer pool.Exec(context.Background(), "DELETE FROM run_log WHERE wp_ref = $1", wpRef)

	body := PostRunLogRequest{
		EventType: "dispatch",
		PrRef:     "#42",
		WpRef:     wpRef,
		Summary:   "test run",
		Payload:   map[string]any{"key": "val"},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/runs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp RunLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.EventType != "dispatch" {
		t.Fatalf("expected event_type dispatch, got %s", resp.EventType)
	}
	if resp.PrRef != "#42" {
		t.Fatalf("expected pr_ref #42, got %s", resp.PrRef)
	}
	if resp.WpRef != wpRef {
		t.Fatalf("expected wp_ref %s, got %s", wpRef, resp.WpRef)
	}
}

func TestLedger_PostRunLog_MissingEventType_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	body := PostRunLogRequest{
		PrRef: "#42",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/runs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /api/ledger/runs — append + query, newest-first
// ---------------------------------------------------------------------------

func TestLedger_ListRunLog_NewestFirst(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	wpRef := "WP-O3-ORDER-" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer pool.Exec(context.Background(), "DELETE FROM run_log WHERE wp_ref = $1", wpRef)

	// Insert two entries with a small delay
	body1 := PostRunLogRequest{EventType: "dispatch", PrRef: "#1", WpRef: wpRef, Summary: "first"}
	b1, _ := json.Marshal(body1)
	req1 := httptest.NewRequest("POST", "/runs", bytes.NewReader(b1))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first POST: expected 200, got %d", rec1.Code)
	}

	time.Sleep(10 * time.Millisecond)

	body2 := PostRunLogRequest{EventType: "complete", PrRef: "#2", WpRef: wpRef, Summary: "second"}
	b2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest("POST", "/runs", bytes.NewReader(b2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second POST: expected 200, got %d", rec2.Code)
	}

	// List — newest first → "complete" before "dispatch"
	req := httptest.NewRequest("GET", "/runs?limit=50&offset=0", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET runs: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var listResp RunLogListResponse
	json.Unmarshal(rec.Body.Bytes(), &listResp)

	// Find our two entries
	var first, second *RunLogResponse
	for i := range listResp.Records {
		if listResp.Records[i].WpRef == wpRef {
			if first == nil {
				first = &listResp.Records[i]
			} else {
				second = &listResp.Records[i]
				break
			}
		}
	}
	if first == nil || second == nil {
		t.Fatal("expected 2 run_log entries for our wp_ref")
	}
	if first.EventType != "complete" {
		t.Fatalf("expected newest-first (complete), got %s", first.EventType)
	}
	if second.EventType != "dispatch" {
		t.Fatalf("expected second (dispatch), got %s", second.EventType)
	}
}

// ---------------------------------------------------------------------------
// POST /api/ledger/findings
// ---------------------------------------------------------------------------

func TestLedger_PostFinding_Returns200(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls := uniqueClass("post-find")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class = $1", cls)

	body := PostFindingRequest{
		PrRef:       "#42",
		WpRef:       "WP-O2",
		Gate:        1,
		AuthorAgent: "roux",
		Model:       "glm-5.1",
		Severity:    "warning",
		Class:       cls,
		RootCause:   "missing test",
		Summary:     "no test found",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/findings", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FindingResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Class != cls {
		t.Fatalf("expected class %s, got %s", cls, resp.Class)
	}
	if resp.Severity != "warning" {
		t.Fatalf("expected severity warning, got %s", resp.Severity)
	}
	if resp.AuthorAgent != "roux" {
		t.Fatalf("expected author_agent roux, got %s", resp.AuthorAgent)
	}
}

func TestLedger_PostFinding_MissingClass_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	body := PostFindingRequest{
		PrRef:    "#42",
		Severity: "info",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/findings", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /api/ledger/findings — filter by class/severity/wp_ref
// ---------------------------------------------------------------------------

func TestLedger_FilterFindingsByClass(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	clsA := uniqueClass("cls-a")
	clsB := uniqueClass("cls-b")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class IN ($1, $2)", clsA, clsB)

	postFinding := func(cls string) {
		body := PostFindingRequest{
			PrRef: "#42", WpRef: "WP-O3", Gate: 1,
			AuthorAgent: "roux", Model: "glm-5.1",
			Severity: "info", Class: cls, Summary: "s",
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding: expected 200, got %d", rec.Code)
		}
	}
	postFinding(clsA)
	postFinding(clsA)
	postFinding(clsB)

	// Filter by classA → 2
	req := httptest.NewRequest("GET", "/findings?class="+clsA, nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET findings: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FindingsListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records for class %s, got %d", clsA, len(resp.Records))
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2 for class %s filter, got %d", clsA, resp.Total)
	}
	for _, r := range resp.Records {
		if r.Class != clsA {
			t.Fatalf("expected class %s, got %s", clsA, r.Class)
		}
	}
}

func TestLedger_FilterFindingsBySeverity(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	clsW := uniqueClass("sev-warn")
	clsI := uniqueClass("sev-info")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class IN ($1, $2)", clsW, clsI)

	postFinding := func(cls, sev string) {
		body := PostFindingRequest{
			PrRef: "#42", WpRef: "WP-O3", Gate: 1,
			AuthorAgent: "roux", Model: "glm-5.1",
			Severity: sev, Class: cls,
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding: expected 200, got %d", rec.Code)
		}
	}
	postFinding(clsW, "warning")
	postFinding(clsI, "info")

	req := httptest.NewRequest("GET", "/findings?severity=warning", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FindingsListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	// Should include our warning finding
	found := false
	for _, r := range resp.Records {
		if r.Class == clsW {
			found = true
		}
		if r.Class == clsI {
			t.Fatal("info-class finding should not appear in severity=warning filter")
		}
	}
	if !found {
		t.Fatal("expected to find warning-class finding in severity=warning results")
	}
	if resp.Total < 1 {
		t.Fatalf("expected Total >= 1 for severity=warning filter, got %d", resp.Total)
	}
}

func TestLedger_FilterFindingsByWpRef(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls := uniqueClass("wpref-f")
	wpA := "WP-O3-" + fmt.Sprintf("%d", time.Now().UnixNano())
	wpB := "WP-O4-" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class = $1", cls)

	postFinding := func(wp string) {
		body := PostFindingRequest{
			PrRef: "#42", WpRef: wp, Gate: 1,
			AuthorAgent: "roux", Model: "glm-5.1",
			Severity: "info", Class: cls,
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding: expected 200, got %d", rec.Code)
		}
	}
	postFinding(wpA)
	postFinding(wpB)

	req := httptest.NewRequest("GET", "/findings?wp_ref="+wpA, nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FindingsListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Records) != 1 {
		t.Fatalf("expected 1 record for wp_ref %s, got %d", wpA, len(resp.Records))
	}
	if resp.Records[0].WpRef != wpA {
		t.Fatalf("expected wp_ref %s, got %s", wpA, resp.Records[0].WpRef)
	}
	if resp.Total != 1 {
		t.Fatalf("expected Total=1 for wp_ref=%s filter, got %d", wpA, resp.Total)
	}
}

// ---------------------------------------------------------------------------
// GET /api/ledger/recurrence — cross-agent/wp discrimination + 3 surfaces, 2 does not
// ---------------------------------------------------------------------------

// TestLedger_RecurringFindings_CrossAgentWp proves: recurrence groups by (class, agent, wp_ref).
// A regression that drops wp_ref from GROUP BY would aggregate agent-a's 3 rows (WP-O3) with
// agent-b's 2 rows (WP-O4) to count=5 and surface both, even though agent-b has only 2.
//
// NOTE: This test discriminates the author_agent dimension (agents differ, so buggy GROUP BY
// (class, author_agent) still separates them). Use SameAgentSplitWpRef for wp_ref discrimination.
func TestLedger_RecurringFindings_CrossAgentWp(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls := uniqueClass("recur-discrim")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class = $1", cls)

	// Agent A, WP-O3: 3 findings → should surface at min_count=3
	for i := 0; i < 3; i++ {
		body := PostFindingRequest{
			PrRef: "#42", WpRef: "WP-O3", Gate: 1,
			AuthorAgent: "agent-a", Model: "glm-5.1",
			Severity: "warning", Class: cls,
			Summary: fmt.Sprintf("finding-a-%d", i),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding agent-a %d: expected 200, got %d", i, rec.Code)
		}
	}
	// Agent B, WP-O4: 2 findings of same class → should NOT surface at min_count=3
	for i := 0; i < 2; i++ {
		body := PostFindingRequest{
			PrRef: "#43", WpRef: "WP-O4", Gate: 1,
			AuthorAgent: "agent-b", Model: "glm-5.1",
			Severity: "warning", Class: cls,
			Summary: fmt.Sprintf("finding-b-%d", i),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding agent-b %d: expected 200, got %d", i, rec.Code)
		}
	}

	// min_count=3 → only (agent-a, WP-O3) should surface
	req := httptest.NewRequest("GET", "/recurrence?min_count=3", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET recurrence: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp RecurringFindingsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	var foundA, foundB bool
	for _, r := range resp.Records {
		if r.Class == cls && r.AuthorAgent == "agent-a" {
			foundA = true
			if r.WpRef != "WP-O3" {
				t.Fatalf("expected wp_ref WP-O3 for agent-a, got %s", r.WpRef)
			}
			if r.Count < 3 {
				t.Fatalf("expected count >= 3 for agent-a, got %d", r.Count)
			}
		}
		if r.Class == cls && r.AuthorAgent == "agent-b" {
			foundB = true
		}
	}
	if !foundA {
		t.Fatal("expected (agent-a, WP-O3) to surface with count >= 3")
	}
	if foundB {
		t.Fatal("expected (agent-b, WP-O4) NOT to surface — only 2 rows, below threshold 3")
	}
}

// TestLedger_RecurringFindings_3Surfaces_2DoesNot proves: with fixed min_count=3,
// a class with 3 rows surfaces and a class with 2 rows does not. Tests the literal
// near-miss that the markdown-ledger convention cares about.
func TestLedger_RecurringFindings_3Surfaces_2DoesNot(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls3 := uniqueClass("recur3")
	cls2 := uniqueClass("recur2")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class IN ($1, $2)", cls3, cls2)

	// Insert 3 findings of cls3 (same agent/wp)
	for i := 0; i < 3; i++ {
		body := PostFindingRequest{
			PrRef: "#42", WpRef: "WP-O3", Gate: 1,
			AuthorAgent: "roux", Model: "glm-5.1",
			Severity: "warning", Class: cls3,
			Summary: fmt.Sprintf("finding %d", i),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding cls3-%d: expected 200, got %d", i, rec.Code)
		}
	}
	// Insert 2 findings of cls2 (same agent/wp)
	for i := 0; i < 2; i++ {
		body := PostFindingRequest{
			PrRef: "#43", WpRef: "WP-O3", Gate: 1,
			AuthorAgent: "roux", Model: "glm-5.1",
			Severity: "warning", Class: cls2,
			Summary: fmt.Sprintf("finding 2-%d", i),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.LedgerRoutes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST finding cls2-%d: expected 200, got %d", i, rec.Code)
		}
	}

	// min_count=3 → cls3 should surface, cls2 should NOT
	req := httptest.NewRequest("GET", "/recurrence?min_count=3", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET recurrence: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp RecurringFindingsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	var found3 bool
	for _, r := range resp.Records {
		if r.Class == cls3 {
			found3 = true
			if r.Count < 3 {
				t.Fatalf("expected cls3 count >= 3, got %d", r.Count)
			}
		}
		if r.Class == cls2 {
			t.Fatal("expected cls2 NOT to appear — only 2 rows, below threshold 3")
		}
	}
	if !found3 {
		t.Fatal("expected cls3 to surface with count >= 3")
	}
}

// TestLedger_RecurringFindings_SameAgentSplitWpRef proves: a single agent's same class
// split across two wp_refs, each below threshold but summing over it, must NOT surface.
//
// MUTATION GUARD: This test MUST FAIL if wp_ref is dropped from the RecurringFindings
// GROUP BY. Under buggy GROUP BY (class, author_agent), the two sub-threshold groups
// aggregate to count=4 >= 3 and surface. Under correct GROUP BY (class, author_agent, wp_ref),
// each group stays at count=2 and neither surfaces.
func TestLedger_RecurringFindings_SameAgentSplitWpRef(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls := uniqueClass("recur-split-wp")
	wpA := "WP-ALPHA-" + fmt.Sprintf("%d", time.Now().UnixNano())
	wpB := "WP-BETA-" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class = $1", cls)

	// Same agent "roux", same class, split across two wp_refs: 2 rows each
	for i := 0; i < 2; i++ {
		for _, wp := range []string{wpA, wpB} {
			body := PostFindingRequest{
				PrRef: "#42", WpRef: wp, Gate: 1,
				AuthorAgent: "roux", Model: "glm-5.1",
				Severity: "warning", Class: cls,
				Summary: fmt.Sprintf("finding-%s-%d", wp, i),
			}
			b, _ := json.Marshal(body)
			req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			a.LedgerRoutes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST finding %s-%d: expected 200, got %d", wp, i, rec.Code)
			}
		}
	}

	// min_count=3 → neither should surface (each group has only 2)
	// A bug that dropped wp_ref from GROUP BY would aggregate to count=4 and surface
	req := httptest.NewRequest("GET", "/recurrence?min_count=3", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET recurrence: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp RecurringFindingsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, r := range resp.Records {
		if r.Class == cls {
			t.Fatalf("expected class %s NOT to surface — split across wp_refs (2+2), each below threshold 3; got count=%d", cls, r.Count)
		}
	}
}

// TestLedger_RecurringFindings_SameClassSplitAgent proves: same class + wp_ref,
// two different agents, each below threshold, must NOT surface.
//
// MUTATION GUARD: This test MUST FAIL if author_agent is dropped from GROUP BY.
// Under buggy GROUP BY (class, wp_ref), the two sub-threshold groups aggregate
// to count=4 >= 3 and surface. Under correct GROUP BY (class, author_agent, wp_ref),
// each group stays at count=2 and neither surfaces.
func TestLedger_RecurringFindings_SameClassSplitAgent(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	cls := uniqueClass("recur-split-agent")
	defer pool.Exec(context.Background(), "DELETE FROM findings WHERE class = $1", cls)

	for i := 0; i < 2; i++ {
		for _, agent := range []string{"agent-x", "agent-y"} {
			body := PostFindingRequest{
				PrRef: "#42", WpRef: "WP-SHARED", Gate: 1,
				AuthorAgent: agent, Model: "glm-5.1",
				Severity: "warning", Class: cls,
				Summary: fmt.Sprintf("finding-%s-%d", agent, i),
			}
			b, _ := json.Marshal(body)
			req := httptest.NewRequest("POST", "/findings", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			a.LedgerRoutes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST finding %s-%d: expected 200, got %d", agent, i, rec.Code)
			}
		}
	}

	// min_count=3 → neither should surface (each agent group has only 2)
	req := httptest.NewRequest("GET", "/recurrence?min_count=3", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET recurrence: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp RecurringFindingsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, r := range resp.Records {
		if r.Class == cls {
			t.Fatalf("expected class %s NOT to surface — split across agents (2+2), each below threshold 3; got count=%d", cls, r.Count)
		}
	}
}

// ---------------------------------------------------------------------------
// Bad filter values → 400
// ---------------------------------------------------------------------------

func TestLedger_BadLimit_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	req := httptest.NewRequest("GET", "/runs?limit=abc", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad limit, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestLedger_BadOffset_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	req := httptest.NewRequest("GET", "/findings?offset=-5", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative offset, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestLedger_BadMinCount_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	req := httptest.NewRequest("GET", "/recurrence?min_count=0", nil)
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for min_count=0, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestLedger_BadJSON_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool := newTestAPIForLedger(t)
	defer pool.Close()
	ensureLedgerTables(t, pool)

	req := httptest.NewRequest("POST", "/runs", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.LedgerRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
