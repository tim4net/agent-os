package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// drainChat collects all chunks from a chat channel until it closes or a
// timeout fires, returning the assembled content and whether a Done chunk was
// observed. A Done is what the SSE consumer (internal/api/chat.go) gates
// assistant-message persistence on; without it the response is silently
// discarded and a brand-new conversation is rolled back.
func drainChat(t *testing.T, ch <-chan ChatChunk) (content string, sawDone bool, sawErr bool) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return content, sawDone, sawErr
			}
			if chunk.Error != nil {
				sawErr = true
			}
			content += chunk.Content
			if chunk.Done {
				sawDone = true
			}
		case <-timeout:
			t.Fatal("timed out draining chat channel")
			return content, sawDone, sawErr
		}
	}
}

// sseServer streams the given raw SSE body and then closes the connection —
// deliberately WITHOUT a trailing `data: [DONE]` — to simulate a provider that
// terminates via finish_reason:"length", content_filter, or a clean EOF.
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
}

// LiteLLM: a stream that ends without [DONE] or finish_reason:"stop" (here
// finish_reason:"length", the max_tokens case) must still yield a Done so the
// streamed content is persisted, not discarded.
func TestLiteLLMChatEmitsDoneOnImplicitTermination(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"Hello "}}]}
data: {"choices":[{"delta":{"content":"world"},"finish_reason":"length"}]}
`
	srv := sseServer(t, body)
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	ch, err := l.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{Model: "x"})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}

	content, sawDone, sawErr := drainChat(t, ch)
	if sawErr {
		t.Fatalf("unexpected error chunk; content=%q", content)
	}
	if content != "Hello world" {
		t.Fatalf("content = %q, want %q", content, "Hello world")
	}
	if !sawDone {
		t.Fatal("no Done chunk on implicit (finish_reason:length) termination — response would be silently discarded and a new conversation rolled back")
	}
}

// LiteLLM: a clean EOF after content (no finish_reason at all) must also Done.
func TestLiteLLMChatEmitsDoneOnCleanEOF(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"partial answer"}}]}
`
	srv := sseServer(t, body)
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	ch, err := l.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{Model: "x"})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}

	content, sawDone, _ := drainChat(t, ch)
	if content != "partial answer" {
		t.Fatalf("content = %q, want %q", content, "partial answer")
	}
	if !sawDone {
		t.Fatal("no Done chunk on clean EOF — streamed content would be lost")
	}
}

// Hermes raw chat: same implicit-termination guarantee as litellm.
func TestHermesChatEmitsDoneOnImplicitTermination(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"Hi "}}]}
data: {"choices":[{"delta":{"content":"there"},"finish_reason":"length"}]}
`
	srv := sseServer(t, body)
	defer srv.Close()

	h := &HermesHarness{baseURL: srv.URL, httpClient: srv.Client()}
	ch, err := h.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{Model: "x"})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}

	content, sawDone, sawErr := drainChat(t, ch)
	if sawErr {
		t.Fatalf("unexpected error chunk; content=%q", content)
	}
	if content != "Hi there" {
		t.Fatalf("content = %q, want %q", content, "Hi there")
	}
	if !sawDone {
		t.Fatal("no Done chunk on implicit termination — response would be discarded")
	}
}

// Hermes session chat: a stream that ends without a run.completed/done/error
// terminal event (clean EOF / dropped gateway connection) must still Done.
func TestHermesSessionChatEmitsDoneOnImplicitTermination(t *testing.T) {
	body := `event: assistant.delta
data: {"delta":"streamed "}

event: assistant.delta
data: {"delta":"reply"}

`
	srv := sseServer(t, body)
	defer srv.Close()

	h := &HermesHarness{baseURL: srv.URL, httpClient: srv.Client()}
	ch, err := h.SessionChat(context.Background(), "sess-123", "hi")
	if err != nil {
		t.Fatalf("SessionChat error = %v", err)
	}

	content, sawDone, sawErr := drainChat(t, ch)
	if sawErr {
		t.Fatalf("unexpected error chunk; content=%q", content)
	}
	if content != "streamed reply" {
		t.Fatalf("content = %q, want %q", content, "streamed reply")
	}
	if !sawDone {
		t.Fatal("no Done chunk on session stream EOF without terminal event — reply would be lost and the conversation rolled back")
	}
}
