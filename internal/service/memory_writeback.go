package service

// Automatic memory writeback — the "feedback loop" (issue #127).
//
// After a chat turn completes, the system distills the conversation into a
// durable memory note and persists it to BOTH:
//
//   1. the Obsidian vault (human-readable Markdown, under auto/conversations/),
//      so a human browsing the vault sees a clean, growing knowledge base; and
//   2. the memory_index table (machine-queryable), so the RAG search in
//      chat.go (searchMemoryForContext) can surface it on future turns —
//      closing the loop: the agent now remembers what it learned.
//
// Idempotency / no infinite loops:
//   - The note path is deterministic per conversation (auto/conversations/<uuid>.md),
//     so re-running Writeback on the same conversation upserts the SAME row.
//     UpsertMemory's ON CONFLICT(file_path) DO UPDATE is the dedupe guarantee.
//   - Writeback only READS messages and WRITES a file + memory_index row. It
//     never creates a new message or triggers a new chat turn, so there is no
//     feedback path that could drive a writeback → chat → writeback cycle.
//
// Testability:
//   - The LLM distillation step is an injected DistillerFunc, so the core logic
//     is unit-tested without any LLM or database (see memory_writeback_test.go).
//   - DB-backed coverage lives in the api package integration tests, gated on
//     AOS_TEST_DSN like the rest of the memory suite.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// autoMemoryDir is the vault-relative directory for auto-generated memory notes.
// The trailing conversation UUID makes each note's path stable across replays.
const autoMemoryDir = "auto/conversations"

// DistillerFunc converts a conversation transcript into a structured Markdown
// memory note. It is injected so the writeback logic can be unit-tested without
// an LLM (tests pass a deterministic fake). A real implementation calls the
// LiteLLM/OpenRouter proxy (see api.MemoryDistiller).
type DistillerFunc func(ctx context.Context, transcript string) (markdown string, err error)

// HeuristicDistill is a dependency-free fallback distiller used when no LLM is
// configured or an LLM call fails. It still produces a useful note — a short
// summary plus the most recent exchange — so the feedback loop degrades
// gracefully instead of silently dropping memory.
func HeuristicDistill(_ context.Context, transcript string) (string, error) {
	return buildHeuristicNote(transcript), nil
}

// memoryNotePath returns the deterministic vault-relative path for a
// conversation's auto-generated memory note. The conversation UUID makes the
// path stable across replays, which is what makes Writeback idempotent (the
// same path always maps to the same memory_index row via file_path).
// Returns "" for an invalid conversation id.
func memoryNotePath(convID pgtype.UUID) string {
	if !convID.Valid {
		return ""
	}
	return filepath.ToSlash(filepath.Join(autoMemoryDir, convID.String()+".md"))
}

// MemoryWriteback distills a conversation's messages into a durable memory
// note and persists it to the Obsidian vault and the memory_index table.
type MemoryWriteback struct {
	queries      *db.Queries
	obsidianPath string
	distill      DistillerFunc
	bus          *EventBus // optional; nil-safe
}

// NewMemoryWriteback creates a writeback that writes notes under obsidianPath
// and indexes them via queries. distill must be non-nil; pass HeuristicDistill
// when no LLM is available.
func NewMemoryWriteback(queries *db.Queries, obsidianPath string, distill DistillerFunc) *MemoryWriteback {
	if distill == nil {
		distill = HeuristicDistill
	}
	return &MemoryWriteback{
		queries:      queries,
		obsidianPath: obsidianPath,
		distill:      distill,
	}
}

// WithEventBus attaches an event bus so writeback completion is observable on
// the activity feed / SSE event stream. Returns mw for chaining.
func (mw *MemoryWriteback) WithEventBus(bus *EventBus) *MemoryWriteback {
	mw.bus = bus
	return mw
}

