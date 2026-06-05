package tuiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamChat(t *testing.T) {
	sseData := `event: info
data: {"user_message_id":"038e4642-1234"}

event: chunk
data: {"content":"TUI_CHAT_PATH_OK"}

event: done
data: {"assistant_message_id":"3a6a","context_sources":null,"conversation_id":"6db0e0ae-1234","done":true,"user_message_id":"038e4642-1234"}

`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseData))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	eventsChan, err := client.StreamChat(context.Background(), "test-agent-id", ChatRequest{Message: "hello"})
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	var events []ChatEvent
	for evt := range eventsChan {
		events = append(events, evt)
	}

	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}

	if events[0].Type != "info" || events[0].UserMessageID != "038e4642-1234" {
		t.Errorf("Unexpected info event: %+v", events[0])
	}

	if events[1].Type != "chunk" || events[1].Content != "TUI_CHAT_PATH_OK" {
		t.Errorf("Unexpected chunk event: %+v", events[1])
	}

	if events[2].Type != "done" || !events[2].Done || events[2].ConversationID != "6db0e0ae-1234" {
		t.Errorf("Unexpected done event: %+v", events[2])
	}
}
