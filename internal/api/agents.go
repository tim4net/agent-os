package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// ListAgents returns all visible agents (filtered by backend visibility).
func (a *API) ListAgents(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	// Workspace scoping (issue #134): when project_id is supplied, return only
	// the agents assigned to that workspace.
	if pidStr := r.URL.Query().Get("project_id"); pidStr != "" {
		var projectID pgtype.UUID
		if err := projectID.Scan(pidStr); err != nil {
			http.Error(w, "invalid project_id parameter", http.StatusBadRequest)
			return
		}
		agents, err := a.queries.ListAgentsByProject(r.Context(), db.ListAgentsByProjectParams{
			OwnerID:   ownerID,
			ProjectID: projectID,
		})
		if err != nil {
			http.Error(w, "failed to list agents", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sanitizeAgents(agents))
		return
	}

	agents, err := a.queries.ListVisibleAgents(r.Context(), ownerID)
	if err != nil {
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitizeAgents(agents))
}

// GetAgent returns a single agent by ID.
func (a *API) GetAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{
		ID:      id,
		OwnerID: ownerID,
	})
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
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Harness == "" || req.BaseURL == "" {
		http.Error(w, "name, harness, and base_url are required", http.StatusBadRequest)
		return
	}

	// display_name is optional: fall back to the agent's name (slug) when none is
	// supplied. This guarantees every agent has a meaningful label in the UI
	// instead of a placeholder/empty value, regardless of how it was registered.
	if req.DisplayName == "" {
		req.DisplayName = req.Name
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
		OwnerID:     ownerID,
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
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	// Confirm it exists first so a bad id returns 404, not a silent 204.
	if _, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: id, OwnerID: ownerID}); err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	if err := a.queries.DeleteAgent(r.Context(), db.DeleteAgentParams{ID: id, OwnerID: ownerID}); err != nil {
		http.Error(w, "failed to delete agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// seedOwnerUUIDBytes holds the raw bytes of the seed user UUID
// 00000000-0000-0000-0000-000000000001 (migration 024). This is the default
// owner when the identity middleware hasn't populated the context (tests, dev
// without auth proxy). Agents with a different owner_id fail the WHERE clause →
// 404.
var seedOwnerUUIDBytes = [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

// seedOwnerUUID constructs a fresh pgtype.UUID from seedOwnerUUIDBytes.
func seedOwnerUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: seedOwnerUUIDBytes, Valid: true}
}

// resolveOwnerID returns the owner UUID from the request context (set by the
// identity middleware), falling back to the seed owner when absent. The caller
// receives the resolved ID as a value so downstream functions have no hidden
// dependency on package-level state.
func resolveOwnerID(ctx context.Context) pgtype.UUID {
	if id, ok := OwnerIDFromContext(ctx); ok {
		return id
	}
	return seedOwnerUUID()
}

// UpdateAgentConfigRequest is the request body for updating agent config.
// Name is a pointer so we can distinguish "not provided" (nil) from "provided
// but empty" (non-nil, ""). An empty name triggers a 400 validation error.
type UpdateAgentConfigRequest struct {
	Name         *string         `json:"name"`
	Role         string          `json:"role"`
	SystemPrompt string          `json:"system_prompt"`
	Persona      json.RawMessage `json:"persona"`
}

// UpdateAgentConfig handles PATCH /api/agents/{id} to update role, system_prompt,
// persona, and optionally rename the agent (name + display_name).
//
// The handler delegates rename logic to handleAgentRename and config logic to
// updateAgentFields, keeping each responsibility in a focused function.
func (a *API) UpdateAgentConfig(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

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

	// --- Rename phase (if name is provided) ---
	if req.Name != nil {
		ownerID := resolveOwnerID(r.Context())

		renamed, ok := a.handleAgentRename(w, r, idStr, id, *req.Name, ownerID)
		if !ok {
			return // error response already written by handleAgentRename
		}

		// If no config fields to update, return the renamed agent.
		if req.Role == "" && req.SystemPrompt == "" && len(req.Persona) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(sanitizeAgent(renamed))
			return
		}

		// Fall through to update config on the just-renamed agent.
	}

	// --- Config update phase ---
	persona := []byte("{}")
	if len(req.Persona) > 0 {
		persona = req.Persona
	}

	agent, err := a.queries.UpdateAgentConfig(r.Context(), db.UpdateAgentConfigParams{
		ID:           id,
		OwnerID:      ownerID,
		Role:         pgtype.Text{String: req.Role, Valid: true},
		SystemPrompt: pgtype.Text{String: req.SystemPrompt, Valid: true},
		Persona:      persona,
	})
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitizeAgent(agent))
}