// Writeback distills one conversation into memory and persists it. It is safe
// to call from a background goroutine after a chat turn. Returns nil (no-op)
// when the conversation has no complete user→assistant exchange to distill.
//
// All persistence targets are updated: the Obsidian .md file is written first
// (human-readable), then the memory_index row is upserted (machine-queryable).
func (mw *MemoryWriteback) Writeback(ctx context.Context, convID, ownerID pgtype.UUID) error {
	notePath := memoryNotePath(convID)
	if notePath == "" {
		return fmt.Errorf("writeback: invalid conversation id")
	}

	conv, err := mw.queries.GetConversation(ctx, db.GetConversationParams{ID: convID, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("get conversation: %w", err)
	}

	msgs, err := mw.queries.ListMessages(ctx, db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	// Nothing durable to remember from a half-conversation.
	if !hasExchange(msgs) {
		return nil
	}

	transcript := buildTranscript(msgs)
	markdown, err := mw.distill(ctx, transcript)
	if err != nil {
		// Never let a distiller failure silently drop memory — fall back so
		// the loop stays closed even when the LLM is down.
		slog.Warn("memory-writeback: distiller failed, using heuristic fallback",
			"conversation_id", convID.String(), "error", err)
		markdown = buildHeuristicNote(transcript)
	}

	title := conversationTitle(conv)
	note := renderNote(title, convID, markdown)

	// 1. Obsidian — human-readable Markdown.
	if err := mw.writeVaultFile(notePath, note); err != nil {
		return fmt.Errorf("write vault file: %w", err)
	}

	// 2. memory_index — machine-queryable for RAG. Idempotent via the
	//    ON CONFLICT(file_path) DO UPDATE clause in UpsertMemory.
	plain := stripMarkdown(note)
	if _, err := mw.queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		OwnerID:  ownerID,
		FilePath: notePath,
		Title:    pgtype.Text{String: title, Valid: title != ""},
		Content:  pgtype.Text{String: plain, Valid: plain != ""},
		Tags:     []string{"auto", "conversation"},
	}); err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}

	if mw.bus != nil {
		mw.bus.PublishTyped("memory_writeback", map[string]any{
			"conversation_id": convID.String(),
			"file_path":       notePath,
			"title":           title,
		})
	}
	slog.Info("memory-writeback: persisted conversation memory",
		"conversation_id", convID.String(), "file_path", notePath)
	return nil
}

// writeVaultFile writes the note body to obsidianPath/relPath, creating parent
// directories as needed. A missing obsidianPath is a normal first-run state;
// rather than error we log and skip so chat turns never fail because the vault
// isn't mounted yet (the memory_index upsert still happens).
func (mw *MemoryWriteback) writeVaultFile(relPath, content string) error {
	if mw.obsidianPath == "" {
		slog.Debug("memory-writeback: obsidian path empty, skipping vault write", "file_path", relPath)
		return nil
	}
	full := filepath.Join(mw.obsidianPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// hasExchange reports whether msgs contain at least one user AND one assistant
// message — the minimum signal worth distilling into memory.
func hasExchange(msgs []db.Message) bool {
	var sawUser, sawAssistant bool
	for _, m := range msgs {
		switch m.Role {
		case "user":
			sawUser = true
		case "assistant":
			sawAssistant = true
		}
		if sawUser && sawAssistant {
			return true
		}
	}
	return false
}

// buildTranscript renders messages into a compact "Role: content" transcript for
// the distiller. Long messages are capped so the LLM context stays bounded.
func buildTranscript(msgs []db.Message) string {
	const perMsgCap = 1200
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.Title(m.Role) //nolint:staticcheck // simple capitalization is fine here
		if role == "" {
			role = "Unknown"
		}
		content := m.Content
		if len(content) > perMsgCap {
			content = content[:perMsgCap] + "\n…(truncated)"
		}
		sb.WriteString(role + ": " + content + "\n\n")
	}
	return strings.TrimSpace(sb.String())
}

// buildHeuristicNote produces a no-LLM Markdown note: a short summary line
// plus the last user/assistant exchange. Used as a fallback so the loop stays
// closed when no distiller is available.
func buildHeuristicNote(transcript string) string {
	var sb strings.Builder
	sb.WriteString("## Summary\n\n")
	sb.WriteString("Auto-captured conversation (no LLM distillation available).\n\n")
	sb.WriteString("## Transcript (excerpt)\n\n")
	excerpt := transcript
	const cap = 2000
	if len(excerpt) > cap {
		excerpt = excerpt[:cap] + "\n…(truncated)"
	}
	sb.WriteString(excerpt)
	sb.WriteString("\n")
	return sb.String()
}

// conversationTitle returns a human title for the note, preferring the
// conversation's title and falling back to a generic label.
func conversationTitle(conv db.Conversation) string {
	if conv.Title.Valid && strings.TrimSpace(conv.Title.String) != "" {
		return strings.TrimSpace(conv.Title.String)
	}
	return "Conversation"
}

// renderNote wraps the distilled body in a self-describing Markdown document
// with YAML frontmatter. The frontmatter carries metadata the Obsidian indexer
// (and a human) can rely on, and the H1 gives extractTitle a clean title.
func renderNote(title string, convID pgtype.UUID, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "title: %q\n", title)
	fmt.Fprintf(&sb, "source: conversation\n")
	fmt.Fprintf(&sb, "conversation_id: %s\n", convID.String())
	fmt.Fprintf(&sb, "distilled_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "tags: [auto, conversation]\n")
	sb.WriteString("---\n\n")
	sb.WriteString("# " + title + "\n\n")
	body = strings.TrimSpace(body)
	if body == "" {
		body = "(no distillable content)"
	}
	sb.WriteString(body)
	sb.WriteString("\n")
	return sb.String()
}
