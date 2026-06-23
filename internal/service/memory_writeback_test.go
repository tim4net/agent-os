package service

// Unit tests for the automatic memory writeback (issue #127).
//
// These are hermetic — no LLM, no database. The DistillerFunc seam lets us
// assert the distilled note is rendered, the vault file is written, and that
// the logic is idempotent / loop-free purely from in-memory state. DB-backed
// coverage (the UpsertMemory half) lives in the api integration suite.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// fakeDistiller returns a canned note body and records the transcript it saw.
func fakeDistiller(body string) (DistillerFunc, *string) {
	var seen string
	return func(_ context.Context, transcript string) (string, error) {
		seen = transcript
		return body, nil
	}, &seen
}

func mustScanUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid %q: %v", s, err)
	}
	return u
}

// validText builds a valid pgtype.Text.
func validText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func msg(role, content string) db.Message {
	return db.Message{Role: role, Content: content}
}

// TestHasExchange proves the gate: only a conversation with BOTH a user and an
// assistant message is worth distilling. A lone user message must be skipped.
func TestHasExchange(t *testing.T) {
	cases := []struct {
		name string
		msgs []db.Message
		want bool
	}{
		{"empty", nil, false},
		{"only user", []db.Message{msg("user", "hi")}, false},
		{"only assistant", []db.Message{msg("assistant", "hello")}, false},
		{"user+assistant", []db.Message{msg("user", "hi"), msg("assistant", "hello")}, true},
		{"system noise only", []db.Message{msg("system", "s"), msg("user", "u")}, false},
		{"full round trip", []db.Message{msg("user", "q"), msg("assistant", "a"), msg("user", "q2"), msg("assistant", "a2")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasExchange(c.msgs); got != c.want {
				t.Fatalf("hasExchange=%v want %v", got, c.want)
			}
		})
	}
}

