package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tim4net/agent-os/internal/service"
)

// seedEvent posts one work-event through the real ingest route. Fails the test on non-2xx.
func seedEvent(t *testing.T, a *API, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req = req.WithContext(withTestOwner(req.Context()))
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("seed event failed: status %d body %s", rec.Code, rec.Body.String())
	}
}

// fetchUnits hits GET /work-units (optionally tenant-scoped) and returns the decoded response.
func fetchUnits(t *testing.T, a *API, query string) WorkUnitsResponse {
	t.Helper()
	req := httptest.NewRequest("GET", "/?limit=200"+query, nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	a.WorkUnitRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /work-units%s: status %d body %s", query, rec.Code, rec.Body.String())
	}
	var resp WorkUnitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// findByBranch returns the unit whose branch matches, or nil. Tests use globally-unique
// branch names (uuid-suffixed) so concurrent/other tests' data never collides with ours.
func findByBranch(units []service.WorkUnit, branch string) *service.WorkUnit {
	for i := range units {
		if units[i].Branch == branch {
			return &units[i]
		}
	}
	return nil
}

// TestHTTPWorkUnits_LivenessReachesBoundary is the F10 honesty proof at the HTTP layer:
// a session whose terminal event is session.end/done MUST surface liveness="done" in the
// work-units response (not "running"), and a still-open session MUST surface "running".
func TestHTTPWorkUnits_LivenessReachesBoundary(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	seedTestIngestKey(t, pool)
	host := "route-test-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host) })

	tok := uuid.NewString()[:8]
	doneBranch := "route/done-" + tok
	runBranch := "route/run-" + tok
	now := time.Now().UTC().Format(time.RFC3339)
	doneSession := "wu-done-" + uuid.NewString()
	runSession := "wu-run-" + uuid.NewString()

	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"bounded","external_ref":"#14","branch":%q,"sha":"deadbee1","title":"done unit"}`,
		uuid.NewString(), host, doneSession, now, doneBranch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.end","session_id":%q,"ts":%q,"status":"done","external_ref":"#14","branch":%q,"sha":"deadbee1","cost_usd":1.25}`,
		uuid.NewString(), host, doneSession, now, doneBranch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"supervised","pid":4242,"external_ref":"SC-91130","branch":%q,"sha":"cafef00d","title":"running unit"}`,
		uuid.NewString(), host, runSession, now, runBranch))

	resp := fetchUnits(t, a, "")
	doneU := findByBranch(resp.WorkUnits, doneBranch)
	runU := findByBranch(resp.WorkUnits, runBranch)
	if doneU == nil || runU == nil {
		t.Fatalf("expected both units present; done=%v run=%v", doneU != nil, runU != nil)
	}
	if doneU.Liveness != "done" {
		t.Errorf("done unit: expected liveness=done, got %q", doneU.Liveness)
	}
	if runU.Liveness != "running" {
		t.Errorf("running unit: expected liveness=running, got %q", runU.Liveness)
	}
	if doneU.Title != "done unit" {
		t.Errorf("done unit: expected title %q, got %q", "done unit", doneU.Title)
	}
	if len(doneU.Harnesses) == 0 || doneU.Harnesses[0] != "codex" {
		t.Errorf("done unit: expected harnesses [codex], got %v", doneU.Harnesses)
	}
	if doneU.CostUsd == nil {
		t.Errorf("done unit: expected cost ~1.25, got nil")
	} else if *doneU.CostUsd < 1.24 || *doneU.CostUsd > 1.26 {
		t.Errorf("done unit: expected cost ~1.25, got %v", *doneU.CostUsd)
	}
}

// TestHTTPWorkUnits_UnknownEventDoesNotMaskTerminal is review finding B1: a non-session
// event (artifact.created with status='unknown') arriving AFTER a session.end/done must
// NOT flip the unit back to running — only session lifecycle events drive liveness.
func TestHTTPWorkUnits_UnknownEventDoesNotMaskTerminal(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	seedTestIngestKey(t, pool)
	host := "route-b1-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host) })

	branch := "b1/x-" + uuid.NewString()[:8]
	now := time.Now().UTC()
	sess := "b1-" + uuid.NewString()
	mk := func(off time.Duration) string { return now.Add(off).Format(time.RFC3339) }

	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"bounded","external_ref":"#99","branch":%q,"sha":"aaaa1111"}`,
		uuid.NewString(), host, sess, mk(-2*time.Minute), branch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.end","session_id":%q,"ts":%q,"status":"done","external_ref":"#99","branch":%q,"sha":"aaaa1111"}`,
		uuid.NewString(), host, sess, mk(-1*time.Minute), branch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"artifact.created","session_id":%q,"ts":%q,"status":"unknown","external_ref":"#99","branch":%q,"sha":"aaaa1111","artifacts":[{"type":"note","path":"b1/out.txt"}]}`,
		uuid.NewString(), host, sess, mk(0), branch))

	u := findByBranch(fetchUnits(t, a, "").WorkUnits, branch)
	if u == nil {
		t.Fatal("b1 unit not present")
	}
	if u.Liveness != "done" {
		t.Errorf("B1: expected liveness=done despite later unknown event, got %q", u.Liveness)
	}
}

// TestHTTPWorkUnits_MultiSessionLiveness is review finding B2: a unit with one running
// session and one done session must report running (precedence), active_session_count=1.
func TestHTTPWorkUnits_MultiSessionLiveness(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	seedTestIngestKey(t, pool)
	host := "route-b2-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host) })

	branch := "b2/x-" + uuid.NewString()[:8]
	now := time.Now().UTC().Format(time.RFC3339)
	sDone := "b2-done-" + uuid.NewString()
	sRun := "b2-run-" + uuid.NewString()

	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"bounded","external_ref":"#77","branch":%q,"sha":"bbbb2222"}`,
		uuid.NewString(), host, sDone, now, branch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.end","session_id":%q,"ts":%q,"status":"done","external_ref":"#77","branch":%q,"sha":"bbbb2222"}`,
		uuid.NewString(), host, sDone, now, branch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"supervised","pid":7,"external_ref":"#77","branch":%q,"sha":"bbbb2222"}`,
		uuid.NewString(), host, sRun, now, branch))

	u := findByBranch(fetchUnits(t, a, "").WorkUnits, branch)
	if u == nil {
		t.Fatal("b2 unit not present")
	}
	if u.Liveness != "running" {
		t.Errorf("B2: a running+done multi-session unit must be running (precedence), got %q", u.Liveness)
	}
	if u.SessionCount != 2 {
		t.Errorf("B2: expected session_count=2, got %d", u.SessionCount)
	}
	if u.ActiveSessionCount != 1 {
		t.Errorf("B2: expected active_session_count=1 (only the running session), got %d", u.ActiveSessionCount)
	}
}

// TestHTTPWorkUnits_TerminalIsAbsorbing is review finding B5 + contract §4 (no resurrection):
// a session.heartbeat (status running) arriving AFTER session.end/done must NOT flip the
// session back to running. Terminal is absorbing.
func TestHTTPWorkUnits_TerminalIsAbsorbing(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	seedTestIngestKey(t, pool)
	host := "route-b5-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host) })

	branch := "b5/x-" + uuid.NewString()[:8]
	now := time.Now().UTC()
	sess := "b5-" + uuid.NewString()
	mk := func(off time.Duration) string { return now.Add(off).Format(time.RFC3339) }

	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"supervised","pid":11,"external_ref":"#55","branch":%q,"sha":"eeee5555"}`,
		uuid.NewString(), host, sess, mk(-3*time.Minute), branch))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.end","session_id":%q,"ts":%q,"status":"done","external_ref":"#55","branch":%q,"sha":"eeee5555"}`,
		uuid.NewString(), host, sess, mk(-2*time.Minute), branch))
	// A LATER heartbeat (status running) — must NOT resurrect the terminated session.
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.heartbeat","session_id":%q,"ts":%q,"status":"running","liveness_mode":"supervised","pid":11,"external_ref":"#55","branch":%q,"sha":"eeee5555"}`,
		uuid.NewString(), host, sess, mk(0), branch))

	u := findByBranch(fetchUnits(t, a, "").WorkUnits, branch)
	if u == nil {
		t.Fatal("b5 unit not present")
	}
	if u.Liveness != "done" {
		t.Errorf("B5: terminal is absorbing — a late heartbeat must NOT resurrect a done session, got liveness=%q", u.Liveness)
	}
	if u.ActiveSessionCount != 0 {
		t.Errorf("B5: expected active_session_count=0 (session is terminal), got %d", u.ActiveSessionCount)
	}
}

func TestHTTPWorkUnits_TenantScoping(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	seedTestIngestKey(t, pool)
	host := "route-b3-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host) })

	branch := "b3/x-" + uuid.NewString()[:8]
	now := time.Now().UTC().Format(time.RFC3339)
	sess := "b3-" + uuid.NewString()
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"bounded","external_ref":"#33","branch":%q,"sha":"cccc3333"}`,
		uuid.NewString(), host, sess, now, branch))

	// In-scope tenant (all events resolve to 'personal') sees it.
	if findByBranch(fetchUnits(t, a, "&tenant=personal").WorkUnits, branch) == nil {
		t.Error("B3: tenant=personal should include the personal-tenant unit")
	}
	// A different tenant must NOT see it (server-side filter, not client).
	if findByBranch(fetchUnits(t, a, "&tenant=dayjob").WorkUnits, branch) != nil {
		t.Error("B3: tenant=dayjob must NOT include a personal-tenant unit (cross-tenant leak)")
	}
}
