package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// DelegateRequest is the body for the agent-to-agent delegation endpoint.
type DelegateRequest struct {
	TargetAgentID string `json:"target_agent_id"`
	Message       string `json:"message"`
	Model         string `json:"model,omitempty"`
	SystemPrompt  string `json:"system_prompt,omitempty"`
}

// DelegateResponse is the full result of a delegation.
type DelegateResponse struct {
	Delegation DelegationResponse `json:"delegation"`
	Response   string             `json:"response"`
}

// DelegateToAgent handles POST /api/agents/{id}/delegate — sends a task from
// one agent to another, executes it synchronously via the target agent's harness,
// and records the full lifecycle in the delegations table.
//
// This is the agent-to-agent delegation primitive: the source agent (:id) delegates
// a task to the target agent (target_agent_id), the target's harness processes it,
// and the result is returned. No external message bus is required — the existing
// harness Chat() interface is used directly.
func (a *API) DelegateToAgent(w http.ResponseWriter, r *http.Request) {
	sourceIDStr := chi.URLParam(r, "id")
	var sourceID pgtype.UUID
	if err := sourceID.Scan(sourceIDStr); err != nil {
		http.Error(w, "invalid source agent ID", http.StatusBadRequest)
		return
	}

	var req DelegateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetAgentID == "" || req.Message == "" {
		http.Error(w, "target_agent_id and message are required", http.StatusBadRequest)
		return
	}

	var targetID pgtype.UUID
	if err := targetID.Scan(req.TargetAgentID); err != nil {
		http.Error(w, "invalid target_agent_id", http.StatusBadRequest)
		return
	}

	// Prevent self-delegation (no-op)
	if bytes.Equal(sourceID.Bytes[:], targetID.Bytes[:]) {
		http.Error(w, "source and target agents are the same", http.StatusBadRequest)
		return
	}

	// Look up both agents
	sourceAgent, err := a.queries.GetAgent(r.Context(), sourceID)
	if err != nil {
		http.Error(w, "source agent not found", http.StatusNotFound)
		return
	}

	targetAgent, err := a.queries.GetAgent(r.Context(), targetID)
	if err != nil {
		http.Error(w, "target agent not found", http.StatusNotFound)
		return
	}

	// Create delegation record (status=running)
	meta, _ := json.Marshal(map[string]string{
		"target_agent_id":  targetID.String(),
		"source_agent_name": sourceAgent.Name,
		"type":             "agent-to-agent",
	})

	deg, err := a.queries.CreateDelegation(r.Context(), db.CreateDelegationParams{
		ParentAgentID:  sourceID,
		ChildAgentName: targetAgent.Name,
		TaskGoal:       req.Message,
		Status:         "running",
		ResultSummary:  pgtype.Text{},
		Metadata:       meta,
	})
	if err != nil {
		http.Error(w, "failed to create delegation: "+err.Error(), http.StatusInternalServerError)
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

	// Execute the delegation via the target agent's harness.
	// Use a detached context since LLM calls can take 60-300s.
	execCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	h, err := a.registry.Get(targetAgent.Harness)
	if err != nil {
		a.failDelegation(execCtx, deg, fmt.Sprintf("unknown harness: %s", targetAgent.Harness))
		http.Error(w, "target agent harness not available", http.StatusBadRequest)
		return
	}

	config := a.buildHarnessConfig(execCtx, targetAgent)
	if err := h.Init(config); err != nil {
		a.failDelegation(execCtx, deg, "harness init failed: "+err.Error())
		http.Error(w, "target agent harness init failed", http.StatusInternalServerError)
		return
	}
	defer h.Close()

	// Build messages for the target agent
	messages := []harness.ChatMessage{
		{Role: "user", Content: req.Message},
	}

	// Prepend system prompt if the target agent has one configured
	if targetAgent.SystemPrompt.Valid && targetAgent.SystemPrompt.String != "" {
		systemMsg := targetAgent.SystemPrompt.String
		if req.SystemPrompt != "" {
			systemMsg = systemMsg + "\n\n" + req.SystemPrompt
		}
		messages = append([]harness.ChatMessage{{Role: "system", Content: systemMsg}}, messages...)
	} else if req.SystemPrompt != "" {
		messages = append([]harness.ChatMessage{{Role: "system", Content: req.SystemPrompt}}, messages...)
	}

	opts := harness.ChatOptions{
		Model: req.Model,
	}

	chunkCh, err := h.Chat(execCtx, messages, opts)
	if err != nil {
		if err == harness.ErrNotSupported {
			a.failDelegation(execCtx, deg, "target agent does not support chat")
			http.Error(w, "target agent does not support chat", http.StatusNotImplemented)
			return
		}
		a.failDelegation(execCtx, deg, "chat failed: "+err.Error())
		http.Error(w, "delegation chat failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Collect the full response from the chunk channel
	var fullResponse strings.Builder
	var chatErr error
	for chunk := range chunkCh {
		if chunk.Error != nil {
			chatErr = chunk.Error
			break
		}
		if chunk.Content != "" {
			fullResponse.WriteString(chunk.Content)
		}
	}

	if chatErr != nil {
		a.failDelegation(execCtx, deg, "stream error: "+chatErr.Error())
		http.Error(w, "delegation stream error: "+chatErr.Error(), http.StatusInternalServerError)
		return
	}

	responseText := fullResponse.String()
	if responseText == "" {
		responseText = "(empty response from target agent)"
	}

	// Truncate for the result_summary field (keeps DB rows reasonable)
	summary := responseText
	if len(summary) > 2000 {
		summary = summary[:2000] + "..."
	}

	// Update delegation with completion
	updatedDeg, err := a.queries.UpdateDelegation(execCtx, db.UpdateDelegationParams{
		ID:            deg.ID,
		Status:        "completed",
		ResultSummary: pgtype.Text{String: summary, Valid: true},
	})
	if err != nil {
		slog.Warn("failed to update delegation status", "delegation_id", deg.ID.String(), "error", err)
		updatedDeg = deg
	}

	// Broadcast completion SSE event
	a.bus.PublishTyped("delegation_updated", map[string]any{
		"id":               updatedDeg.ID.String(),
		"parent_agent_id":  updatedDeg.ParentAgentID.String(),
		"child_agent_name": updatedDeg.ChildAgentName,
		"task_goal":        updatedDeg.TaskGoal,
		"status":           updatedDeg.Status,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DelegateResponse{
		Delegation: delegationToResponse(updatedDeg),
		Response:   responseText,
	})
}

// failDelegation marks a delegation as failed with an error summary and
// broadcasts the update via SSE.
func (a *API) failDelegation(ctx context.Context, deg db.Delegation, errMsg string) {
	_, err := a.queries.UpdateDelegation(ctx, db.UpdateDelegationParams{
		ID:            deg.ID,
		Status:        "failed",
		ResultSummary: pgtype.Text{String: errMsg, Valid: true},
	})
	if err != nil {
		slog.Warn("failed to update delegation to failed status",
			"delegation_id", deg.ID.String(), "error", err)
	}

	a.bus.PublishTyped("delegation_updated", map[string]any{
		"id":               deg.ID.String(),
		"parent_agent_id":  deg.ParentAgentID.String(),
		"child_agent_name": deg.ChildAgentName,
		"task_goal":        deg.TaskGoal,
		"status":           "failed",
	})
}
