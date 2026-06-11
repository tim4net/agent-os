package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// DelegationRequest is the webhook payload from Hermes.
type DelegationRequest struct {
	ParentAgentID  string          `json:"parent_agent_id"`
	ChildAgentName string          `json:"child_agent_name"`
	TaskGoal       string          `json:"task_goal"`
	Status         string          `json:"status"`
	ResultSummary  string          `json:"result_summary,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// DelegationResponse is the API response shape.
type DelegationResponse struct {
	ID             string          `json:"id"`
	ParentAgentID  string          `json:"parent_agent_id"`
	ChildAgentName string          `json:"child_agent_name"`
	TaskGoal       string          `json:"task_goal"`
	Status         string          `json:"status"`
	ResultSummary  string          `json:"result_summary,omitempty"`
	CreatedAt      string          `json:"created_at"`
	CompletedAt    string          `json:"completed_at,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// delegationToResponse converts a db Delegation row to API response.
func delegationToResponse(d db.Delegation) DelegationResponse {
	resp := DelegationResponse{
		ID:             d.ID.String(),
		ParentAgentID:  d.ParentAgentID.String(),
		ChildAgentName: d.ChildAgentName,
		TaskGoal:       d.TaskGoal,
		Status:         d.Status,
		CreatedAt:      d.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
		Metadata:       json.RawMessage(d.Metadata),
	}
	if d.ResultSummary.Valid {
		resp.ResultSummary = d.ResultSummary.String
	}
	if d.CompletedAt.Valid {
		resp.CompletedAt = d.CompletedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

// CreateDelegation handles POST /api/delegations — Hermes fires this when delegate_task starts/completes.
func (a *API) CreateDelegation(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var req DelegationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ParentAgentID == "" || req.ChildAgentName == "" || req.TaskGoal == "" {
		http.Error(w, "parent_agent_id, child_agent_name, and task_goal are required", http.StatusBadRequest)
		return
	}
	if req.Status == "" {
		req.Status = "pending"
	}

	var parentID pgtype.UUID
	if err := parentID.Scan(req.ParentAgentID); err != nil {
		http.Error(w, "invalid parent_agent_id", http.StatusBadRequest)
		return
	}

	var summary pgtype.Text
	if req.ResultSummary != "" {
		summary = pgtype.Text{String: req.ResultSummary, Valid: true}
	}

	meta := []byte("{}")
	if len(req.Metadata) > 0 {
		meta = req.Metadata
	}

	deg, err := a.queries.CreateDelegation(r.Context(), db.CreateDelegationParams{
		OwnerID:        ownerID,
		ParentAgentID:  parentID,
		ChildAgentName: req.ChildAgentName,
		TaskGoal:       req.TaskGoal,
		Status:         req.Status,
		ResultSummary:  summary,
		Metadata:       meta,
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast SSE event
	a.bus.PublishTyped("delegation_created", map[string]any{
		"id":               deg.ID.String(),
		"parent_agent_id":  deg.ParentAgentID.String(),
		"child_agent_name": deg.ChildAgentName,
		"task_goal":        deg.TaskGoal,
		"status":           deg.Status,
	})

	// FIX (finding #4): Run shim synchronously so errors are surfaced before responding.
	// FIX (finding #4): Stamp ts = now (server clock) so synthetic events don't trip the ±10min skew rule.
	if err := a.synthesizeWorkEvent(r.Context(), deg, ""); err != nil {
		slog.Default().Warn("failed to synthesize work_event from delegation",
			"delegation_id", deg.ID.String(),
			"error", err,
		)
		// Surface to caller — 202 since the delegation itself was created
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"id":             deg.ID.String(),
			"warning":        "delegation created but work_event synthesis failed",
			"synthesis_error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delegationToResponse(deg))
}

// synthesizeWorkEvent creates a work_event row from a delegation (legacy bridge shim).
// kindOverride, if non-empty, forces a specific kind (used for PATCH terminal synthesis).
// Otherwise the kind is derived from the delegation status.
func (a *API) synthesizeWorkEvent(ctx context.Context, deg db.Delegation, kindOverride string) error {
	req := service.BuildBridgeWorkEventRequest(deg, kindOverride)

	artifactsPath := os.Getenv("AGENTOS_ARTIFACTS_PATH")
	if artifactsPath == "" {
		artifactsPath = "/data/artifacts"
	}
	svc := service.NewIngestService(a.queries, a.bus, slog.Default(), artifactsPath)

	_, _, err := svc.Ingest(ctx, req, deg.OwnerID)
	return err
}

// UpdateDelegationStatus handles PATCH /api/delegations/{id} — Hermes fires this on completion/failure.
func (a *API) UpdateDelegationStatus(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	id := chi.URLParam(r, "id")
	var uid pgtype.UUID
	if err := uid.Scan(id); err != nil {
		http.Error(w, "invalid delegation id", http.StatusBadRequest)
		return
	}

	var req struct {
		Status        string `json:"status"`
		ResultSummary string `json:"result_summary,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	var summary pgtype.Text
	if req.ResultSummary != "" {
		summary = pgtype.Text{String: req.ResultSummary, Valid: true}
	}

	deg, err := a.queries.UpdateDelegation(r.Context(), db.UpdateDelegationParams{
		ID:            uid,
		OwnerID:       ownerID,
		Status:        req.Status,
		ResultSummary: summary,
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast SSE event
	a.bus.PublishTyped("delegation_updated", map[string]any{
		"id":               deg.ID.String(),
		"parent_agent_id":  deg.ParentAgentID.String(),
		"child_agent_name": deg.ChildAgentName,
		"task_goal":        deg.TaskGoal,
		"status":           deg.Status,
	})

	// FIX (finding #5): Synthesize a work_event for terminal delegation states via PATCH path.
	// CreateDelegation already handles the POST path. UpdateDelegationStatus carries
	// completion/failure, so we must synthesize session.end here too.
	// FIX (finding #3 rev2): Surface synthesis errors instead of swallowing them.
	if req.Status == "completed" || req.Status == "failed" || req.Status == "interrupted" {
		if err := a.synthesizeWorkEvent(r.Context(), deg, "session.end"); err != nil {
			// Surface the error — the delegation update succeeded but synthesis failed.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"id":               deg.ID.String(),
				"warning":          "delegation updated but work_event synthesis failed",
				"synthesis_error":  err.Error(),
			})
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delegationToResponse(deg))
}

// ListDelegationsHandler handles GET /api/delegations — for the UI.
func (a *API) ListDelegationsHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32)
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32)

	var pgAgentID pgtype.UUID
	if agentID != "" {
		pgAgentID = pgtype.UUID{Valid: true}
		if err := pgAgentID.Scan(agentID); err != nil {
			pgAgentID = pgtype.UUID{Valid: false}
		}
	}

	degs, err := a.queries.ListDelegations(r.Context(), db.ListDelegationsParams{
		OwnerID: ownerID,
		Column2: pgAgentID,
		Column3: status,
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := make([]DelegationResponse, len(degs))
	for i, d := range degs {
		resp[i] = delegationToResponse(d)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"delegations": resp})
}

// DelegationRoutes returns a router for delegation endpoints.
func (a *API) DelegationRoutes() http.Handler {
	r := chi.NewRouter()
	r.Post("/", a.CreateDelegation)
	r.Get("/", a.ListDelegationsHandler)
	r.Patch("/{id}", a.UpdateDelegationStatus)
	return r
}
