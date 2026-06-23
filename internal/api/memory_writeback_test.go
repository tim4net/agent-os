package api

// Integration test for the automatic memory writeback (issue #127).
//
// Proves the feedback loop end-to-end against a real Postgres:
//
//   1. A conversation is distilled into a structured memory note.
//   2. The note lands in memory_index (machine-queryable).
//   3. SearchMemory — the same query the chat handler's RAG path uses — finds
//      it, proving the loop closes (future turns will recall this knowledge).
//   4. The Obsidian .md file is written under the vault.
//   5. Calling Writeback twice on the same conversation produces exactly ONE
//      memory_index row (idempotent — no duplicate / infinite writeback).
//
// Gated on AOS_TEST_DATABASE_URL (with all migrations applied) like the rest of
// the DB-backed suite. The distiller is a deterministic fake so this test never
// calls a live LLM.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// setupWritebackTestDB returns a clean queries + pool, truncating the tables
// this suite touches.
func setupWritebackTestDB(t *testing.T) (*db.Queries, *pgxpool.Pool) {
	t.Helper()
	pool := getTestDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "TRUNCATE memory_index, messages, conversations CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db.New(pool), pool
}

// writebackFixture seeds one conversation with a user+assistant exchange and
// returns its id. An agent is also seeded because conversations FK to agents.
func writebackFixture(t *testing.T, queries *db.Queries, pool *pgxpool.Pool) (convID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	owner := testOwnerID()

	var agentID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO agents (name, display_name, harness, base_url, visible, status)
		 VALUES ('wb-test-agent', 'WB Test', 'generic', 'mock://test', true, 'online')
		 RETURNING id`).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", agentID) })

	conv, err := queries.CreateConversation(ctx, db.CreateConversationParams{
		OwnerID:  owner,
		AgentID:  agentID,
		Title:    pgtype.Text{String: "How to deploy Postgres", Valid: true},
		Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	for _, m := range []struct{ role, content string }{
		{"user", "How do I deploy a Postgres cluster on Kubernetes?"},
		{"assistant", "Use the CloudNativePG operator: install the CRD, create a Cluster resource with 3 replicas, and enable WAL archiving to S3 for backups."},
	} {
		if _, err := queries.CreateMessage(ctx, db.CreateMessageParams{
			OwnerID:        owner,
			ConversationID: conv.ID,
			Role:           m.role,
			Content:        m.content,
			Metadata:       []byte("{}"),
		}); err != nil {
			t.Fatalf("create %s message: %v", m.role, err)
		}
	}
	return conv.ID
}

// fakeDistillBody is the canned note a deterministic distiller returns.
const fakeDistillBody = `## Summary
Deploying Postgres on Kubernetes uses the CloudNativePG operator with 3 replicas and S3 WAL archiving.

## Key Facts
- CloudNativePG operator manages Postgres on Kubernetes.
- A Cluster CRD with 3 replicas gives HA.
- WAL archiving to S3 provides backups.`

// TestWriteback_PersistsAndClosesLoop is the headline test for issue #127:
// after Writeback runs, the distilled knowledge is in memory_index AND
// retrievable by the RAG search the chat handler uses.
func TestWriteback_PersistsAndClosesLoop(t *testing.T) {
	queries, pool := setupWritebackTestDB(t)
	owner := testOwnerID()
	convID := writebackFixture(t, queries, pool)

	vault := t.TempDir()
	distiller := func(_ context.Context, _ string) (string, error) {
		return fakeDistillBody, nil
	}
	mw := service.NewMemoryWriteback(queries, vault, distiller)

	if err := mw.Writeback(context.Background(), convID, owner); err != nil {
		t.Fatalf("Writeback: %v", err)
	}

	// AC: memory_index has the row, attributed to the conversation owner.
	ctx := context.Background()
	got, err := queries.GetMemoryByPath(ctx, db.GetMemoryByPathParams{
		FilePath: "auto/conversations/" + convID.String() + ".md",
		OwnerID:  owner,
	})
	if err != nil {
		t.Fatalf("GetMemoryByPath: %v (row not persisted?)", err)
	}
	if !got.Title.Valid || !strings.Contains(got.Title.String, "Postgres") {
		t.Fatalf("unexpected title: %+v", got.Title)
	}
	if !strings.Contains(got.Content.String, "CloudNativePG") {
		t.Fatalf("memory content missing distilled fact: %q", got.Content.String)
	}

	// AC: the loop closes — SearchMemory (RAG) finds the note.
	hits, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		OwnerID:            owner,
		WebsearchToTsquery: "CloudNativePG Postgres Kubernetes",
		Limit:              5,
		ProjectID:          pgtype.UUID{},
	})
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("RAG search returned no hits — the loop did NOT close")
	}

	// AC: Obsidian .md file exists (human-readable target).
	mdPath := filepath.Join(vault, "auto", "conversations", convID.String()+".md")
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("Obsidian note not written: %v", err)
	}
	body, _ := os.ReadFile(mdPath)
	if !strings.Contains(string(body), "CloudNativePG") {
		t.Fatalf("Obsidian note missing distilled content: %s", body)
	}
}

// TestWriteback_Idempotent proves AC: "No duplicate/infinite writeback loops;
// idempotent on replay". Calling Writeback twice yields exactly one row.
func TestWriteback_Idempotent(t *testing.T) {
	queries, pool := setupWritebackTestDB(t)
	owner := testOwnerID()
	convID := writebackFixture(t, queries, pool)

	mw := service.NewMemoryWriteback(queries, t.TempDir(),
		func(_ context.Context, _ string) (string, error) { return fakeDistillBody, nil })

	if err := mw.Writeback(context.Background(), convID, owner); err != nil {
		t.Fatalf("first Writeback: %v", err)
	}
	// Second call — a replay / re-trigger of the same conversation.
	if err := mw.Writeback(context.Background(), convID, owner); err != nil {
		t.Fatalf("second Writeback: %v", err)
	}

	// Count rows for this owner: must be exactly one (the single conversation).
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM memory_index WHERE owner_id = $1", owner).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 memory row after two writebacks, got %d (not idempotent)", n)
	}
}

// TestWriteback_SkipsIncompleteConversation proves a conversation with no
// assistant reply produces no memory (nothing durable to distill).
func TestWriteback_SkipsIncompleteConversation(t *testing.T) {
	queries, pool := setupWritebackTestDB(t)
	owner := testOwnerID()
	ctx := context.Background()

	var agentID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO agents (name, display_name, harness, base_url, visible, status)
		 VALUES ('wb-incomplete', 'WB Incomplete', 'generic', 'mock://test', true, 'online')
		 RETURNING id`).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", agentID) })

	conv, err := queries.CreateConversation(ctx, db.CreateConversationParams{
		OwnerID: owner, AgentID: agentID, Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// Only a user message — no assistant reply.
	if _, err := queries.CreateMessage(ctx, db.CreateMessageParams{
		OwnerID: owner, ConversationID: conv.ID, Role: "user", Content: "hello?", Metadata: []byte("{}"),
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	called := false
	mw := service.NewMemoryWriteback(queries, t.TempDir(),
		func(_ context.Context, _ string) (string, error) { called = true; return "x", nil })

	if err := mw.Writeback(ctx, conv.ID, owner); err != nil {
		t.Fatalf("Writeback on incomplete conversation errored: %v", err)
	}
	if called {
		t.Fatal("distiller should not be called for an incomplete conversation")
	}
	var n int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM memory_index WHERE owner_id = $1", owner).Scan(&n)
	if n != 0 {
		t.Fatalf("expected 0 memory rows for incomplete conversation, got %d", n)
	}
}

// TestWriteback_BusPublishesEvent proves the writeback emits a typed event on
// the event bus so the activity feed / SSE stream surfaces "memory learned".
func TestWriteback_BusPublishesEvent(t *testing.T) {
	queries, pool := setupWritebackTestDB(t)
	owner := testOwnerID()
	convID := writebackFixture(t, queries, pool)

	bus := service.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	mw := service.NewMemoryWriteback(queries, t.TempDir(),
		func(_ context.Context, _ string) (string, error) { return fakeDistillBody, nil }).WithEventBus(bus)

	if err := mw.Writeback(context.Background(), convID, owner); err != nil {
		t.Fatalf("Writeback: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Type != "memory_writeback" {
			t.Fatalf("expected event type memory_writeback, got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for memory_writeback event")
	}
}
