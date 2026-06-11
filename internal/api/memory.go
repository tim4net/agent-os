package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// MemoryAPI holds dependencies for memory/vault endpoints.
type MemoryAPI struct {
	queries      *db.Queries
	obsidianPath string
	litellmURL   string
	llmModel     string
}

// NewMemoryAPI creates a new MemoryAPI.
func NewMemoryAPI(queries *db.Queries, obsidianPath string, litellmURL string, llmModel string) *MemoryAPI {
	return &MemoryAPI{
		queries:      queries,
		obsidianPath: obsidianPath,
		litellmURL:   litellmURL,
		llmModel:     llmModel,
	}
}

// TreeNode represents a file or directory in the vault tree.
type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Type     string      `json:"type"` // "file" or "dir"
	Modified time.Time   `json:"modified"`
	Size     int64       `json:"size"`
	Children []TreeNode  `json:"children,omitempty"`
}

// FileResponse is the JSON response for reading a file.
type FileResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Title   string `json:"title"`
}

// FileWriteRequest is the JSON body for writing a file.
type FileWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SearchHit represents a single search result.
type SearchHit struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	Title    string `json:"title"`
	Snippet  string `json:"snippet"`
}

// SynthesizeRequest is the JSON body for the synthesize endpoint.
type SynthesizeRequest struct {
	Paths []string `json:"paths"`
	Type  string   `json:"type"` // "summary", "study_guide", "flashcards", "outline"
}

// SynthesizeResponse is the JSON response for the synthesize endpoint.
type SynthesizeResponse struct {
	Type        string   `json:"type"`
	Content     string   `json:"content"`
	SourcePaths []string `json:"source_paths"`
}

// MemoryRoutes returns a Chi router with memory routes.
func (m *MemoryAPI) MemoryRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/tree", m.Tree)
	r.Get("/file", m.GetFile)
	r.Post("/file", m.WriteFile)
	r.Get("/search", m.Search)
	r.Post("/synthesize", m.Synthesize)

	return r
}

