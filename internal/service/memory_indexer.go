package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ProjectPathMapping maps a vault-relative path prefix to a project slug.
// When the indexer encounters a file whose relative path starts with one of
// these prefixes, it tags the memory row with the corresponding project_id.
type ProjectPathMapping struct {
	PathPrefix string // e.g. "projects/riftwing" or "Riftwing"
	Slug       string // e.g. "riftwing"
}

// DefaultProjectPathMappings provides the initial set of vault-folder → project
// slug mappings. This can be overridden via WithProjectPathMappings.
var DefaultProjectPathMappings = []ProjectPathMapping{
	{PathPrefix: "projects/riftwing", Slug: "riftwing"},
	{PathPrefix: "Riftwing", Slug: "riftwing"},
	{PathPrefix: "projects/agent-os", Slug: "agent-os"},
}

// MemoryIndexer walks the Obsidian vault periodically and indexes .md files.
type MemoryIndexer struct {
	queries             *db.Queries
	bus                 *EventBus
	obsidianPath        string
	stopCh              chan struct{}
	projectPathMappings []ProjectPathMapping

	mu       sync.RWMutex
	slugToID map[string]pgtype.UUID // slug → project UUID
}

// NewMemoryIndexer creates a new MemoryIndexer.
func NewMemoryIndexer(queries *db.Queries, bus *EventBus, obsidianPath string) *MemoryIndexer {
	return &MemoryIndexer{
		queries:             queries,
		bus:                 bus,
		obsidianPath:        obsidianPath,
		stopCh:              make(chan struct{}),
		projectPathMappings: DefaultProjectPathMappings,
		slugToID:            make(map[string]pgtype.UUID),
	}
}

// WithProjectPathMappings sets the path-prefix → slug mapping table.
func (mi *MemoryIndexer) WithProjectPathMappings(mappings []ProjectPathMapping) *MemoryIndexer {
	mi.projectPathMappings = mappings
	return mi
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

// RefreshProjectCache loads all projects from the database and builds the
// slug → id lookup map. Public so integration tests can trigger a cache
// refresh without running a full index cycle.
func (mi *MemoryIndexer) RefreshProjectCache(ctx context.Context) {
	mi.refreshProjectCache(ctx)
}

// refreshProjectCache loads all projects from the database and builds the
// slug → id lookup map. Called at the start of each index cycle.
func (mi *MemoryIndexer) refreshProjectCache(ctx context.Context) {
	projects, err := mi.queries.ListProjects(ctx)
	if err != nil {
		slog.Warn("memory-indexer: failed to load projects", "error", err)
		return
	}

	m := make(map[string]pgtype.UUID, len(projects))
	for _, p := range projects {
		m[p.Slug] = p.ID
	}

	mi.mu.Lock()
	mi.slugToID = m
	mi.mu.Unlock()
}

// DeriveProjectID maps a vault-relative file path to a project_id using the
// configured path-prefix → slug mappings and the cached slug → id lookup.
// Returns a zero-value pgtype.UUID (Valid=false) when no project matches.
func (mi *MemoryIndexer) DeriveProjectID(relPath string) pgtype.UUID {
	// Normalize separators to forward slashes for consistent matching.
	// strings.ReplaceAll handles both OS-native paths (filepath.ToSlash)
	// and literal backslashes that may appear in cross-platform data.
	relPath = strings.ReplaceAll(filepath.ToSlash(relPath), "\\", "/")

	mi.mu.RLock()
	defer mi.mu.RUnlock()

	for _, mapping := range mi.projectPathMappings {
		prefix := filepath.ToSlash(mapping.PathPrefix)
		if strings.HasPrefix(relPath, prefix+"/") || relPath == prefix {
			if id, ok := mi.slugToID[mapping.Slug]; ok {
				return id
			}
		}
	}
	return pgtype.UUID{}
}

func (mi *MemoryIndexer) index(ctx context.Context) {
	// Refresh the project cache each cycle so new projects are picked up.
	mi.refreshProjectCache(ctx)

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

		// Derive project_id from file path
		projectID := mi.DeriveProjectID(relPath)

		// Upsert into memory_index
		_, err = mi.queries.UpsertMemory(ctx, db.UpsertMemoryParams{
			FilePath:  relPath,
			Title:     pgtype.Text{String: title, Valid: title != ""},
			Content:   pgtype.Text{String: plainText, Valid: plainText != ""},
			Tags:      []string{},
			ProjectID: projectID,
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
