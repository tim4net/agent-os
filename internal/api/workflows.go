package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// WorkflowRoutes returns a Chi router with workflow routes.
func (a *API) WorkflowRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListWorkflows)
	r.Post("/", a.CreateWorkflow)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetWorkflow)
		r.Patch("/", a.UpdateWorkflow)
		r.Delete("/", a.DeleteWorkflow)
		r.Post("/run", a.RunWorkflow)
	})

	return r
}

// workflowResponse is a frontend-friendly representation of a workflow
// that properly deserializes Steps from JSONB instead of base64-encoding []byte.
type workflowResponse struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Steps       []WorkflowStep `json:"steps"`
	AgentID     string         `json:"agent_id"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
}

// workflowToResponse converts a db.Workflow (with raw JSONB Steps) into a
// frontend-safe workflowResponse with Steps as a proper JSON array.
func workflowToResponse(wf db.Workflow) (workflowResponse, error) {
	var steps []WorkflowStep
	if len(wf.Steps) > 0 {
		if err := json.Unmarshal(wf.Steps, &steps); err != nil {
			return workflowResponse{}, err
		}
	}
	if steps == nil {
		steps = []WorkflowStep{}
	}

	resp := workflowResponse{
		ID:          wf.ID.String(),
		Name:        wf.Name,
		Description: wf.Description.String,
		Steps:       steps,
		AgentID:     wf.AgentID.String(),
	}
	if wf.CreatedAt.Valid {
		resp.CreatedAt = wf.CreatedAt.Time.Format(time.RFC3339)
	}
	if wf.UpdatedAt.Valid {
		resp.UpdatedAt = wf.UpdatedAt.Time.Format(time.RFC3339)
	}
	return resp, nil
}

// ListWorkflows handles GET /api/workflows
func (a *API) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	workflows, err := a.queries.ListWorkflows(r.Context())
	if err != nil {
		http.Error(w, "failed to list workflows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if workflows == nil {
		workflows = []db.Workflow{}
	}

	resp := make([]workflowResponse, 0, len(workflows))
	for _, wf := range workflows {
		wr, err := workflowToResponse(wf)
		if err != nil {
			http.Error(w, "failed to serialize workflow: "+err.Error(), http.StatusInternalServerError)
			return
		}
		resp = append(resp, wr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetWorkflow handles GET /api/workflows/{id}
func (a *API) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid workflow ID", http.StatusBadRequest)
		return
	}

	workflow, err := a.queries.GetWorkflow(r.Context(), id)
	if err != nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	resp, err := workflowToResponse(workflow)
	if err != nil {
		http.Error(w, "failed to serialize workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CreateWorkflowRequest is the request body for creating a workflow.
type CreateWorkflowRequest struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Steps       []WorkflowStep   `json:"steps"`
	AgentID     string           `json:"agent_id"`
}

// WorkflowStep represents a single step in a workflow.
type WorkflowStep struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// CreateWorkflow handles POST /api/workflows
func (a *API) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req CreateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	stepsJSON, _ := json.Marshal(req.Steps)
	if len(req.Steps) == 0 {
		stepsJSON = []byte("[]")
	}

	var agentID pgtype.UUID
	if req.AgentID != "" {
		if err := agentID.Scan(req.AgentID); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	workflow, err := a.queries.CreateWorkflow(r.Context(), db.CreateWorkflowParams{
		Name:        req.Name,
		Description: pgtypeText(req.Description),
		Steps:       stepsJSON,
		AgentID:     agentID,
	})
	if err != nil {
		http.Error(w, "failed to create workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := workflowToResponse(workflow)
	if err != nil {
		http.Error(w, "failed to serialize workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// UpdateWorkflowRequest is the request body for updating a workflow.
type UpdateWorkflowRequest struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Steps       []WorkflowStep  `json:"steps"`
	AgentID     *string         `json:"agent_id"`
}

// UpdateWorkflow handles PATCH /api/workflows/{id}
func (a *API) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid workflow ID", http.StatusBadRequest)
		return
	}

	existing, err := a.queries.GetWorkflow(r.Context(), id)
	if err != nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	var req UpdateWorkflowRequest
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
		description = pgtypeText(*req.Description)
	}

	stepsJSON := existing.Steps
	if req.Steps != nil {
		stepsJSON, _ = json.Marshal(req.Steps)
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

	workflow, err := a.queries.UpdateWorkflow(r.Context(), db.UpdateWorkflowParams{
		ID:          id,
		Name:        name,
		Description: description,
		Steps:       stepsJSON,
		AgentID:     agentID,
	})
	if err != nil {
		http.Error(w, "failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := workflowToResponse(workflow)
	if err != nil {
		http.Error(w, "failed to serialize workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// DeleteWorkflow handles DELETE /api/workflows/{id}
func (a *API) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid workflow ID", http.StatusBadRequest)
		return
	}

	if err := a.queries.DeleteWorkflow(r.Context(), id); err != nil {
		http.Error(w, "failed to delete workflow", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RunWorkflow handles POST /api/workflows/{id}/run
// Executes each step sequentially through LiteLLM.
func (a *API) RunWorkflow(w http.ResponseWriter, r *http.Request) {
	// Use a detached context with generous timeout
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid workflow ID", http.StatusBadRequest)
		return
	}

	workflow, err := a.queries.GetWorkflow(ctx, id)
	if err != nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	// Parse steps from JSONB
	var steps []WorkflowStep
	if err := json.Unmarshal(workflow.Steps, &steps); err != nil {
		http.Error(w, "failed to parse workflow steps: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(steps) == 0 {
		http.Error(w, "workflow has no steps", http.StatusBadRequest)
		return
	}

	// Create a workflow run
	run, err := a.queries.CreateWorkflowRun(ctx, db.CreateWorkflowRunParams{
		WorkflowID:  id,
		Status:      pgtype.Text{String: "running", Valid: true},
		CurrentStep: pgtype.Int4{Int32: 0, Valid: true},
		Result:      []byte("{}"),
	})
	if err != nil {
		http.Error(w, "failed to create workflow run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Execute steps sequentially
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

	results := make([]map[string]string, 0, len(steps))

	for i, step := range steps {
		// Build context from previous steps
		contextStr := ""
		for j, prev := range results {
			if j < i {
				contextStr += "Step " + prev["name"] + " result: " + prev["output"] + "\n\n"
			}
		}

		prompt := step.Prompt
		if contextStr != "" {
			prompt = "Previous context:\n" + contextStr + "\n\nCurrent task: " + step.Prompt
		}

		chatReq := chatRequest{
			Model: "free-fast",
			Messages: []chatMessage{
				{Role: "system", Content: "You are an AI assistant executing workflow steps. Provide concise, actionable output."},
				{Role: "user", Content: prompt},
			},
		}

		body, _ := json.Marshal(chatReq)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.litellmURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			// Mark run as failed
			a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
				ID:          run.ID,
				Status:      pgtype.Text{String: "failed", Valid: true},
				CurrentStep: pgtype.Int4{Int32: int32(i), Valid: true},
				Result:      []byte(`{"error":"` + err.Error() + `"}`),
			})
			http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
				ID:          run.ID,
				Status:      pgtype.Text{String: "failed", Valid: true},
				CurrentStep: pgtype.Int4{Int32: int32(i), Valid: true},
				Result:      []byte(`{"error":"` + err.Error() + `"}`),
			})
			http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
				ID:          run.ID,
				Status:      pgtype.Text{String: "failed", Valid: true},
				CurrentStep: pgtype.Int4{Int32: int32(i), Valid: true},
				Result:      []byte(`{"error":"failed to read LLM response"}`),
			})
			http.Error(w, "failed to read LLM response", http.StatusInternalServerError)
			return
		}

		var chatResp chatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil || len(chatResp.Choices) == 0 {
			a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
				ID:          run.ID,
				Status:      pgtype.Text{String: "failed", Valid: true},
				CurrentStep: pgtype.Int4{Int32: int32(i), Valid: true},
				Result:      []byte(`{"error":"failed to parse LLM response"}`),
			})
			http.Error(w, "failed to parse LLM response", http.StatusInternalServerError)
			return
		}

		output := chatResp.Choices[0].Message.Content
		results = append(results, map[string]string{
			"name":   step.Name,
			"output": output,
		})

		// Update run progress
		resultJSON, _ := json.Marshal(map[string]any{
			"steps":   results,
			"current": step.Name,
		})
		a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
			ID:          run.ID,
			Status:      pgtype.Text{String: "running", Valid: true},
			CurrentStep: pgtype.Int4{Int32: int32(i + 1), Valid: true},
			Result:      resultJSON,
		})
	}

	// Mark as completed
	finalResult, _ := json.Marshal(map[string]any{
		"steps":    results,
		"completed": true,
	})
	run, err = a.queries.UpdateWorkflowRun(ctx, db.UpdateWorkflowRunParams{
		ID:          run.ID,
		Status:      pgtype.Text{String: "completed", Valid: true},
		CurrentStep: pgtype.Int4{Int32: int32(len(steps)), Valid: true},
		Result:      finalResult,
	})
	if err != nil {
		http.Error(w, "failed to finalize workflow run", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(run)
}
