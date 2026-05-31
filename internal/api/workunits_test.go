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
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("seed event failed: status %d body %s", rec.Code, rec.Body.String())
	}
}

// TestHTTPWorkUnits_LatestStatusReachesBoundary is the F10 honesty proof at the HTTP layer:
// a session whose terminal event is session.end/done MUST surface latest_status="done" in the
// work-units response (not "running"), and a still-open session MUST surface "running".
// This is exactly the field the UI uses to derive liveness — if it were missing or wrong, the
// UI would lie about a finished agent still being alive.
func TestHTTPWorkUnits_LatestStatusReachesBoundary(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	host := "route-test-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), "DELETE FROM work_events WHERE host = $1", host)
	})

	now := time.Now().UTC().Format(time.RFC3339)
	doneSession := "wu-done-" + uuid.NewString()
	runSession := "wu-run-" + uuid.NewString()

	// DONE unit: start (running) -> end (done), correlated by external_ref + branch.
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"bounded","external_ref":"#14","branch":"route/done","sha":"deadbee1","title":"done unit"}`,
		uuid.NewString(), host, doneSession, now))
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"codex","kind":"session.end","session_id":%q,"ts":%q,"status":"done","external_ref":"#14","branch":"route/done","sha":"deadbee1","cost_usd":1.25}`,
		uuid.NewString(), host, doneSession, now))

	// RUNNING unit: start only, correlated by a different branch.
	seedEvent(t, a, fmt.Sprintf(`{"schema":"agentos.work_event/v1","event_id":%q,"host":%q,"harness":"claude","kind":"session.start","session_id":%q,"ts":%q,"status":"running","liveness_mode":"supervised","pid":4242,"external_ref":"SC-91130","branch":"route/run","sha":"cafef00d","title":"running unit"}`,
		uuid.NewString(), host, runSession, now))

	// Hit the real route.
	req := httptest.NewRequest("GET", "/?limit=200", nil)
	rec := httptest.NewRecorder()
	a.WorkUnitRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /work-units: status %d body %s", rec.Code, rec.Body.String())
	}

	var resp WorkUnitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Find our two units by branch and assert the honest aggregates reached the boundary.
	var doneU, runU *service.WorkUnit
	for i := range resp.WorkUnits {
		switch resp.WorkUnits[i].Branch {
		case "route/done":
			doneU = &resp.WorkUnits[i]
		case "route/run":
			runU = &resp.WorkUnits[i]
		}
	}
	if doneU == nil {
		t.Fatal("done unit (branch route/done) not present in work-units response")
	}
	if runU == nil {
		t.Fatal("running unit (branch route/run) not present in work-units response")
	}

	// The core honesty assertions — these would FAIL on the old query (which never
	// surfaced latest_status), proving the field is load-bearing and non-tautological.
	if doneU.LatestStatus != "done" {
		t.Errorf("done unit: expected latest_status=done, got %q", doneU.LatestStatus)
	}
	if runU.LatestStatus != "running" {
		t.Errorf("running unit: expected latest_status=running, got %q", runU.LatestStatus)
	}
	// Title + harness + cost should also reach the boundary.
	if doneU.Title != "done unit" {
		t.Errorf("done unit: expected title %q, got %q", "done unit", doneU.Title)
	}
	if len(doneU.Harnesses) == 0 || doneU.Harnesses[0] != "codex" {
		t.Errorf("done unit: expected harnesses [codex], got %v", doneU.Harnesses)
	}
	if doneU.CostUsd == nil || *doneU.CostUsd < 1.24 || *doneU.CostUsd > 1.26 {
		t.Errorf("done unit: expected cost ~1.25, got %v", doneU.CostUsd)
	}
	if !doneU.Correlated {
		t.Error("done unit: expected correlated=true")
	}
}
