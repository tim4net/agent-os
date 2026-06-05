package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// ListAgents returns all visible agents (filtered by backend visibility).
func (a *API) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.queries.ListVisibleAgents(r.Context())
	if err != nil {
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitizeAgents(agents))
}

// GetAgent returns a single agent by ID.
func (a *API) GetAgent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitizeAgent(agent))
}

// CreateAgentRequest is the request body for creating an agent.
type CreateAgentRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	BaseURL     string `json:"base_url"`
	AuthToken   string `json:"auth_token"` // optional; stored in metadata for harnesses that use it
}

// CreateAgent creates a new agent.
func (a *API) CreateAgent(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.DisplayName == "" || req.Harness == "" || req.BaseURL == "" {
		http.Error(w, "name, display_name, harness, and base_url are required", http.StatusBadRequest)
		return
	}

	// Validate the harness is a registered type so we never persist an agent
	// pointing at a non-existent driver.
	if _, err := a.registry.Get(req.Harness); err != nil {
		http.Error(w, "unknown harness: "+req.Harness, http.StatusBadRequest)
		return
	}

	// auth_token (if provided) is encrypted at rest in metadata; buildHarnessConfig
	// decrypts it for openclaw. If a token is supplied but encryption is unavailable,
	// refuse rather than persist plaintext.
	metadata, ok := a.encodeAuthTokenMetadata(req.AuthToken)
	if !ok {
		http.Error(w, "secret storage is disabled: set AOS_MASTER_KEY on the server to store an agent auth token", http.StatusServiceUnavailable)
		return
	}

	agent, err := a.queries.CreateAgent(r.Context(), db.CreateAgentParams{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Harness:     req.Harness,
		BaseUrl:     req.BaseURL,
		Metadata:    metadata,
	})
	if err != nil {
		http.Error(w, "failed to create agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sanitizeAgent(agent))
}

// DeleteAgent handles DELETE /api/agents/{id}.
func (a *API) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	// Confirm it exists first so a bad id returns 404, not a silent 204.
	if _, err := a.queries.GetAgent(r.Context(), id); err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	if err := a.queries.DeleteAgent(r.Context(), id); err != nil {
		http.Error(w, "failed to delete agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UpdateAgentConfigRequest is the request body for updating agent config.
type UpdateAgentConfigRequest struct {
	Role         string          `json:"role"`
	SystemPrompt string          `json:"system_prompt"`
	Persona      json.RawMessage `json:"persona"`
}

// UpdateAgentConfig handles PATCH /api/agents/{id} to update role, system_prompt, persona.
func (a *API) UpdateAgentConfig(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	var req UpdateAgentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	persona := []byte("{}")
	if len(req.Persona) > 0 {
		persona = req.Persona
	}

	agent, err := a.queries.UpdateAgentConfig(r.Context(), db.UpdateAgentConfigParams{
		ID:           id,
		Role:         pgtype.Text{String: req.Role, Valid: true},
		SystemPrompt: pgtype.Text{String: req.SystemPrompt, Valid: true},
		Persona:      persona,
	})
	if err != nil {
		http.Error(w, "failed to update agent config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitizeAgent(agent))
}

// GetAgentConfig handles GET /api/agents/{id}/config and returns role, system_prompt, persona.
func (a *API) GetAgentConfig(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":            agent.ID,
		"role":          agent.Role,
		"system_prompt": agent.SystemPrompt,
		"persona":       agent.Persona,
	})
}

// GetAgentModels proxies the model list request to the agent's harness.
// It enriches model info with display names from a server-side mapping.
func (a *API) GetAgentModels(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	h, err := a.registry.Get(agent.Harness)
	if err != nil {
		http.Error(w, "unknown harness: "+agent.Harness, http.StatusBadRequest)
		return
	}

	config := a.buildHarnessConfig(r.Context(), agent)
	if err := h.Init(config); err != nil {
		http.Error(w, "harness init failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer h.Close()

	models, err := h.ListModels(r.Context())
	if err != nil {
		if err == harness.ErrNotSupported {
			http.Error(w, "model listing not supported for this agent", http.StatusNotImplemented)
			return
		}
		http.Error(w, "failed to list models: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter out non-chat models (TTS, embedding, image gen, etc.)
	chatModels := make([]harness.ModelInfo, 0, len(models))
	for _, m := range models {
		if strings.HasPrefix(m.ID, "tts-") || m.ID == "tts-1" || m.ID == "tts-1-hd" {
			continue // skip text-to-speech models
		}
		if strings.HasPrefix(m.ID, "embed-") {
			continue // skip embedding models
		}
		if strings.HasPrefix(m.ID, "whisper") {
			continue // skip transcription models
		}
		if strings.HasPrefix(m.ID, "dall-") {
			continue // skip image generation models
		}
		chatModels = append(chatModels, m)
	}

	// Enrich with server-side display names
	for i := range chatModels {
		chatModels[i].DisplayName = modelDisplayName(chatModels[i].ID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chatModels)
}

// modelDisplayName returns a human-friendly name for a model ID.
// This mapping lives server-side so the frontend doesn't need a label map.
func modelDisplayName(id string) string {
	names := map[string]string{
		"local-qwen": "Qwen (Local)",
		"local-chat": "Chat (Local)",
		"free-chat":  "Chat (Free)",
		"free-fast":  "Fast (Free)",
		"free-deep":  "Deep Think (Free)",
		"free-gpt":   "GPT (Free)",
		"clovis":     "Clovis",
	}
	if name, ok := names[id]; ok {
		return name
	}
	return id
}

// GetAgentCommands returns the slash commands available for an agent's harness.
func (a *API) GetAgentCommands(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	h, err := a.registry.Get(agent.Harness)
	if err != nil {
		http.Error(w, "unknown harness: "+agent.Harness, http.StatusBadRequest)
		return
	}

	config := a.buildHarnessConfig(r.Context(), agent)
	if err := h.Init(config); err != nil {
		// Return empty commands if harness can't init (e.g., offline agent)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]harness.Command{})
		return
	}
	defer h.Close()

	commands := h.Commands()
	if commands == nil {
		commands = []harness.Command{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commands)
}
