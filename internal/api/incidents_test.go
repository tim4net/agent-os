package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-L: Incidents handler tests — real-PG httptest route tests
// ---------------------------------------------------------------------------

// failedSessionWorkEvent creates a valid session.end event with status=failed.
func failedSessionWorkEvent(eventID, sessionID string) string {
	req := map[string]interface{}{
		"schema":        "agentos.work_event/v1",
		"event_id":      eventID,
		"host":          "failhost",
		"harness":       "claude",
		"kind":          "session.end",
		"session_id":    sessionID,
		"ts":            time.Now().Format(time.RFC3339),
		"status":        "failed",
		"liveness_mode": "supervised",
		"pid":           98765,
		"tenant":        "personal",
		"title":         "crashed session",
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// doneSessionWorkEvent creates a valid session.end event with status=done.
func doneSessionWorkEvent(eventID, sessionID string) string {
	req := map[string]interface{}{
		"schema":        "agentos.work_event/v1",
		"event_id":      eventID,
		"host":          "donehost",
		"harness":       "hermes",
		"kind":          "session.end",
		"session_id":    sessionID,
		"ts":            time.Now().Format(time.RFC3339),
		"status":        "done",
		"liveness_mode": "supervised",
		"pid":           12345,
		"tenant":        "personal",
		"title":         "completed session",
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// ingestWorkEvent is a test helper that POSTs a work event through the ingestion
// endpoint and returns the HTTP status code (does not assert, caller checks).
func ingestWorkEvent(t *testing.T, a *API, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")
	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)
	return rec.Code
}



// requireTestDB skips the test if AOS_TEST_DATABASE_URL is not set.
func requireTestDB(t *testing.T) {
	t.Helper()
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
}

// TestIncidents_NoFailedSessions_ReturnsEmptyGreen tests that when there are no
// failed sessions, the endpoint returns an empty incidents list (honest "all green").
// Uses a unique tenant to avoid cross-test pollution from sibling tests.
func TestIncidents_NoFailedSessions_ReturnsEmptyGreen(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	req := httptest.NewRequest("GET", "/?tenant=incidents-empty-green-test", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if len(resp.Incidents) != 0 {
		t.Fatalf("expected 0 incidents (all green), got %d", len(resp.Incidents))
	}
	if resp.Total != 0 {
		t.Fatalf("expected total=0, got %d", resp.Total)
	}
}

// TestIncidents_FailedSessionSurfaces tests AC: a failed work-event surfaces
// in the incidents endpoint. No failure is buried.
// This is a REST test — the GET returning the incident proves "surfaces within
// one poll cycle". No SSE/bus assertion needed (incidents endpoint is REST-only).
func TestIncidents_FailedSessionSurfaces(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest a session.start followed by a session.end with status=failed.
	sessionID := uuid.NewString()
	startID := uuid.NewString()
	endID := uuid.NewString()

	startBody := validWorkEventJSON(startID)
	startReq := map[string]interface{}{}
	json.Unmarshal([]byte(startBody), &startReq)
	startReq["event_id"] = startID
	startReq["session_id"] = sessionID
	startReq["status"] = "running"
	startReq["kind"] = "session.start"
	startReq["liveness_mode"] = "supervised"
	startReq["pid"] = 98765
	startJSON, _ := json.Marshal(startReq)

	if code := ingestWorkEvent(t, a, string(startJSON)); code != http.StatusCreated {
		t.Fatalf("start event: expected 201, got %d", code)
	}

	// Ingest the failed session.end
	failBody := failedSessionWorkEvent(endID, sessionID)
	failReq := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &failReq)
	failReq["event_id"] = endID
	failReq["session_id"] = sessionID
	failJSON, _ := json.Marshal(failReq)

	if code := ingestWorkEvent(t, a, string(failJSON)); code != http.StatusCreated {
		t.Fatalf("failed end event: expected 201, got %d", code)
	}

	// Query the incidents endpoint (REST — proves "surfaces within one poll cycle")
	incReq := httptest.NewRequest("GET", "/?tenant=personal", nil)
	incRec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(incRec, incReq)

	if incRec.Code != http.StatusOK {
		t.Fatalf("incidents: expected 200, got %d; body: %s", incRec.Code, incRec.Body.String())
	}

	var resp IncidentsResponse
	if err := json.Unmarshal(incRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if resp.Total < 1 {
		t.Fatalf("expected total >= 1 (at least one failed session), got %d", resp.Total)
	}

	// Find the failed session incident
	found := false
	for _, inc := range resp.Incidents {
		if inc.Type == "failed_session" && inc.SessionID == sessionID && inc.Status == "failed" {
			found = true
			if inc.Harness != "claude" {
				t.Errorf("expected harness=claude, got %q", inc.Harness)
			}
			if inc.Host != "failhost" {
				t.Errorf("expected host=failhost, got %q", inc.Host)
			}
			if inc.Title != "crashed session" {
				t.Errorf("expected title='crashed session', got %q", inc.Title)
			}
			if inc.Tenant != "personal" {
				t.Errorf("expected tenant=personal, got %q", inc.Tenant)
			}
			if inc.ReceivedAt.IsZero() {
				t.Errorf("expected non-zero received_at, got zero")
			}
			break
		}
	}
	if !found {
		t.Fatalf("failed session %s not found in incidents: %+v", sessionID, resp.Incidents)
	}
}

// TestIncidents_DoneSessionDoesNotSurface tests that a session.end with status=done
// does NOT appear as an incident. Only failed sessions surface.
func TestIncidents_DoneSessionDoesNotSurface(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest a session.start + done session.end
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	startReq := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &startReq)
	startReq["session_id"] = sessionID

	doneBody := doneSessionWorkEvent(uuid.NewString(), sessionID)
	doneReq := map[string]interface{}{}
	json.Unmarshal([]byte(doneBody), &doneReq)
	doneReq["session_id"] = sessionID

	ingestWorkEvent(t, a, string(startJSON))
	ingestWorkEvent(t, a, string(doneBody))

	// Query incidents
	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// The done session must NOT appear as an incident
	for _, inc := range resp.Incidents {
		if inc.Type == "failed_session" && inc.SessionID == sessionID {
			t.Fatalf("done session %s should NOT appear as a failed incident", sessionID)
		}
	}
}

// TestIncidents_TenantIsolation tests that incidents are tenant-scoped (ADR-002).
// A failed session for tenant "personal" must not appear when querying tenant "dayjob".
func TestIncidents_TenantIsolation(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest a failed session for tenant "personal"
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	startReq := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &startReq)
	startReq["session_id"] = sessionID

	failBody := failedSessionWorkEvent(uuid.NewString(), sessionID)
	failReq := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &failReq)
	failReq["session_id"] = sessionID

	ingestWorkEvent(t, a, string(startJSON))
	ingestWorkEvent(t, a, string(failBody))

	// Query incidents with tenant=dayjob — the personal failure must not appear
	req := httptest.NewRequest("GET", "/?tenant=dayjob", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, inc := range resp.Incidents {
		if inc.SessionID == sessionID {
			t.Fatalf("tenant isolation violation: personal session %s leaked into dayjob view", sessionID)
		}
	}
}

// TestIncidents_Pagination tests that limit and offset work correctly —
// pages do not overlap by session_id. Unified pagination across all types.
func TestIncidents_Pagination(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Create 3 failed sessions
	for i := 0; i < 3; i++ {
		sid := uuid.NewString()
		startJSON := validWorkEventJSON(uuid.NewString())
		startReq := map[string]interface{}{}
		json.Unmarshal([]byte(startJSON), &startReq)
		startReq["session_id"] = sid

		failBody := failedSessionWorkEvent(uuid.NewString(), sid)
		failReq := map[string]interface{}{}
		json.Unmarshal([]byte(failBody), &failReq)
		failReq["session_id"] = sid
		failReq["host"] = "pagination-host"

		ingestWorkEvent(t, a, string(startJSON))
		ingestWorkEvent(t, a, string(failBody))
	}

	// Page 1: limit=2
	req1 := httptest.NewRequest("GET", "/?tenant=personal&limit=2&offset=0", nil)
	rec1 := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec1, req1)

	var resp1 IncidentsResponse
	json.Unmarshal(rec1.Body.Bytes(), &resp1)
	if len(resp1.Incidents) > 2 {
		t.Fatalf("page 1: expected at most 2 incidents, got %d", len(resp1.Incidents))
	}

	// Page 2: limit=2, offset=2
	req2 := httptest.NewRequest("GET", "/?tenant=personal&limit=2&offset=2", nil)
	rec2 := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec2, req2)

	var resp2 IncidentsResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp2)

	// Pages should not overlap (by session_id)
	ids1 := make(map[string]bool)
	for _, inc := range resp1.Incidents {
		ids1[inc.SessionID] = true
	}
	for _, inc := range resp2.Incidents {
		if ids1[inc.SessionID] {
			t.Fatalf("pagination overlap: session %s appears on both pages", inc.SessionID)
		}
	}
}

