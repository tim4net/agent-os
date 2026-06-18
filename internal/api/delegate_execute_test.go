package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// newDelegateReq builds a POST /delegate httptest request with the chi URL-param
// ("id") context wired up, since DelegateToAgent reads chi.URLParam(r, "id").
func newDelegateReq(t *testing.T, sourceID string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sourceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestDelegateToAgent_RejectsOversizedMessage proves the input-length guard
// fires with 413 BEFORE any DB or harness access. A bare &API{} (nil deps) is
// sufficient because the handler returns before touching queries/registry/bus.
func TestDelegateToAgent_RejectsOversizedMessage(t *testing.T) {
	a := &API{} // no DB/harness needed: validation short-circuits before they're used

	src := uuid.NewString()
	tgt := uuid.NewString()
	oversized := bytes.Repeat([]byte("x"), maxDelegateMessageLen+1)

	req := newDelegateReq(t, src, map[string]string{
		"target_agent_id": tgt,
		"message":         string(oversized),
	})
	rec := httptest.NewRecorder()
	a.DelegateToAgent(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized message, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestDelegateToAgent_RejectsOversizedSystemPrompt covers the system_prompt cap.
func TestDelegateToAgent_RejectsOversizedSystemPrompt(t *testing.T) {
	a := &API{}

	src := uuid.NewString()
	tgt := uuid.NewString()
	oversizedPrompt := bytes.Repeat([]byte("y"), maxDelegateSystemPromptLen+1)

	req := newDelegateReq(t, src, map[string]string{
		"target_agent_id": tgt,
		"message":         "hello",
		"system_prompt":   string(oversizedPrompt),
	})
	rec := httptest.NewRecorder()
	a.DelegateToAgent(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized system_prompt, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestDelegateToAgent_RejectsOversizedModel covers the model cap.
func TestDelegateToAgent_RejectsOversizedModel(t *testing.T) {
	a := &API{}

	src := uuid.NewString()
	tgt := uuid.NewString()
	oversizedModel := bytes.Repeat([]byte("m"), maxDelegateModelLen+1)

	req := newDelegateReq(t, src, map[string]string{
		"target_agent_id": tgt,
		"message":         "hello",
		"model":           string(oversizedModel),
	})
	rec := httptest.NewRecorder()
	a.DelegateToAgent(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized model, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestDelegateToAgent_AcceptsMaxSizeMessage confirms a message exactly at the
// cap is NOT rejected by the length guard (boundary check). We reuse the same
// UUID for source and target so the handler short-circuits at the
// self-delegation guard (400) before reaching a.queries (nil here) — any
// non-413 code proves the length guard let the at-cap message through.
func TestDelegateToAgent_AcceptsMaxSizeMessage(t *testing.T) {
	a := &API{}

	// Same UUID for source (URL param) and target so we stop at the
	// self-delegation check, avoiding the nil-DB GetAgent call.
	id := uuid.NewString()
	atCap := bytes.Repeat([]byte("z"), maxDelegateMessageLen)

	req := newDelegateReq(t, id, map[string]string{
		"target_agent_id": id,
		"message":         string(atCap),
	})
	rec := httptest.NewRecorder()
	a.DelegateToAgent(rec, req)

	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("at-cap message was wrongly rejected with 413; body: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (self-delegation) for at-cap message, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
