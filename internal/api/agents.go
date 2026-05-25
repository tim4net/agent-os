package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// ListAgents returns all registered agents.
func (a *API) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.queries.ListAgents(r.Context())
	if err != nil {
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
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
	json.NewEncoder(w).Encode(agent)
}

// CreateAgentRequest is the request body for creating an agent.
type CreateAgentRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	BaseURL     string `json:"base_url"`
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

	agent, err := a.queries.CreateAgent(r.Context(), db.CreateAgentParams{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Harness:     req.Harness,
		BaseUrl:     req.BaseURL,
		Metadata:    []byte("{}"),
	})
	if err != nil {
		http.Error(w, "failed to create agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(agent)
}

// GetAgentModels proxies the model list request to the agent's harness.
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

	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}