// TestIncidents_NewestFirst tests that incidents are ordered by received_at DESC
// (most recent failure first). This is a mutation self-check: if the subquery
// wrapper were removed and DISTINCT ON ordering leaked through, this test would fail.
func TestIncidents_NewestFirst(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest 3 failed sessions with known time ordering.
	// Add a small delay to ensure distinct received_at values.
	var sessionIDs []string
	for i := 0; i < 3; i++ {
		sid := uuid.NewString()
		sessionIDs = append(sessionIDs, sid)
		startJSON := validWorkEventJSON(uuid.NewString())
		startReq := map[string]interface{}{}
		json.Unmarshal([]byte(startJSON), &startReq)
		startReq["session_id"] = sid

		failBody := failedSessionWorkEvent(uuid.NewString(), sid)
		failReq := map[string]interface{}{}
		json.Unmarshal([]byte(failBody), &failReq)
		failReq["session_id"] = sid

		ingestWorkEvent(t, a, string(startJSON))
		ingestWorkEvent(t, a, string(failBody))

		if i < 2 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	req := httptest.NewRequest("GET", "/?tenant=personal&limit=10", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Incidents) < 2 {
		t.Fatalf("expected at least 2 incidents for ordering check, got %d", len(resp.Incidents))
	}

	// Verify each incident is more recent than or equal to the next.
	for i := 0; i < len(resp.Incidents)-1; i++ {
		a := resp.Incidents[i].ReceivedAt
		b := resp.Incidents[i+1].ReceivedAt
		if a.Before(b) {
			t.Errorf("incident %d (received_at=%v) should be >= incident %d (received_at=%v); ordering is wrong",
				i, a, i+1, b)
		}
	}
}

// TestIncidents_ResponseShape tests the response JSON structure is correct.
func TestIncidents_ResponseShape(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify Content-Type is JSON
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	// Verify response has required fields
	var raw map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &raw)

	requiredFields := []string{"incidents", "total", "limit", "offset"}
	for _, f := range requiredFields {
		if _, ok := raw[f]; !ok {
			t.Fatalf("response missing required field %q", f)
		}
	}
}

