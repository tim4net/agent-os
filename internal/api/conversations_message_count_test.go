package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// conversationListItem mirrors one row of the GET /api/conversations JSON
// response. It captures only the fields this test asserts on, including the
// message_count that this change adds (issue #140).
type conversationListItem struct {
	ID           string `json:"id"`
	MessageCount int    `json:"message_count"`
}

// TestListConversations_MessageCount proves GET /api/conversations returns the
// actual message_count for each conversation (issue #140): a conversation that
// has messages reports count > 0, and a conversation with no messages reports
// 0. Before the fix the count was always 0 because the list query had no
// message-count join.
func TestListConversations_MessageCount(t *testing.T) {
	a, _ := newTestAPIForChat(t)
	ctx := context.Background()
	owner := testOwnerID()

	// Seed a visible agent owned by the test owner. Name must be unique.
	var agentID pgtype.UUID
	if err := a.pool.QueryRow(ctx,
		`INSERT INTO agents (name, display_name, harness, base_url, visible, status, owner_id)
		 VALUES ($1, $2, 'mock', 'mock://test', true, 'online', $3) RETURNING id`,
		"mc-agent-"+uuid.NewString(), "MC Agent", owner,
	).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() { a.pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", agentID) })

	// Conversation WITH messages.
	var convWithMsgs pgtype.UUID
	if err := a.pool.QueryRow(ctx,
		`INSERT INTO conversations (owner_id, agent_id, title, metadata)
		 VALUES ($1, $2, 'has-messages', '{}') RETURNING id`,
		owner, agentID,
	).Scan(&convWithMsgs); err != nil {
		t.Fatalf("seed conversation with messages: %v", err)
	}
	// Three messages (alternating roles) — count must be 3.
	for i := 0; i < 3; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := a.pool.Exec(ctx,
			`INSERT INTO messages (owner_id, conversation_id, role, content, metadata)
			 VALUES ($1, $2, $3, 'msg', '{}')`,
			owner, convWithMsgs, role,
		); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}

	// Empty conversation (zero messages).
	var convEmpty pgtype.UUID
	if err := a.pool.QueryRow(ctx,
		`INSERT INTO conversations (owner_id, agent_id, title, metadata)
		 VALUES ($1, $2, 'empty', '{}') RETURNING id`,
		owner, agentID,
	).Scan(&convEmpty); err != nil {
		t.Fatalf("seed empty conversation: %v", err)
	}

	// Drive the handler through a real chi route with the test owner identity.
	r := chi.NewRouter()
	r.Get("/api/conversations", a.ListConversations)
	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req = req.WithContext(withTestOwner(req.Context()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var items []conversationListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}

	counts := make(map[string]int, len(items))
	for _, it := range items {
		counts[it.ID] = it.MessageCount
	}

	if got, want := counts[convWithMsgs.String()], 3; got != want {
		t.Fatalf("conversation %s message_count = %d, want %d", convWithMsgs.String(), got, want)
	}
	if got, want := counts[convEmpty.String()], 0; got != want {
		t.Fatalf("empty conversation %s message_count = %d, want %d", convEmpty.String(), got, want)
	}
}
