package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// SkillRoutes returns a Chi router with skill routes.
func (a *API) SkillRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListSkills)
	r.Post("/", a.CreateSkill)
	r.Post("/sync", a.SyncSkillsFromHermes)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetSkill)
		r.Patch("/", a.UpdateSkill)
		r.Delete("/", a.DeleteSkill)
	})

	return r
}

// ListSkills handles GET /api/skills
func (a *API) ListSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := a.queries.ListSkills(r.Context())
	if err != nil {
		http.Error(w, "failed to list skills: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if skills == nil {
		skills = []db.Skill{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skills)
}

// GetSkill handles GET /api/skills/{id}
func (a *API) GetSkill(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid skill ID", http.StatusBadRequest)
		return
	}

	skill, err := a.queries.GetSkill(r.Context(), id)
	if err != nil {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skill)
}

// CreateSkillRequest is the request body for creating a skill.
type CreateSkillRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Content     string   `json:"content"`
	Triggers    []string `json:"triggers"`
	AgentID     string   `json:"agent_id"`
}

// CreateSkill handles POST /api/skills
func (a *API) CreateSkill(w http.ResponseWriter, r *http.Request) {
	var req CreateSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	if req.Category == "" {
		req.Category = "general"
	}

	if req.Triggers == nil {
		req.Triggers = []string{}
	}

	var agentID pgtype.UUID
	if req.AgentID != "" {
		if err := agentID.Scan(req.AgentID); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	skill, err := a.queries.CreateSkill(r.Context(), db.CreateSkillParams{
		Name:        req.Name,
		Description: req.Description,
		Category:    req.Category,
		Content:     req.Content,
		Triggers:    req.Triggers,
		AgentID:     agentID,
	})
	if err != nil {
		http.Error(w, "failed to create skill: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(skill)
}

// UpdateSkillRequest is the request body for updating a skill.
type UpdateSkillRequest struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Category    *string  `json:"category"`
	Content     *string  `json:"content"`
	Triggers    []string `json:"triggers"`
	AgentID     *string  `json:"agent_id"`
}

// UpdateSkill handles PATCH /api/skills/{id}
func (a *API) UpdateSkill(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid skill ID", http.StatusBadRequest)
		return
	}

	existing, err := a.queries.GetSkill(r.Context(), id)
	if err != nil {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}

	var req UpdateSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	name := existing.Name
	if req.Name != nil && *req.Name != "" {
		name = *req.Name
	}

	description := existing.Description
	if req.Description != nil {
		description = *req.Description
	}

	category := existing.Category
	if req.Category != nil && *req.Category != "" {
		category = *req.Category
	}

	content := existing.Content
	if req.Content != nil {
		content = *req.Content
	}

	triggers := existing.Triggers
	if req.Triggers != nil {
		triggers = req.Triggers
	}

	agentID := existing.AgentID
	if req.AgentID != nil {
		if *req.AgentID == "" {
			agentID = pgtype.UUID{}
		} else if err := agentID.Scan(*req.AgentID); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	skill, err := a.queries.UpdateSkill(r.Context(), db.UpdateSkillParams{
		ID:          id,
		Name:        name,
		Description: description,
		Category:    category,
		Content:     content,
		Triggers:    triggers,
		AgentID:     agentID,
	})
	if err != nil {
		http.Error(w, "failed to update skill: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skill)
}

// DeleteSkill handles DELETE /api/skills/{id}
func (a *API) DeleteSkill(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid skill ID", http.StatusBadRequest)
		return
	}

	if err := a.queries.DeleteSkill(r.Context(), id); err != nil {
		http.Error(w, "failed to delete skill", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// hermesSkill represents a parsed Hermes SKILL.md file.
type hermesSkill struct {
	Name        string
	Description string
	Category    string
	Content     string
	Triggers    []string
}

// parseSkillFrontmatter parses a SKILL.md file and extracts name, description, and full content.
func parseSkillFrontmatter(data []byte) hermesSkill {
	var s hermesSkill
	s.Triggers = []string{}

	// Check for YAML frontmatter (between --- delimiters)
	content := string(data)
	if !strings.HasPrefix(content, "---") {
		s.Content = strings.TrimSpace(content)
		return s
	}

	// Find end of frontmatter
	end := strings.Index(content[3:], "---")
	if end == -1 {
		s.Content = strings.TrimSpace(content)
		return s
	}
	fm := content[3 : end+3]
	body := content[end+6:] // skip "---\n"

	// Parse frontmatter lines
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			s.Name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), "\"")
		} else if strings.HasPrefix(line, "description:") {
			s.Description = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), "\"")
		}
	}

	s.Content = strings.TrimSpace(body)
	return s
}

// SyncSkillsFromHermes handles POST /api/skills/sync
// Walks the Hermes skills directory, parses SKILL.md files, and upserts into the skills table.
func (a *API) SyncSkillsFromHermes(w http.ResponseWriter, r *http.Request) {
	skillsPath := a.hermesSkillsPath
	if skillsPath == "" {
		http.Error(w, "Hermes skills path not configured", http.StatusServiceUnavailable)
		return
	}

	// Check if path exists
	info, err := os.Stat(skillsPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Hermes skills path does not exist: "+skillsPath, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "Failed to access skills path: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !info.IsDir() {
		http.Error(w, "Hermes skills path is not a directory: "+skillsPath, http.StatusServiceUnavailable)
		return
	}

	// Walk the skills directory looking for SKILL.md files
	var skills []hermesSkill
	err = filepath.WalkDir(skillsPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		skill := parseSkillFrontmatter(data)
		if skill.Name == "" {
			// Derive name from parent directory
			parentDir := filepath.Base(filepath.Dir(path))
			skill.Name = parentDir
		}

		// Derive category from the top-level directory under skills/
		relPath, err := filepath.Rel(skillsPath, path)
		if err == nil {
			parts := strings.Split(relPath, string(os.PathSeparator))
			if len(parts) > 1 {
				skill.Category = parts[0]
			}
		}
		if skill.Category == "" {
			skill.Category = "general"
		}

		skills = append(skills, skill)
		return nil
	})
	if err != nil {
		http.Error(w, "Failed to walk skills directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the Roux agent ID to associate skills with
	// First try to find an agent named "roux" or "hermes"
	var agentID pgtype.UUID
	agents, err := a.queries.ListAgents(r.Context())
	if err == nil {
		for _, agent := range agents {
			if agent.Name == "roux" || agent.Name == "hermes" {
				agentID = agent.ID
				break
			}
		}
	}

	// Upsert each skill
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	synced := 0
	created := 0
	updated := 0
	var errors []string

	for _, skill := range skills {
		if skill.Name == "" {
			continue
		}

		triggers := skill.Triggers
		if triggers == nil {
			triggers = []string{}
		}

		result, err := a.queries.UpsertSkill(ctx, db.UpsertSkillParams{
			Name:        skill.Name,
			Description: skill.Description,
			Category:    skill.Category,
			Content:     skill.Content,
			Triggers:    triggers,
			AgentID:     agentID,
		})
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", skill.Name, err.Error()))
			continue
		}
		synced++
		_ = result
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"synced":  synced,
		"created": created,
		"updated": updated,
		"total":   len(skills),
		"errors":  errors,
	})
}