// TestIncidents_AllTenantsFilter tests that empty tenant parameter returns
// incidents across all tenants.
func TestIncidents_AllTenantsFilter(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest a failed session for personal
	personalSID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	sr := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &sr)
	sr["session_id"] = personalSID

	failBody := failedSessionWorkEvent(uuid.NewString(), personalSID)
	fr := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &fr)
	fr["session_id"] = personalSID

	ingestWorkEvent(t, a, string(startJSON))
	ingestWorkEvent(t, a, string(failBody))

	// Query with no tenant filter
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total < 1 {
		t.Fatalf("expected at least 1 incident with no tenant filter, got %d", resp.Total)
	}

	// The personal session should be visible
	found := false
	for _, inc := range resp.Incidents {
		if inc.SessionID == personalSID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("personal session %s not found in all-tenants view", personalSID)
	}
}

// TestIncidents_ClampAbsurdLimit tests that absurdly large limit values are clamped.
func TestIncidents_ClampAbsurdLimit(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Request with absurd limit
	req := httptest.NewRequest("GET", "/?tenant=personal&limit=999999999", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Limit > maxIncidentLimit {
		t.Fatalf("limit should be clamped to %d, got %d", maxIncidentLimit, resp.Limit)
	}
}

// TestIncidents_FailedThenSucceededDoesNotSurface tests finding #4:
// a session that failed then later succeeded must NOT surface as failed.
// The query uses the latest terminal status per session.
func TestIncidents_FailedThenSucceededDoesNotSurface(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest: session.start → session.end(failed) → session.end(done)
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	startReq := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &startReq)
	startReq["session_id"] = sessionID
	startReq["event_id"] = uuid.NewString()
	sj, _ := json.Marshal(startReq)
	ingestWorkEvent(t, a, string(sj))

	failBody := failedSessionWorkEvent(uuid.NewString(), sessionID)
	fr := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &fr)
	fr["session_id"] = sessionID
	fr["harness"] = "hermes" // same harness as start/done so (harness, session_id) is the same session
	fj, _ := json.Marshal(fr)
	ingestWorkEvent(t, a, string(fj))

	// Now ingest a successful end for the same session
	doneBody := doneSessionWorkEvent(uuid.NewString(), sessionID)
	dr := map[string]interface{}{}
	json.Unmarshal([]byte(doneBody), &dr)
	dr["session_id"] = sessionID
	dj, _ := json.Marshal(dr)
	ingestWorkEvent(t, a, string(dj))

	// Query incidents — the session should NOT appear as failed
	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, inc := range resp.Incidents {
		if inc.Type == "failed_session" && inc.SessionID == sessionID {
			t.Fatalf("session %s failed then succeeded — must NOT appear as failed incident", sessionID)
		}
	}
}