// isWithinBase reports whether absPath is the base directory itself or a
// descendant of it. It is a containment check that is robust to the
// sibling-prefix pitfall of a naive strings.HasPrefix (e.g. base "/data/vault"
// must NOT match "/data/vault-evil"). Both arguments must already be absolute.
func isWithinBase(absPath, absBase string) bool {
	if absPath == absBase {
		return true
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return false
	}
	// Any path that needs to climb out of base (".." or "../...") is outside.
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Tree handles GET /api/memory/tree?path=&depth=
func (m *MemoryAPI) Tree(w http.ResponseWriter, r *http.Request) {
	subPath := r.URL.Query().Get("path")
	root := filepath.Join(m.obsidianPath, subPath)

	// Security: ensure path is within obsidianPath
	absRoot, err := filepath.Abs(root)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	absBase, _ := filepath.Abs(m.obsidianPath)
	if !isWithinBase(absRoot, absBase) {
		http.Error(w, "path access denied", http.StatusForbidden)
		return
	}

	// Default depth=1 for lazy loading (only immediate children)
	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed >= 0 {
			depth = parsed
		}
	}

	tree, err := m.buildTree(absRoot, absBase, depth)
	if err != nil {
		// A missing vault directory is a legitimate empty/first-run state, not a
		// server error. The Knowledge > Files tab should render an empty tree
		// rather than a scary 500 when no notes exist yet.
		if errors.Is(err, fs.ErrNotExist) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]TreeNode{})
			return
		}
		http.Error(w, "failed to read tree: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Encode a non-nil slice so an empty (but present) vault returns [] not null.
	if tree == nil {
		tree = []TreeNode{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tree)
}

func (m *MemoryAPI) buildTree(root, base string, maxDepth int) ([]TreeNode, error) {
	var nodes []TreeNode

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		// Skip .obsidian directories
		if entry.Name() == ".obsidian" {
			continue
		}

		fullPath := filepath.Join(root, entry.Name())
		relPath, _ := filepath.Rel(base, fullPath)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		node := TreeNode{
			Name:     entry.Name(),
			Path:     relPath,
			Modified: info.ModTime(),
		}

		if entry.IsDir() {
			node.Type = "dir"
			// Only recurse if we have depth budget remaining
			if maxDepth > 0 {
				children, err := m.buildTree(fullPath, base, maxDepth-1)
				if err != nil {
					children = []TreeNode{}
				}
				node.Children = children
			} else {
				// Mark that this folder has unloaded children
				node.Children = nil
			}
		} else {
			node.Type = "file"
			node.Size = info.Size()
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// GetFile handles GET /api/memory/file?path=
func (m *MemoryAPI) GetFile(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path parameter required", http.StatusBadRequest)
		return
	}

	fullPath := filepath.Join(m.obsidianPath, filePath)

	// Security check
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	absBase, _ := filepath.Abs(m.obsidianPath)
	if !isWithinBase(absPath, absBase) {
		http.Error(w, "path access denied", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	content := string(data)
	title := extractTitle(content, filepath.Base(absPath))

	resp := FileResponse{
		Path:    filePath,
		Content: content,
		Title:   title,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// WriteFile handles POST /api/memory/file
func (m *MemoryAPI) WriteFile(w http.ResponseWriter, r *http.Request) {
	var req FileWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	// Ensure .md extension
	if !strings.HasSuffix(req.Path, ".md") {
		req.Path += ".md"
	}

	fullPath := filepath.Join(m.obsidianPath, req.Path)

	// Security check
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	absBase, _ := filepath.Abs(m.obsidianPath)
	if !isWithinBase(absPath, absBase) {
		http.Error(w, "path access denied", http.StatusForbidden)
		return
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		http.Error(w, "failed to create directory", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(absPath, []byte(req.Content), 0644); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	title := extractTitle(req.Content, filepath.Base(absPath))

	resp := FileResponse{
		Path:    req.Path,
		Content: req.Content,
		Title:   title,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Search handles GET /api/memory/search?q=&project_id=
func (m *MemoryAPI) Search(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "q parameter required", http.StatusBadRequest)
		return
	}

	limit := int32(20)
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 {
			limit = int32(n)
		}
	}

	// Optional project_id filter — when provided, restrict results to that project.
	var projectID pgtype.UUID
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		if err := projectID.Scan(pid); err != nil {
			http.Error(w, "invalid project_id parameter", http.StatusBadRequest)
			return
		}
	}

	results, err := m.queries.SearchMemory(r.Context(), db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: query,
		Limit:              limit,
		ProjectID:          projectID,
	})
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	hits := make([]SearchHit, 0, len(results))
	for _, r := range results {
		idStr := ""
		if r.ID.Valid {
			idStr = r.ID.String()
		}
		title := ""
		if r.Title.Valid {
			title = r.Title.String
		}
		snippet := ""
		if r.Content.Valid {
			snippet = truncate(r.Content.String, 200)
		}
		hits = append(hits, SearchHit{
			ID:       idStr,
			FilePath: r.FilePath,
			Title:    title,
			Snippet:  snippet,
		})
	}

	if hits == nil {
		hits = []SearchHit{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hits)
}

// Synthesize handles POST /api/memory/synthesize
func (m *MemoryAPI) Synthesize(w http.ResponseWriter, r *http.Request) {
	var req SynthesizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.Paths) == 0 {
		http.Error(w, "at least one path is required", http.StatusBadRequest)
		return
	}

	validTypes := map[string]bool{"summary": true, "study_guide": true, "flashcards": true, "outline": true}
	if !validTypes[req.Type] {
		http.Error(w, "type must be one of: summary, study_guide, flashcards, outline", http.StatusBadRequest)
		return
	}

	// Read all note files
	var allContent strings.Builder
	absBase, _ := filepath.Abs(m.obsidianPath)

	for _, p := range req.Paths {
		fullPath := filepath.Join(m.obsidianPath, p)
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			continue
		}
		if !isWithinBase(absPath, absBase) {
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		allContent.WriteString(fmt.Sprintf("--- File: %s ---\n\n%s\n\n", p, string(data)))
	}

	if allContent.Len() == 0 {
		http.Error(w, "no readable files found", http.StatusBadRequest)
		return
	}

	// Call LiteLLM for synthesis
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type chatRequest struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}
	type chatChoice struct {
		Message chatMessage `json:"message"`
	}
	type chatResponse struct {
		Choices []chatChoice `json:"choices"`
	}

	systemPrompt := fmt.Sprintf(
		"You are a knowledge synthesis assistant. Given these notes, produce a %s. Use markdown formatting.",
		req.Type,
	)

	chatReq := chatRequest{
		Model: m.llmModel,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: allContent.String()},
		},
	}

	body, _ := json.Marshal(chatReq)
	resp, err := http.Post(m.litellmURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		http.Error(w, "failed to parse LLM response", http.StatusInternalServerError)
		return
	}

	synthesis := ""
	if len(chatResp.Choices) > 0 {
		synthesis = chatResp.Choices[0].Message.Content
	}

	// Save synthesis as a new note
	timestamp := time.Now().Format("2006-01-02-150405")
	synthesisPath := filepath.Join("syntheses", fmt.Sprintf("%s-%s.md", req.Type, timestamp))
	synthesisFull := filepath.Join(m.obsidianPath, synthesisPath)

	if err := os.MkdirAll(filepath.Dir(synthesisFull), 0755); err == nil {
		title := fmt.Sprintf("Synthesis: %s (%s)", req.Type, timestamp)
		fileContent := fmt.Sprintf("# %s\n\n> Generated from: %s\n\n%s", title, strings.Join(req.Paths, ", "), synthesis)
		os.WriteFile(synthesisFull, []byte(fileContent), 0644)
	}

	result := SynthesizeResponse{
		Type:        req.Type,
		Content:     synthesis,
		SourcePaths: req.Paths,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// extractTitle extracts the title from the first # heading or YAML frontmatter.
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

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// skipObsidianDir returns true if the path contains .obsidian directory.
func skipObsidianDir(path string, info fs.FileInfo) bool {
	return info.IsDir() && info.Name() == ".obsidian"
}

// pgtypeText is a helper to create a pgtype.Text.
func pgtypeText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// logf is a helper for logging.
func logf(format string, args ...any) {
	slog.Info("memory: "+fmt.Sprintf(format, args...))
}
