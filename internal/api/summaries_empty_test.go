package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// chatCompletionsServer returns a test server that responds to
// /v1/chat/completions with a single choice carrying the given content.
func chatCompletionsServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(content) + `}}]}`))
	}))
}

// jsonString quotes a string for embedding in a JSON literal (handles the empty
// and whitespace cases this test cares about; not a general-purpose encoder).
func jsonString(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}

// An LLM that returns empty content must produce an error, NOT ("", nil) — so
// callers keep the existing (good) title instead of overwriting it with a blank
// that renders as "New conversation".
func TestGenerateSummary_EmptyContentReturnsError(t *testing.T) {
	srv := chatCompletionsServer(t, "")
	defer srv.Close()

	a := &API{litellmURL: srv.URL, llmModel: "test-model"}
	summary, err := a.generateSummary(context.Background(), "User: hi\nAssistant: hello\n")
	if err == nil {
		t.Fatalf("expected error for empty LLM content, got summary=%q nil err", summary)
	}
	if summary != "" {
		t.Fatalf("expected empty summary on error, got %q", summary)
	}
}

// Whitespace-only content (after quote-stripping) is also not a valid title.
func TestGenerateSummary_WhitespaceOnlyReturnsError(t *testing.T) {
	srv := chatCompletionsServer(t, "   \n  ")
	defer srv.Close()

	a := &API{litellmURL: srv.URL, llmModel: "test-model"}
	_, err := a.generateSummary(context.Background(), "User: hi\n")
	if err == nil {
		t.Fatal("expected error for whitespace-only LLM content, got nil")
	}
}

// A quote-wrapped whitespace payload (`" "`) must also be rejected — the trim
// happens after quote-stripping.
func TestGenerateSummary_QuotedWhitespaceReturnsError(t *testing.T) {
	srv := chatCompletionsServer(t, `"   "`)
	defer srv.Close()

	a := &API{litellmURL: srv.URL, llmModel: "test-model"}
	_, err := a.generateSummary(context.Background(), "User: hi\n")
	if err == nil {
		t.Fatal("expected error for quoted-whitespace LLM content, got nil")
	}
}

// A genuine non-empty summary still succeeds (guards against over-rejecting).
func TestGenerateSummary_ValidContentSucceeds(t *testing.T) {
	srv := chatCompletionsServer(t, "Fix voice transcription errors")
	defer srv.Close()

	a := &API{litellmURL: srv.URL, llmModel: "test-model"}
	summary, err := a.generateSummary(context.Background(), "User: my voice notes are wrong\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != "Fix voice transcription errors" {
		t.Fatalf("summary = %q, want %q", summary, "Fix voice transcription errors")
	}
}
