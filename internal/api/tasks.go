package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// TaskRoutes returns a Chi router with task routes.
func (a *API) TaskRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListTasks)
	r.Post("/", a.CreateTask)
	r.Post("/reorder", a.ReorderTasks)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetTask)
		r.Put("/", a.UpdateTask)
		r.Delete("/", a.DeleteTask)
		r.Post("/breakdown", a.BreakdownTask)
	})

	return r
}

// ListTasks handles GET /api/tasks?status=&agent_id=&priority=
func (a *API) ListTasks(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	statusFilter := r.URL.Query().Get("status")
	agentIDStr := r.URL.Query().Get("agent_id")
	priorityStr := r.URL.Query().Get("priority")

	var statusParam pgtype.Text
	if statusFilter != "" {
		statusParam = pgtype.Text{String: statusFilter, Valid: true}
	}

	var agentIDParam pgtype.UUID
	if agentIDStr != "" {
		if err := agentIDParam.Scan(agentIDStr); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	tasks, err := a.queries.ListTasks(r.Context(), db.ListTasksParams{
		OwnerID: ownerID,
		Column2: statusParam.String,
		Column3: agentIDParam,
	})
	if err != nil {
		http.Error(w, "failed to list tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter by priority in code since sqlc query doesn't support it
	if priorityStr != "" {
		priorityVal, err := strconv.Atoi(priorityStr)
		if err != nil {
			http.Error(w, "invalid priority", http.StatusBadRequest)
			return
		}
		var filtered []db.Task
		for _, t := range tasks {
			if t.Priority.Valid && t.Priority.Int32 == int32(priorityVal) {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	if tasks == nil {
		tasks = []db.Task{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// GetTask handles GET /api/tasks/{id}
func (a *API) GetTask(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// CreateTaskRequest is the request body for creating a task.
type CreateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    *int   `json:"priority"`
	AgentID     string `json:"agent_id"`
	DueDate     string `json:"due_date"`
}

// CreateTask handles POST /api/tasks
func (a *API) CreateTask(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	status := req.Status
	if status == "" {
		status = "backlog"
	}

	priority := 0
	if req.Priority != nil {
		priority = *req.Priority
	}

	var agentID pgtype.UUID
	if req.AgentID != "" {
		if err := agentID.Scan(req.AgentID); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	// Store due_date in metadata
	metadata := map[string]any{}
	if req.DueDate != "" {
		metadata["due_date"] = req.DueDate
	}
	metadataJSON, _ := json.Marshal(metadata)

	task, err := a.queries.CreateTask(r.Context(), db.CreateTaskParams{
		OwnerID:     ownerID,
		AgentID:     agentID,
		Title:       req.Title,
		Description: pgtypeText(req.Description),
		Status:      status,
		Priority:    pgtype.Int4{Int32: int32(priority), Valid: true},
		Metadata:    metadataJSON,
	})
	if err != nil {
		http.Error(w, "failed to create task: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}

// UpdateTaskRequest is the request body for updating a task.
type UpdateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    *int   `json:"priority"`
	AgentID     string `json:"agent_id"`
	DueDate     string `json:"due_date"`
}

// UpdateTask handles PUT /api/tasks/{id}
func (a *API) UpdateTask(w http.ResponseWriter, r *http.Request) {
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

	var req UpdateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Get existing task to preserve fields not being updated
	existing, err := a.queries.GetTask(r.Context(), db.GetTaskParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	title := req.Title
	if title == "" {
		title = existing.Title
	}

	description := req.Description
	if description == "" && existing.Description.Valid {
		description = existing.Description.String
	}

	status := req.Status
	if status == "" {
		status = existing.Status
	}

	priority := existing.Priority
	if req.Priority != nil {
		priority = pgtype.Int4{Int32: int32(*req.Priority), Valid: true}
	}

	// Merge metadata with existing to handle due_date
	var metadata map[string]any
	if existing.Metadata != nil {
		json.Unmarshal(existing.Metadata, &metadata)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if req.DueDate != "" {
		metadata["due_date"] = req.DueDate
	}
	// Update agent_id in metadata if provided
	if req.AgentID != "" {
		metadata["agent_id"] = req.AgentID
	}
	metadataJSON, _ := json.Marshal(metadata)

	task, err := a.queries.UpdateTask(r.Context(), db.UpdateTaskParams{
		ID:          id,
		OwnerID:     ownerID,
		Title:       title,
		Description: pgtypeText(description),
		Status:      status,
		Priority:    priority,
		Metadata:    metadataJSON,
	})
	if err != nil {
		http.Error(w, "failed to update task: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// DeleteTask handles DELETE /api/tasks/{id}
func (a *API) DeleteTask(w http.ResponseWriter, r *http.Request) {
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

	if err := a.queries.DeleteTask(r.Context(), db.DeleteTaskParams{ID: id, OwnerID: ownerID}); err != nil {
		http.Error(w, "failed to delete task", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReorderTasksRequest is the request body for batch reordering tasks.
type ReorderTasksRequest struct {
	Tasks []ReorderTaskItem `json:"tasks"`
}

// ReorderTaskItem represents a single task in a reorder batch.
type ReorderTaskItem struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
}

// ReorderTasks handles POST /api/tasks/reorder
func (a *API) ReorderTasks(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var req ReorderTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Tasks) == 0 {
		http.Error(w, "tasks array is required", http.StatusBadRequest)
		return
	}

	var updated []db.Task
	for _, item := range req.Tasks {
		var id pgtype.UUID
		if err := id.Scan(item.ID); err != nil {
			http.Error(w, "invalid task ID: "+item.ID, http.StatusBadRequest)
			return
		}

		// Get existing task to preserve title and description
		existing, err := a.queries.GetTask(r.Context(), db.GetTaskParams{ID: id, OwnerID: ownerID})
		if err != nil {
			http.Error(w, "task not found: "+item.ID, http.StatusNotFound)
			return
		}

		task, err := a.queries.UpdateTask(r.Context(), db.UpdateTaskParams{
			ID:          id,
			OwnerID:     ownerID,
			Title:       existing.Title,
			Description: existing.Description,
			Status:      item.Status,
			Priority:    pgtype.Int4{Int32: int32(item.Priority), Valid: true},
			Metadata:    existing.Metadata,
		})
		if err != nil {
			http.Error(w, "failed to update task: "+item.ID, http.StatusInternalServerError)
			return
		}
		updated = append(updated, task)
	}

	if updated == nil {
		updated = []db.Task{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// BreakdownTask handles POST /api/tasks/{id}/breakdown
// Sends the task to LiteLLM to break it into 3-8 subtasks.
func (a *API) BreakdownTask(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	// Use a detached context with generous timeout — the local LLM can be slow
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := a.queries.GetTask(ctx, db.GetTaskParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	// Build prompt from task
	taskText := task.Title
	if task.Description.Valid && task.Description.String != "" {
		taskText += " - " + task.Description.String
	}

	// Call LiteLLM for task breakdown
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

	chatReq := chatRequest{
		Model: a.llmModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a project planner. Break this task into 3-8 concrete subtasks. Return ONLY a JSON array of objects with 'title', 'description', 'priority' (1-5). No other text."},
			{Role: "user", Content: fmt.Sprintf("Task: %s", taskText)},
		},
	}

	body, _ := json.Marshal(chatReq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.litellmURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "LLM request create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read LLM response", http.StatusInternalServerError)
		return
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		http.Error(w, "failed to parse LLM response", http.StatusInternalServerError)
		return
	}

	if len(chatResp.Choices) == 0 {
		http.Error(w, "no response from LLM", http.StatusInternalServerError)
		return
	}

	content := chatResp.Choices[0].Message.Content

	// Extract JSON array from response (may be wrapped in markdown code block)
	jsonStr := content
	if idx := strings.Index(jsonStr, "["); idx >= 0 {
		jsonStr = jsonStr[idx:]
		if endIdx := strings.LastIndex(jsonStr, "]"); endIdx >= 0 {
			jsonStr = jsonStr[:endIdx+1]
		}
	}

	type breakdownItem struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
	}

	var items []breakdownItem
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil {
		http.Error(w, "failed to parse task breakdown: "+err.Error()+". Raw: "+content, http.StatusInternalServerError)
		return
	}

	// Create subtask rows with parent_task_id set
	var created []db.Task
	for _, item := range items {
		priority := item.Priority
		if priority < 1 {
			priority = 1
		}
		if priority > 5 {
			priority = 5
		}

		subtask, err := a.queries.CreateSubtask(ctx, db.CreateSubtaskParams{
			AgentID:      task.AgentID,
			Title:        item.Title,
			Description:  pgtypeText(item.Description),
			Status:       "backlog",
			Priority:     pgtype.Int4{Int32: int32(priority), Valid: true},
			Metadata:     []byte(fmt.Sprintf(`{"parent_task_id":"%s"}`, idStr)),
			ParentTaskID: pgtype.UUID{Bytes: id.Bytes, Valid: true},
		})
		if err != nil {
			http.Error(w, "failed to create subtask: "+err.Error(), http.StatusInternalServerError)
			return
		}
		created = append(created, subtask)
	}

	if created == nil {
		created = []db.Task{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(created)
}