// handleAgentRename validates the new name, checks for owner-scoped duplicates,
// performs the rename query, and emits an activity event. It writes any HTTP
// error response to w and returns ok=false on failure, or returns the renamed
// agent and ok=true on success.
//
// ownerID is passed explicitly (resolved by the caller via resolveOwnerID) so
// this function has no hidden dependency on package-level or context state.
func (a *API) handleAgentRename(w http.ResponseWriter, r *http.Request, idStr string, id pgtype.UUID, newName string, ownerID pgtype.UUID) (db.Agent, bool) {
	name := strings.TrimSpace(newName)
	if name == "" {
		http.Error(w, "name must not be empty", http.StatusBadRequest)
		return db.Agent{}, false
	}
	if len(name) > 64 {
		http.Error(w, "name must be 64 characters or fewer", http.StatusBadRequest)
		return db.Agent{}, false
	}

	// Check for duplicate name within the same owner.
	if existing, err := a.queries.GetAgentByNameAndOwner(r.Context(), db.GetAgentByNameAndOwnerParams{
		Name:    name,
		OwnerID: ownerID,
	}); err == nil && existing.ID != id {
		http.Error(w, "an agent with this name already exists", http.StatusConflict)
		return db.Agent{}, false
	}

	// Perform the owner-scoped rename. If the agent belongs to a different
	// owner, the WHERE clause won't match → ErrNoRows → 404.
	renamed, err := a.queries.RenameAgent(r.Context(), db.RenameAgentParams{
		ID:          id,
		Name:        name,
		DisplayName: name, // display_name mirrors name on rename
		OwnerID:     ownerID,
	})
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return db.Agent{}, false
	}

	// Emit activity event for the rename (AC6).
	if a.bus != nil {
		a.bus.PublishTyped("agent_renamed", map[string]any{
			"agent_id":   idStr,
			"agent_name": name,
		})
	}

	return renamed, true
}

// GetAgentConfig handles GET /api/agents/{id}/config and returns role, system_prompt, persona.
func (a *API) GetAgentConfig(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":            agent.ID,
		"role":          agent.Role,
		"system_prompt": agent.SystemPrompt,
		"persona":       redactSecretKeys(agent.Persona),
	})
}

// GetAgentModels proxies the model list request to the agent's harness.
// It enriches model info with display names from a server-side mapping.
func (a *API) GetAgentModels(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: id, OwnerID: ownerID})
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

// GetAgentVersion returns the upstream version reported by an agent's harness.
func (a *API) GetAgentVersion(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	h, err := a.registry.Get(agent.Harness)
	if err != nil {
		http.Error(w, "unknown harness: "+agent.Harness, http.StatusBadRequest)
		return
	}

	respondUnknown := func() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&harness.VersionInfo{Current: "", Source: "unknown", CheckedAt: time.Now().UTC()})
	}

	config := a.buildHarnessConfig(r.Context(), agent)
	if err := h.Init(config); err != nil {
		slog.Warn("agent version harness init failed", "agent_id", idStr, "harness", agent.Harness, "error", err)
		respondUnknown()
		return
	}
	defer h.Close()

	prober, ok := h.(harness.VersionProber)
	if !ok {
		respondUnknown()
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	info, err := prober.VersionInfo(ctx)
	if err != nil || info == nil {
		if err != nil {
			slog.Warn("agent version probe failed", "agent_id", idStr, "harness", agent.Harness, "error", err)
		}
		respondUnknown()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// GetAgentCommands returns the slash commands available for an agent's harness.
func (a *API) GetAgentCommands(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: id, OwnerID: ownerID})
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