// TestBuildTranscript proves messages render as "Role: content" pairs, in
// order, with long content truncated.
func TestBuildTranscript(t *testing.T) {
	msgs := []db.Message{
		msg("user", "What is 2+2?"),
		msg("assistant", "4"),
	}
	got := buildTranscript(msgs)
	if !strings.Contains(got, "User: What is 2+2?") {
		t.Fatalf("expected user line, got %q", got)
	}
	if !strings.Contains(got, "Assistant: 4") {
		t.Fatalf("expected assistant line, got %q", got)
	}

	// Truncation: a >1200 char message is capped.
	long := strings.Repeat("x", 3000)
	out := buildTranscript([]db.Message{msg("assistant", long)})
	if strings.Contains(out, strings.Repeat("x", 2000)) {
		t.Fatalf("long message was not truncated (len=%d)", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation marker, got tail: %q", out[len(out)-60:])
	}
}

// TestMemoryNotePath proves the path is deterministic and UUID-derived — the
// foundation of idempotency (same conversation → same file_path → same row).
func TestMemoryNotePath(t *testing.T) {
	id := mustScanUUID(t, "11111111-1111-1111-1111-111111111111")
	got := memoryNotePath(id)
	want := "auto/conversations/11111111-1111-1111-1111-111111111111.md"
	if got != want {
		t.Fatalf("memoryNotePath=%q want %q", got, want)
	}
	// Invalid UUID → empty path (caller treats as error).
	if p := memoryNotePath(pgtype.UUID{}); p != "" {
		t.Fatalf("invalid UUID should yield empty path, got %q", p)
	}
	// Deterministic: same input → same output.
	if memoryNotePath(id) != memoryNotePath(id) {
		t.Fatal("memoryNotePath not deterministic")
	}
}

// TestRenderNote proves the note has frontmatter metadata, an H1 title, and the
// distilled body — so the vault note is self-describing and the indexer's
// extractTitle picks up the H1.
func TestRenderNote(t *testing.T) {
	id := mustScanUUID(t, "22222222-2222-2222-2222-222222222222")
	note := renderNote("Fix voice bug", id, "## Summary\n\nWe fixed it.\n")
	if !strings.HasPrefix(note, "---\n") {
		t.Fatalf("note should start with frontmatter, got %q", note[:20])
	}
	if !strings.Contains(note, "title: \"Fix voice bug\"") {
		t.Fatalf("frontmatter missing title: %s", note)
	}
	if !strings.Contains(note, "conversation_id: 22222222-2222-2222-2222-222222222222") {
		t.Fatalf("frontmatter missing conversation_id: %s", note)
	}
	if !strings.Contains(note, "# Fix voice bug") {
		t.Fatalf("note missing H1 title: %s", note)
	}
	if !strings.Contains(note, "We fixed it.") {
		t.Fatalf("note missing distilled body: %s", note)
	}
}

// TestHeuristicDistill proves the fallback note has the expected sections even
// with no LLM, so the loop degrades gracefully.
func TestHeuristicDistill(t *testing.T) {
	out, err := HeuristicDistill(context.Background(), "User: hi\n\nAssistant: hey")
	if err != nil {
		t.Fatalf("heuristic distill errored: %v", err)
	}
	if !strings.Contains(out, "## Summary") {
		t.Fatalf("heuristic note missing Summary section: %s", out)
	}
	if !strings.Contains(out, "User: hi") {
		t.Fatalf("heuristic note missing transcript excerpt: %s", out)
	}
}

// TestWriteVaultFile proves the note is written under obsidianPath/relPath
// with parent dirs created, and that an empty obsidianPath is a no-op (not an
// error) — a first-run / unmounted-vault state.
func TestWriteVaultFile(t *testing.T) {
	t.Run("writes nested file", func(t *testing.T) {
		vault := t.TempDir()
		mw := &MemoryWriteback{obsidianPath: vault}
		rel := "auto/conversations/abc.md"
		if err := mw.writeVaultFile(rel, "# Hi\nbody"); err != nil {
			t.Fatalf("writeVaultFile: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(vault, rel))
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != "# Hi\nbody" {
			t.Fatalf("content mismatch: %q", got)
		}
	})
	t.Run("empty path is no-op not error", func(t *testing.T) {
		mw := &MemoryWriteback{obsidianPath: ""}
		if err := mw.writeVaultFile("auto/x.md", "hi"); err != nil {
			t.Fatalf("empty obsidianPath should be no-op, got %v", err)
		}
	})
}

// TestConversationTitle proves the title falls back to a generic label when the
// conversation has no title.
func TestConversationTitle(t *testing.T) {
	if got := conversationTitle(db.Conversation{Title: validText("  My Chat  ")}); got != "My Chat" {
		t.Fatalf("expected trimmed title, got %q", got)
	}
	if got := conversationTitle(db.Conversation{Title: pgtype.Text{}}); got != "Conversation" {
		t.Fatalf("expected fallback title, got %q", got)
	}
}

// TestNewMemoryWriteback_DefaultsHeuristicDistiller proves that a nil distiller
// is replaced by the heuristic fallback, so the constructor can never produce a
// writeback that nil-derefs on the distill call.
func TestNewMemoryWriteback_DefaultsHeuristicDistiller(t *testing.T) {
	mw := NewMemoryWriteback(nil, "", nil)
	// Invoke the distiller to prove it is non-nil and works.
	out, err := mw.distill(context.Background(), "User: q\n\nAssistant: a")
	if err != nil || !strings.Contains(out, "Summary") {
		t.Fatalf("nil distiller should fall back to heuristic, got out=%q err=%v", out, err)
	}
}

// TestBuildTranscript_CapBound is a focused check that the per-message cap is
// exactly perMsgCap (1200) chars of content before the truncation marker.
func TestBuildTranscript_CapBound(t *testing.T) {
	// 1199 → no truncation; 1201 → truncation.
	noTrunc := buildTranscript([]db.Message{msg("user", strings.Repeat("y", 1199))})
	if strings.Contains(noTrunc, "truncated") {
		t.Fatal("1199-char message should not be truncated")
	}
	trunc := buildTranscript([]db.Message{msg("user", strings.Repeat("z", 1201))})
	if !strings.Contains(trunc, "truncated") {
		t.Fatal("1201-char message should be truncated")
	}
}
