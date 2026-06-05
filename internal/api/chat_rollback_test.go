package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// mockHarness is a test harness whose Chat() behaviour is scripted: it either
// streams a successful reply (content + done) or emits an error chunk, with no
// network dependency. Used to exercise the chat handler's orphan-conversation
// rollback deterministically.
type mockHarness struct {
	reply   string // streamed as a single content chunk when non-empty
	failErr error  // when non-nil, Chat streams an error chunk instead
}

func (m *mockHarness) Name() string { return "mock" }
func (m *mockHarness) Health(ctx context.Context) (*harness.HealthStatus, error) {
	return &harness.HealthStatus{Status: "online"}, nil
}
func (m *mockHarness) ListModels(ctx context.Context) ([]harness.ModelInfo, error) {
	return nil, nil
}
func (m *mockHarness) Commands() []harness.Command      { return nil }
func (m *mockHarness) Init(config map[string]any) error { return nil }
func (m *mockHarness) Close() error                     { return nil }

func (m *mockHarness) Chat(ctx context.Context, messages []harness.ChatMessage, opts harness.ChatOptions) (<-chan harness.ChatChunk, error) {
	ch := make(chan harness.ChatChunk, 2)
	go func() {
		defer close(ch)
		if m.failErr != nil {
			ch <- harness.ChatChunk{Error: m.failErr}
			return
		}
		ch <- harness.ChatChunk{Content: m.reply}
		ch <- harness.ChatChunk{Done: true}
	}()
	return ch, nil
}

// newTestAPIForChat builds an API wired to the real test DB plus an isolated
// harness registry, so tests can register scripted mock harnesses without
// touching the global DefaultRegistry.
func newTestAPIForChat(t *testing.T) (*API, *harness.Registry) {
	t.Helper()
	pool := getTestDB(t)
	t.Cleanup(func() { pool.Close() })
	reg := harness.NewRegistry()
	a := &API{
		queries:  db.New(pool),
		pool:     pool,
		registry: reg,
	}
	return a, reg
}

// seedChatAgent inserts an agent with the given harness name and returns its ID.
func seedChatAgent(t *testing.T, a *API, harnessName string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := a.pool.QueryRow(ctx,
		`INSERT INTO agents (name, display_name, harness, base_url, visible, status)
		 VALUES ($1, $2, $3, 'mock://test', true, 'online') RETURNING id`,
		"mock-"+harnessName, "Mock "+harnessName, harnessName,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() { a.pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", id) })
	return id
}

// postChat drives ChatWithAgent through a real chi route and returns the
// response recorder plus the conversation_id parsed from the done event (empty
// if none was sent).
func postChat(t *testing.T, a *API, agentID pgtype.UUID, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/api/agents/{id}/chat", a.ChatWithAgent)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID.String()+"/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Parse conversation_id out of the SSE "done" event, if present.
	var convID string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &payload) == nil {
			if v, ok := payload["conversation_id"].(string); ok {
				convID = v
			}
		}
	}
	return rec, convID
}

func countConversations(t *testing.T, a *API, agentID pgtype.UUID) int {
	t.Helper()
	var n int
	err := a.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM conversations WHERE agent_id = $1", agentID).Scan(&n)
	if err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	return n
}

func countMessages(t *testing.T, a *API, convID string) int {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(convID); err != nil {
		t.Fatalf("scan convID: %v", err)
	}
	var n int
	err := a.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM messages WHERE conversation_id = $1", id).Scan(&n)
	if err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

// TestChat_SuccessPersistsConversation proves a successful turn creates a
// conversation and persists both the user and assistant messages.
func TestChat_SuccessPersistsConversation(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	reg.Register("mock", func() harness.Harness { return &mockHarness{reply: "hello back"} })
	agentID := seedChatAgent(t, a, "mock")

	before := countConversations(t, a, agentID)
	rec, convID := postChat(t, a, agentID, `{"message":"hi"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if convID == "" {
		t.Fatal("expected a conversation_id in the done event, got none")
	}
	if got := countConversations(t, a, agentID); got != before+1 {
		t.Fatalf("expected conversation count %d, got %d", before+1, got)
	}
	// user + assistant
	if got := countMessages(t, a, convID); got != 2 {
		t.Fatalf("expected 2 persisted messages (user+assistant), got %d", got)
	}
}

// TestChat_FailedTurnRollsBackNewConversation proves an in-stream error on a
// brand-new conversation rolls the conversation back — no ghost is left behind.
func TestChat_FailedTurnRollsBackNewConversation(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	reg.Register("mockfail", func() harness.Harness {
		return &mockHarness{failErr: context.DeadlineExceeded}
	})
	agentID := seedChatAgent(t, a, "mockfail")

	before := countConversations(t, a, agentID)
	rec, convID := postChat(t, a, agentID, `{"message":"this will fail"}`)

	// The stream opens (200) then emits an error event; no done/conversation_id.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (stream opened), got %d", rec.Code)
	}
	if convID != "" {
		t.Fatalf("expected NO conversation_id on failed turn, got %q", convID)
	}
	if !strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("expected an error event in the stream, body=%q", rec.Body.String())
	}
	if got := countConversations(t, a, agentID); got != before {
		t.Fatalf("ghost conversation left behind: count %d -> %d", before, got)
	}
}

// TestChat_FailedTurnPreservesExistingConversation proves a failure while
// continuing an EXISTING conversation never deletes it — only freshly-created
// conversations are rolled back.
func TestChat_FailedTurnPreservesExistingConversation(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	// First a successful turn to establish a real conversation...
	reg.Register("mockmix", func() harness.Harness { return &mockHarness{reply: "ok"} })
	agentID := seedChatAgent(t, a, "mockmix")
	_, convID := postChat(t, a, agentID, `{"message":"first"}`)
	if convID == "" {
		t.Fatal("setup: expected a conversation_id from the successful turn")
	}
	msgsBefore := countMessages(t, a, convID)
	convsBefore := countConversations(t, a, agentID)

	// ...now swap the harness factory to fail, and send a follow-up INTO the
	// existing conversation.
	reg.Register("mockmix", func() harness.Harness {
		return &mockHarness{failErr: context.DeadlineExceeded}
	})
	rec, _ := postChat(t, a, agentID, `{"message":"failing followup","conversation_id":"`+convID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The existing conversation must still exist...
	if got := countConversations(t, a, agentID); got != convsBefore {
		t.Fatalf("existing conversation was wrongly deleted: count %d -> %d", convsBefore, got)
	}
	// ...and it must still hold its prior messages (the failed user msg may or
	// may not be appended, but the original exchange must survive).
	if got := countMessages(t, a, convID); got < msgsBefore {
		t.Fatalf("existing conversation lost messages: %d -> %d", msgsBefore, got)
	}
}
