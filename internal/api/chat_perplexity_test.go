package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/harness"
)

// capturingHarness records the ChatOptions and messages handed to Chat() so a
// test can assert what the handler injected. It always streams a benign reply.
type capturingHarness struct {
	mu       sync.Mutex
	opts     harness.ChatOptions
	messages []harness.ChatMessage
}

func (c *capturingHarness) Name() string { return "capture" }
func (c *capturingHarness) Health(ctx context.Context) (*harness.HealthStatus, error) {
	return &harness.HealthStatus{Status: "online"}, nil
}
func (c *capturingHarness) ListModels(ctx context.Context) ([]harness.ModelInfo, error) {
	return nil, nil
}
func (c *capturingHarness) Commands() []harness.Command      { return nil }
func (c *capturingHarness) Init(config map[string]any) error { return nil }
func (c *capturingHarness) Close() error                     { return nil }

func (c *capturingHarness) Chat(ctx context.Context, messages []harness.ChatMessage, opts harness.ChatOptions) (<-chan harness.ChatChunk, error) {
	c.mu.Lock()
	c.opts = opts
	c.messages = messages
	c.mu.Unlock()
	ch := make(chan harness.ChatChunk, 2)
	go func() {
		defer close(ch)
		ch <- harness.ChatChunk{Content: "synthesized answer"}
		ch <- harness.ChatChunk{Done: true}
	}()
	return ch, nil
}

// TestChat_PerplexityModeInjectsSystemPrompt proves that selecting the
// "perplexity" mode flows a search-grounded, citation-style system prompt into
// the legacy chat path's ChatOptions.SystemPrompt (#137 acceptance: the mode is
// wired through to the agent and reuses existing chat infra).
func TestChat_PerplexityModeInjectsSystemPrompt(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	cap := &capturingHarness{}
	reg.Register("capture", func() harness.Harness { return cap })
	agentID := seedChatAgent(t, a, "capture")

	body := `{"message":"What is the capital of France?","mode":"perplexity"}`
	rec, convID := postChat(t, a, agentID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if convID == "" {
		t.Fatal("expected a conversation_id in the done event")
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if !strings.Contains(cap.opts.SystemPrompt, "PERPLEXITY MODE") {
		t.Errorf("expected the perplexity system prompt in ChatOptions, got %q", cap.opts.SystemPrompt)
	}
	if !strings.Contains(strings.ToLower(cap.opts.SystemPrompt), "web-search") {
		t.Errorf("perplexity prompt must instruct web search, got %q", cap.opts.SystemPrompt)
	}
}

// TestChat_DefaultModeDoesNotInject proves that a normal (mode-less) chat does
// NOT inject the perplexity instruction — the mode is strictly opt-in.
func TestChat_DefaultModeDoesNotInject(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	cap := &capturingHarness{}
	reg.Register("capture2", func() harness.Harness { return cap })
	agentID := seedChatAgent(t, a, "capture2")

	body := `{"message":"hello"}`
	rec, _ := postChat(t, a, agentID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if strings.Contains(cap.opts.SystemPrompt, "PERPLEXITY MODE") {
		t.Errorf("default chat must NOT inject the perplexity prompt, got %q", cap.opts.SystemPrompt)
	}
}

// mockHermesBackend is an httptest.Server backend that mimics the two Hermes
// HTTP endpoints used by the session chat path: POST /api/sessions (create) and
// POST /api/sessions/{id}/chat/stream (session chat). It records the last
// "message" it received so a test can assert mode-augmentation prepended it.
type mockHermesBackend struct {
	lastMessage string
	mu          sync.Mutex
}

func newMockHermesBackend(t *testing.T) (*mockHermesBackend, *httptest.Server) {
	t.Helper()
	mb := &mockHermesBackend{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/api/sessions"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"session": map[string]string{"id": "sess-test-1"}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/chat/stream"):
			var body struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(raw, &body)
			mb.mu.Lock()
			mb.lastMessage = body.Message
			mb.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			// Minimal valid SSE stream: a done event.
			_, _ = w.Write([]byte("event: done\ndata: {\"done\":true}\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return mb, srv
}

// TestChat_PerplexityModePrependsToSessionMessage proves that for a Hermes
// (session) agent, the perplexity instruction is prepended to the message sent
// to SessionChat — the session path has no system-prompt channel, so the mode
// rides along with the user turn. The original user text is still persisted
// verbatim to the database.
func TestChat_PerplexityModePrependsToSessionMessage(t *testing.T) {
	a, reg := newTestAPIForChat(t)
	mb, srv := newMockHermesBackend(t)
	reg.Register("hermes", func() harness.Harness { return newHermesHarnessForTest(srv.URL) })
	// The handler re-runs Init(config) with base_url = agent.base_url, so the
	// seeded base_url must point at the mock backend (not 'mock://test').
	agentID := seedChatAgentWithBaseURL(t, a, "hermes", srv.URL)

	body := `{"message":"What is the capital of France?","mode":"perplexity"}`
	rec, convID := postChat(t, a, agentID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if convID == "" {
		t.Fatal("expected a conversation_id from the session path")
	}

	mb.mu.Lock()
	got := mb.lastMessage
	mb.mu.Unlock()

	if !strings.HasPrefix(got, "[PERPLEXITY MODE]") {
		t.Errorf("expected session message to be prepended with the perplexity instruction, got %q", got)
	}
	if !strings.Contains(got, "What is the capital of France?") {
		t.Errorf("the original user message must still be present, got %q", got)
	}

	// The original (un-augmented) user message must be persisted verbatim.
	var persisted string
	err := a.pool.QueryRow(context.Background(),
		"SELECT content FROM messages WHERE conversation_id = $1 AND role = 'user'",
		convID,
	).Scan(&persisted)
	if err != nil {
		t.Fatalf("query persisted user message: %v", err)
	}
	if persisted != "What is the capital of France?" {
		t.Errorf("persisted user message must be the original text, not the augmented one; got %q", persisted)
	}
}

// newHermesHarnessForTest builds a real HermesHarness pointed at a test server
// without going through Init(), so a test can register it in the registry.
func newHermesHarnessForTest(baseURL string) harness.Harness {
	// Init with a config is how the handler configures it; replicate the effect.
	hh := harness.NewHermesHarness()
	_ = hh.Init(map[string]any{"base_url": baseURL})
	return hh
}

// seedChatAgentWithBaseURL inserts an agent with a custom base_url (needed when
// the harness hits a real network endpoint during the test, e.g. a mock Hermes
// backend) and returns its ID.
func seedChatAgentWithBaseURL(t *testing.T, a *API, harnessName, baseURL string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := a.pool.QueryRow(ctx,
		`INSERT INTO agents (name, display_name, harness, base_url, visible, status)
		 VALUES ($1, $2, $3, $4, true, 'online') RETURNING id`,
		"agent-"+harnessName+"-burl", "Agent "+harnessName, harnessName, baseURL,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed agent with base_url: %v", err)
	}
	t.Cleanup(func() { a.pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", id) })
	return id
}