// TestIncidents_DownInstanceSurfaces tests that a down instance (WP-I) surfaces
// as a down_instance incident. Seeds an app_instances row with status=down and
// asserts it appears in the incidents response.
func TestIncidents_DownInstanceSurfaces(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Seed a down app_instance directly via SQL.
	ctx := t.Context()
	uniqueHealthURL := fmt.Sprintf("http://down-test-%s:9999/health", uuid.NewString()[:8])
	_, err := pool.Exec(ctx,
		`INSERT INTO app_instances (harness, session_id, host, label, health_url, tenant, status, last_probed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		 ON CONFLICT (host, health_url, tenant) DO UPDATE SET status = $7, last_probed_at = NOW()`,
		"test-harness", uuid.NewString(), "down-test-host", "down test instance", uniqueHealthURL, "personal", "down",
	)
	if err != nil {
		t.Fatalf("seed app_instance: %v", err)
	}

	req := httptest.NewRequest("GET", "/?tenant=personal", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	found := false
	for _, inc := range resp.Incidents {
		if inc.Type == "down_instance" && inc.Host == "down-test-host" {
			found = true
			if inc.Status != "down" {
				t.Errorf("expected status=down, got %q", inc.Status)
			}
			if inc.Title != "down test instance" {
				t.Errorf("expected title='down test instance', got %q", inc.Title)
			}
			if inc.Tenant != "personal" {
				t.Errorf("expected tenant=personal, got %q", inc.Tenant)
			}
			break
		}
	}
	if !found {
		t.Fatalf("down instance not found in incidents; total=%d, incidents=%+v", resp.Total, resp.Incidents)
	}
}

// TestIncidents_StaleSessionSurfaces tests that a supervised session with no
// recent heartbeat surfaces as a stale_session incident (contract §4).
// Liveness is derived from work_events — no agent_sessions table needed (WP-J).
func TestIncidents_StaleSessionSurfaces(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Create a supervised session.start event with a very old timestamp.
	// This will be older than the 5-minute stale window, making it stale.
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	startReq := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &startReq)
	startReq["event_id"] = uuid.NewString()
	startReq["session_id"] = sessionID
	startReq["status"] = "running"
	startReq["kind"] = "session.start"
	startReq["liveness_mode"] = "supervised"
	startReq["pid"] = 12345
	startReq["harness"] = "claude" // must be a valid harness (hermes|claude|antigravity|codex|generic)

	// Use a ts far enough in the past (but within the 10-min server clock skew guard)
	// so the received_at falls outside the stale window. Since received_at is server-set,
	// we need to actually set the timestamp far back. We'll insert the start event
	// and then manually UPDATE received_at to be old.
	sj, _ := json.Marshal(startReq)
	if code := ingestWorkEvent(t, a, string(sj)); code != http.StatusCreated {
		t.Fatalf("start event: expected 201, got %d", code)
	}

	// Manually backdate the received_at to well past the stale window (5 min).
	ctx := t.Context()
	_, err := pool.Exec(ctx,
		`UPDATE work_events SET received_at = NOW() - INTERVAL '10 minutes'
		 WHERE session_id = $1 AND kind = 'session.start'`,
		sessionID,
	)
	if err != nil {
		t.Fatalf("backdate work_event: %v", err)
	}

	// Use a very large stale_window to make our 10-minute-old event definitely stale.
	// The default 5-minute window should already catch it.
	req := httptest.NewRequest("GET", "/?tenant=personal&stale_window=6m", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	found := false
	for _, inc := range resp.Incidents {
		if inc.Type == "stale_session" && inc.SessionID == sessionID {
			found = true
			if inc.Status != "stale" {
				t.Errorf("expected status=stale, got %q", inc.Status)
			}
			if inc.Harness != "claude" {
				t.Errorf("expected harness=claude, got %q", inc.Harness)
			}
			break
		}
	}
	if !found {
		t.Fatalf("stale session %s not found in incidents; total=%d, incidents=%+v", sessionID, resp.Total, resp.Incidents)
	}
}

// TestIncidents_UnifiedTotal tests that Total is the true total across all incident
// types (failed sessions + down instances + stale sessions), not just failed sessions.
func TestIncidents_UnifiedTotal(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Count baseline incidents before we add anything.
	req0 := httptest.NewRequest("GET", "/?tenant=personal&limit=200", nil)
	rec0 := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec0, req0)
	var resp0 IncidentsResponse
	json.Unmarshal(rec0.Body.Bytes(), &resp0)
	baseline := resp0.Total

	// Add a failed session.
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	sr := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &sr)
	sr["session_id"] = sessionID
	ingestWorkEvent(t, a, string(startJSON))

	failBody := failedSessionWorkEvent(uuid.NewString(), sessionID)
	fr := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &fr)
	fr["session_id"] = sessionID
	ingestWorkEvent(t, a, string(failBody))

	// Add a down instance.
	uniqueHealthURL := fmt.Sprintf("http://total-test-%s:9999/health", uuid.NewString()[:8])
	pool.Exec(t.Context(),
		`INSERT INTO app_instances (harness, session_id, host, label, health_url, tenant, status, last_probed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		 ON CONFLICT (host, health_url, tenant) DO UPDATE SET status = $7, last_probed_at = NOW()`,
		"test", uuid.NewString(), "total-test-host", "total test", uniqueHealthURL, "personal", "down",
	)

	// Query all incidents with a large limit.
	req := httptest.NewRequest("GET", "/?tenant=personal&limit=200", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// Total should be baseline + 2 (one failed session + one down instance).
	expected := baseline + 2
	if resp.Total != expected {
		t.Fatalf("expected total=%d (baseline=%d + 1 failed + 1 down), got %d", expected, baseline, resp.Total)
	}

	// Both types should be present in the incidents list.
	typeCounts := map[string]int{}
	for _, inc := range resp.Incidents {
		typeCounts[inc.Type]++
	}
	if typeCounts["failed_session"] < 1 {
		t.Errorf("expected at least 1 failed_session incident, got %d", typeCounts["failed_session"])
	}
	if typeCounts["down_instance"] < 1 {
		t.Errorf("expected at least 1 down_instance incident, got %d", typeCounts["down_instance"])
	}
}

