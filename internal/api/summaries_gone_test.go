package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The conversation-summarization endpoint was decommissioned: its only frontend
// wrapper had zero callers and titling is fully handled by the title pipeline
// (immediate + deferredLLMTitle + hourly title_worker, all writing `title`).
// The handler must now respond 410 Gone, not run the old LLM batch path. The
// route mount in (off-limits) router.go and the dead FE wrapper are removed via
// this PR's integrator note at merge; this test pins the in-interim behavior and
// guards against the dead path being silently reintroduced.
func TestConversationSummary_ReturnsGone(t *testing.T) {
	a := &API{}
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/summarize",
		strings.NewReader(`{"conversation_ids":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	a.ConversationSummary(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d (410 Gone)", rec.Code, http.StatusGone)
	}
}
