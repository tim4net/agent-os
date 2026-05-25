package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// GoalRoutes returns a Chi router with goal routes.
func (a *API) GoalRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListGoals)
	r.Post("/", a.CreateGoal)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetGoal)
		r.Put("/", a.UpdateGoal)
		r.Delete("/", a.DeleteGoal)
		r.Post("/breakdown", a.BreakdownGoal)
	})

	return r
}

// ListGoals handles GET /api/goals
func (a *API) ListGoals(w http.ResponseWriter, r *http.Request) {
	goals, err := a.queries.ListGoals(r.Context())
	if err != nil {
		http.Error(w, "failed to list goals: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if goals == nil {
		goals = []db.Goal{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(goals)
}

// GetGoal handles GET /api/goals/{id}
func (a *API) GetGoal(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid goal ID", http.StatusBadRequest)
		return
	}

	goal, err := a.queries.GetGoal(r.Context(), id)
	if err != nil {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(goal)
}

// CreateGoalRequest is the request body for creating a goal.
type CreateGoalRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	TargetDate  string `json:"target_date"`
	Status      string `json:"status"`
}

// CreateGoal handles POST /api/goals
func (a *API) CreateGoal(w http.ResponseWriter, r *http.Request) {
	var req CreateGoalRequest
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
		status = "active"
	}

	var targetDate pgtype.Date
	if req.TargetDate != "" {
		if err := targetDate.Scan(req.TargetDate); err != nil {
			http.Error(w, "invalid target_date format (use YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}

	goal, err := a.queries.CreateGoal(r.Context(), db.CreateGoalParams{
		Title:       req.Title,
		Description: pgtypeText(req.Description),
		Status:      status,
		Progress:    pgtype.Float4{Float32: 0, Valid: true},
		TargetDate:  targetDate,
		Metadata:    []byte("{}"),
	})
	if err != nil {
		http.Error(w, "failed to create goal: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(goal)
}

// UpdateGoalRequest is the request body for updating a goal.
type UpdateGoalRequest struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Progress    float32 `json:"progress"`
	TargetDate  string  `json:"target_date"`
}

// UpdateGoal handles PUT /api/goals/{id}
func (a *API) UpdateGoal(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid goal ID", http.StatusBadRequest)
		return
	}

	var req UpdateGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Get existing goal to preserve fields
	existing, err := a.queries.GetGoal(r.Context(), id)
	if err != nil {
		http.Error(w, "goal not found", http.StatusNotFound)
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

	progress := existing.Progress
	if req.Progress != 0 {
		progress = pgtype.Float4{Float32: req.Progress, Valid: true}
	}

	targetDate := existing.TargetDate
	if req.TargetDate != "" {
		if err := targetDate.Scan(req.TargetDate); err != nil {
			http.Error(w, "invalid target_date format (use YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}

	goal, err := a.queries.UpdateGoal(r.Context(), db.UpdateGoalParams{
		ID:          id,
		Title:       title,
		Description: pgtypeText(description),
		Status:      status,
		Progress:    progress,
		TargetDate:  targetDate,
		Metadata:    existing.Metadata,
	})
	if err != nil {
		http.Error(w, "failed to update goal: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(goal)
}

// DeleteGoal handles DELETE /api/goals/{id}
func (a *API) DeleteGoal(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid goal ID", http.StatusBadRequest)
		return
	}

	if err := a.queries.DeleteGoal(r.Context(), id); err != nil {
		http.Error(w, "failed to delete goal", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BreakdownGoal handles POST /api/goals/{id}/breakdown
func (a *API) BreakdownGoal(w http.ResponseWriter, r *http.Request) {
	// Use a detached context with generous timeout — the local LLM can be slow
	// and Chi's middleware.Timeout(60s) would otherwise expire the request context.
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid goal ID", http.StatusBadRequest)
		return
	}

	goal, err := a.queries.GetGoal(ctx, id)
	if err != nil {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	// Build prompt from goal
	goalText := goal.Title
	if goal.Description.Valid && goal.Description.String != "" {
		goalText += "\n\n" + goal.Description.String
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
		Model: "local-qwen",
		Messages: []chatMessage{
			{Role: "system", Content: "You are a project planner. Break this goal into 5-10 specific actionable tasks. Return as JSON array of {title, description, priority(1-5)}. Return ONLY the JSON array, no other text."},
			{Role: "user", Content: goalText},
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

	type breakdownTask struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
	}

	var tasks []breakdownTask
	if err := json.Unmarshal([]byte(jsonStr), &tasks); err != nil {
		http.Error(w, "failed to parse task breakdown: "+err.Error()+". Raw: "+content, http.StatusInternalServerError)
		return
	}

	// Create tasks in DB
	var created []db.Task
	for _, t := range tasks {
		priority := t.Priority
		if priority < 1 {
			priority = 1
		}
		if priority > 5 {
			priority = 5
		}

		task, err := a.queries.CreateTask(ctx, db.CreateTaskParams{
			Title:       t.Title,
			Description: pgtypeText(t.Description),
			Status:      "backlog",
			Priority:    pgtype.Int4{Int32: int32(priority), Valid: true},
			Metadata:    []byte(fmt.Sprintf(`{"goal_id":"%s"}`, idStr)),
		})
		if err != nil {
			http.Error(w, "failed to create task: "+err.Error(), http.StatusInternalServerError)
			return
		}
		created = append(created, task)
	}

	if created == nil {
		created = []db.Task{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(created)
}