// TestIncidents_ProjectFanOutDoesNotDuplicate tests that a tenant with multiple
// projects does NOT duplicate a failed session (one session per project).
// Previously the LEFT JOIN was ON sub.tenant = p.tenant, causing N duplicates.
// Now it's ON p.id = sub.project_id — exact match, no fan-out.
func TestIncidents_ProjectFanOutDoesNotDuplicate(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Create 3 projects for tenant "personal"
	ctx := t.Context()
	for i := 0; i < 3; i++ {
		slug := fmt.Sprintf("fanout-project-%d-%s", i, uuid.NewString()[:8])
		_, err := pool.Exec(ctx,
			`INSERT INTO projects (slug, name, tenant) VALUES ($1, $2, 'personal') ON CONFLICT (slug) DO NOTHING`,
			slug, fmt.Sprintf("Fanout Project %d", i),
		)
		if err != nil {
			t.Fatalf("seed project %d: %v", i, err)
		}
	}

	// Ingest one failed session for tenant personal
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	sr := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &sr)
	sr["session_id"] = sessionID

	failBody := failedSessionWorkEvent(uuid.NewString(), sessionID)
	fr := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &fr)
	fr["session_id"] = sessionID

	ingestWorkEvent(t, a, string(startJSON))
	ingestWorkEvent(t, a, string(failBody))

	req := httptest.NewRequest("GET", "/?tenant=personal&limit=200", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// Count how many incidents have this session_id
	count := 0
	for _, inc := range resp.Incidents {
		if inc.Type == "failed_session" && inc.SessionID == sessionID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 incident for session %s with 3 projects in tenant, got %d (total=%d, incidents=%+v)",
			sessionID, count, resp.Total, resp.Incidents)
	}
}

