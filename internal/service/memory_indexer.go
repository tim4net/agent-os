package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// MemoryIndexer walks the Obsidian vault periodically and indexes .md files.
type MemoryIndexer struct {
	queries      *db.Queries
	bus          *EventBus
	obsidianPath string
	stopCh       chan struct{}
}

// NewMemoryIndexer creates a new MemoryIndexer.
func NewMemoryIndexer(queries *db.Queries, bus *EventBus, obsidianPath string) *MemoryIndexer {
	return &MemoryIndexer{
		queries:      queries,
		bus:          bus,
		obsidianPath: obsidianPath,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the periodic indexing loop.
func (mi *MemoryIndexer) Start(ctx context.Context) {
	go func() {
		// Initial index
		mi.index(ctx)

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-mi.stopCh:
				return
			case <-ticker.C:
				mi.index(ctx)
			}
		}
	}()

	slog.Info("memory-indexer: started", "path", mi.obsidianPath, "interval", "5m")
}

// Stop signals the indexer to stop.
func (mi *MemoryIndexer) Stop() {
	close(mi.stopCh)
}

func (mi *MemoryIndexer) index(ctx context.Context) {
	count := 0

	err := filepath.Walk(mi.obsidianPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip .obsidian directories
		if info.IsDir() && info.Name() == ".obsidian" {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		// Only index .md files
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}

		// Read file content
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Error("memory-indexer: failed to read file", "path", path, "error", err)
			return nil
		}

		content := string(data)
		relPath, err := filepath.Rel(mi.obsidianPath, path)
		if err != nil {
			relPath = path
		}

		// Extract title
		title := extractTitle(content, filepath.Base(path))

		// Strip markdown for plain text content
		plainText := stripMarkdown(content)

		// Upsert into memory_index
		_, err = mi.queries.UpsertMemory(ctx, db.UpsertMemoryParams{
			FilePath: relPath,
			Title:    pgtype.Text{String: title, Valid: title != ""},
			Content:  pgtype.Text{String: plainText, Valid: plainText != ""},
			Tags:     []string{},
		})
		if err != nil {
			slog.Error("memory-indexer: failed to upsert", "path", relPath, "error", err)
			return nil
		}

		count++

		return nil
	})

	if err != nil {
		slog.Error("memory-indexer: walk error", "error", err)
	}

	// Publish a single summary event at the end of the index cycle
	if mi.bus != nil && count > 0 {
		mi.bus.PublishTyped("memory_indexed", map[string]any{
			"total": count,
		})
	}

	slog.Info("memory-indexer: indexed files", "count", count)
}

// extractTitle extracts the title from frontmatter or first heading.
func extractTitle(content string, fallback string) string {
	// Try YAML frontmatter title
	fmRe := regexp.MustCompile(`(?s)^---\n.*?title:\s*(.+?)\n.*?---`)
	if matches := fmRe.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Try first # heading
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}

	return fallback
}

// stripMarkdown removes common markdown formatting for plain text indexing.
func stripMarkdown(content string) string {
	// Remove YAML frontmatter
	fm := regexp.MustCompile(`(?s)^---\n.*?\n---\n*`)
	content = fm.ReplaceAllString(content, "")

	// Remove code blocks
	codeBlock := regexp.MustCompile("(?s)```.*?```")
	content = codeBlock.ReplaceAllString(content, "")

	// Remove inline code
	content = regexp.MustCompile("`[^`]+`").ReplaceAllString(content, "")

	// Remove images ![alt](url)
	content = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`).ReplaceAllString(content, "")

	// Remove links [text](url) -> text
	content = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(content, "$1")

	// Remove bold/italic markers
	content = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`).ReplaceAllString(content, "$1")
	content = regexp.MustCompile(`_{1,3}([^_]+)_{1,3}`).ReplaceAllString(content, "$1")

	// Remove headings markers
	content = regexp.MustCompile(`^#{1,6}\s+`).ReplaceAllString(content, "")

	// Remove blockquotes
	content = regexp.MustCompile(`^>\s+`).ReplaceAllString(content, "")

	// Remove horizontal rules
	content = regexp.MustCompile(`^---+$`).ReplaceAllString(content, "")

	// Remove strikethrough
	content = regexp.MustCompile(`~~([^~]+)~~`).ReplaceAllString(content, "$1")

	// Collapse whitespace
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")

	return strings.TrimSpace(content)
}
