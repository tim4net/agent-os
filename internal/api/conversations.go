package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ExportConversation handles POST /api/conversations/:id/export
func (a *API) ExportConversation(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var convID pgtype.UUID
	if err := convID.Scan(idStr); err != nil {
		http.Error(w, "invalid conversation ID", http.StatusBadRequest)
		return
	}

	// Get conversation
	conv, err := a.queries.GetConversation(r.Context(), db.GetConversationParams{ID: convID, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}

	// Get messages
	messages, err := a.queries.ListMessages(r.Context(), db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "failed to list messages", http.StatusInternalServerError)
		return
	}

	// Get agent name
	agentName := "unknown"
	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: conv.AgentID, OwnerID: ownerID})
	if err == nil {
		agentName = agent.DisplayName
	}

	// Build markdown content with YAML frontmatter
	title := "Conversation Export"
	if conv.Title.Valid {
		title = conv.Title.String
	}

	date := time.Now().UTC().Format("2006-01-02")
	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	slug = regexp.MustCompile(`-+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	filename := fmt.Sprintf("%s-%s.md", date, slug)

	// YAML frontmatter
	frontmatter := fmt.Sprintf("---\nconversation_id: %s\nagent: %s\ndate: %s\nmessages: %d\n---",
		convID.String(), agentName, date, len(messages))

	// Body: formatted chat messages
	var body strings.Builder
	body.WriteString(fmt.Sprintf("\n\n# %s\n\n", title))
	for _, msg := range messages {
		role := msg.Role
		if role == "user" {
			role = "👤 User"
		} else if role == "assistant" {
			role = "🤖 Assistant"
		}
		body.WriteString(fmt.Sprintf("## %s\n\n%s\n\n---\n\n", role, msg.Content))
	}

	content := frontmatter + body.String()

	// Write to Obsidian vault
	vaultDir := filepath.Join(a.obsidianPath, "projects", "agent-os", "conversations")
	if err := os.MkdirAll(vaultDir, 0755); err != nil {
		slog.Error("failed to create vault directory", "path", vaultDir, "error", err)
		http.Error(w, "failed to create export directory", http.StatusInternalServerError)
		return
	}

	fullPath := filepath.Join(vaultDir, filename)
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		slog.Error("failed to write export file", "path", fullPath, "error", err)
		http.Error(w, "failed to write export file", http.StatusInternalServerError)
		return
	}

	relPath := filepath.Join("projects", "agent-os", "conversations", filename)
	slog.Info("exported conversation to obsidian", "conversation_id", convID.String(), "path", relPath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "exported",
		"path":     relPath,
		"filename": filename,
		"messages": len(messages),
	})
}

// GetArtifactNotes handles GET /api/artifacts/:id/notes
func (a *API) GetArtifactNotes(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid artifact ID", http.StatusBadRequest)
		return
	}

	artifact, err := a.queries.GetArtifact(r.Context(), db.GetArtifactParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	// Search for the filename in the artifact's file_path
	searchTerm := ""
	if artifact.FilePath.Valid {
		searchTerm = filepath.Base(artifact.FilePath.String)
	}
	if searchTerm == "" && artifact.Title.Valid {
		searchTerm = artifact.Title.String
	}
	if searchTerm == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]db.MemoryIndex{})
		return
	}

	results, err := a.queries.SearchMemory(r.Context(), db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: searchTerm,
		Limit:              20,
	})
	if err != nil {
		slog.Error("failed to search memory for artifact notes", "error", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []db.MemoryIndex{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// GetTaskNotes handles GET /api/tasks/:id/notes
func (a *API) GetTaskNotes(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := a.queries.GetTask(r.Context(), db.GetTaskParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	searchTerm := task.Title
	if searchTerm == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]db.MemoryIndex{})
		return
	}

	results, err := a.queries.SearchMemory(r.Context(), db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: searchTerm,
		Limit:              20,
	})
	if err != nil {
		slog.Error("failed to search memory for task notes", "error", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []db.MemoryIndex{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