// TestIncidents_TotalOnOutOfRangePage tests that Total is still reported
// correctly even when the offset is past all incidents (empty page).
func TestIncidents_TotalOnOutOfRangePage(t *testing.T) {
	requireTestDB(t)
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	// Ingest a failed session
	sessionID := uuid.NewString()
	startJSON := validWorkEventJSON(uuid.NewString())
	sr := map[string]interface{}{}
	json.Unmarshal([]byte(startJSON), &sr)
	sr["session_id"] = sessionID

	failBody := failedSessionWorkEvent(uuid.NewString(), sessionID)
	fr := map[string]interface{}{}
	json.Unmarshal([]byte(failBody), &fr)
	fr["session_id"] = sessionID

	ingestWorkEvent(t, a, string(startJSON))
	ingestWorkEvent(t, a, string(failBody))

	// Query with an offset past all incidents
	req := httptest.NewRequest("GET", "/?tenant=personal&limit=10&offset=99999", nil)
	rec := httptest.NewRecorder()
	a.IncidentRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp IncidentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Incidents) != 0 {
		t.Fatalf("expected 0 incidents on out-of-range page, got %d", len(resp.Incidents))
	}
	if resp.Total < 1 {
		t.Fatalf("expected total >= 1 on out-of-range page (incidents exist but offset is past them), got %d", resp.Total)
	}
}

// ensure imports used
var _ = service.DefaultSupervisedStaleWindow
