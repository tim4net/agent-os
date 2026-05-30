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

// PipelineRoutes returns a Chi router with pipeline routes.
func (a *API) PipelineRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListPipelineItems)
	r.Post("/", a.CreatePipelineItem)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetPipelineItem)
		r.Put("/", a.UpdatePipelineItem)
		r.Delete("/", a.DeletePipelineItem)
		r.Post("/generate", a.GeneratePipelineContent)
		r.Post("/advance", a.AdvancePipelineItem)
	})

	return r
}

// ListPipelineItems handles GET /api/pipeline?status=&type=
func (a *API) ListPipelineItems(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	typeFilter := r.URL.Query().Get("type")

	items, err := a.queries.ListPipelineItems(r.Context(), db.ListPipelineItemsParams{
		Column1: statusFilter,
		Column2: typeFilter,
	})
	if err != nil {
		http.Error(w, "failed to list pipeline items: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if items == nil {
		items = []db.PipelineItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// GetPipelineItem handles GET /api/pipeline/{id}
func (a *API) GetPipelineItem(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid pipeline item ID", http.StatusBadRequest)
		return
	}

	item, err := a.queries.GetPipelineItem(r.Context(), id)
	if err != nil {
		http.Error(w, "pipeline item not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

// CreatePipelineItemRequest is the request body for creating a pipeline item.
type CreatePipelineItemRequest struct {
	Title   string `json:"title"`
	Type    string `json:"type"`
	Outline string `json:"outline"`
}

// CreatePipelineItem handles POST /api/pipeline
func (a *API) CreatePipelineItem(w http.ResponseWriter, r *http.Request) {
	var req CreatePipelineItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	validTypes := map[string]bool{"blog": true, "social": true, "seo": true, "newsletter": true}
	if req.Type == "" || !validTypes[req.Type] {
		http.Error(w, "type is required and must be one of: blog, social, seo, newsletter", http.StatusBadRequest)
		return
	}

	// Store outline in metadata
	metadata := map[string]any{}
	if req.Outline != "" {
		metadata["outline"] = req.Outline
	}
	metadataJSON, _ := json.Marshal(metadata)

	item, err := a.queries.CreatePipelineItem(r.Context(), db.CreatePipelineItemParams{
		Type:     req.Type,
		Title:    req.Title,
		Status:   "draft",
		Content:  pgtype.Text{},
		Metadata: metadataJSON,
	})
	if err != nil {
		http.Error(w, "failed to create pipeline item: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

// UpdatePipelineItemRequest is the request body for updating a pipeline item.
type UpdatePipelineItemRequest struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	Content string `json:"content"`
	Outline string `json:"outline"`
}

// UpdatePipelineItem handles PUT /api/pipeline/{id}
func (a *API) UpdatePipelineItem(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid pipeline item ID", http.StatusBadRequest)
		return
	}

	var req UpdatePipelineItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Get existing item to preserve fields
	existing, err := a.queries.GetPipelineItem(r.Context(), id)
	if err != nil {
		http.Error(w, "pipeline item not found", http.StatusNotFound)
		return
	}

	title := req.Title
	if title == "" {
		title = existing.Title
	}

	status := req.Status
	if status == "" {
		status = existing.Status
	}

	content := existing.Content
	if req.Content != "" {
		content = pgtypeText(req.Content)
	}

	// Merge metadata with existing to handle outline
	var metadata map[string]any
	if existing.Metadata != nil {
		json.Unmarshal(existing.Metadata, &metadata)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if req.Outline != "" {
		metadata["outline"] = req.Outline
	}
	metadataJSON, _ := json.Marshal(metadata)

	item, err := a.queries.UpdatePipelineItem(r.Context(), db.UpdatePipelineItemParams{
		ID:       id,
		Title:    title,
		Status:   status,
		Content:  content,
		Metadata: metadataJSON,
	})
	if err != nil {
		http.Error(w, "failed to update pipeline item: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

// DeletePipelineItem handles DELETE /api/pipeline/{id}
func (a *API) DeletePipelineItem(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid pipeline item ID", http.StatusBadRequest)
		return
	}

	if err := a.queries.DeletePipelineItem(r.Context(), id); err != nil {
		http.Error(w, "failed to delete pipeline item", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GeneratePipelineContent handles POST /api/pipeline/{id}/generate
func (a *API) GeneratePipelineContent(w http.ResponseWriter, r *http.Request) {
	// Use a detached context with generous timeout — the local LLM can be slow
	// and Chi's middleware.Timeout(60s) would otherwise expire the request context.
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid pipeline item ID", http.StatusBadRequest)
		return
	}

	item, err := a.queries.GetPipelineItem(ctx, id)
	if err != nil {
		http.Error(w, "pipeline item not found", http.StatusNotFound)
		return
	}

	// Build system prompt based on type
	promptMap := map[string]string{
		"blog":       "Write a blog post",
		"social":     "Write social media posts",
		"seo":        "Write SEO-optimized content",
		"newsletter": "Write a newsletter section",
	}
	systemPrompt, ok := promptMap[item.Type]
	if !ok {
		systemPrompt = "Write content"
	}

	// Extract outline from metadata
	outline := ""
	if item.Metadata != nil {
		var meta map[string]any
		json.Unmarshal(item.Metadata, &meta)
		if o, ok := meta["outline"].(string); ok {
			outline = o
		}
	}

	userContent := fmt.Sprintf("Title: %s", item.Title)
	if outline != "" {
		userContent += fmt.Sprintf("\n\nOutline:\n%s", outline)
	}

	// Call LiteLLM for content generation
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
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
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

	generatedContent := chatResp.Choices[0].Message.Content

	// Save content back to the pipeline item
	_, err = a.queries.UpdatePipelineItem(ctx, db.UpdatePipelineItemParams{
		ID:       id,
		Title:    item.Title,
		Status:   item.Status,
		Content:  pgtypeText(generatedContent),
		Metadata: item.Metadata,
	})
	if err != nil {
		http.Error(w, "failed to save generated content: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result := map[string]string{"content": generatedContent}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// AdvancePipelineItem handles POST /api/pipeline/{id}/advance
func (a *API) AdvancePipelineItem(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid pipeline item ID", http.StatusBadRequest)
		return
	}

	item, err := a.queries.GetPipelineItem(r.Context(), id)
	if err != nil {
		http.Error(w, "pipeline item not found", http.StatusNotFound)
		return
	}

	// Status progression: draft → ai_review → human_review → published
	nextStatus := map[string]string{
		"draft":        "ai_review",
		"ai_review":    "human_review",
		"human_review": "published",
	}

	next, ok := nextStatus[item.Status]
	if !ok {
		validStatuses := strings.Join([]string{"draft", "ai_review", "human_review", "published"}, ", ")
		http.Error(w, fmt.Sprintf("cannot advance from status '%s'. Valid statuses: %s", item.Status, validStatuses), http.StatusBadRequest)
		return
	}

	updated, err := a.queries.UpdatePipelineItem(r.Context(), db.UpdatePipelineItemParams{
		ID:       id,
		Title:    item.Title,
		Status:   next,
		Content:  item.Content,
		Metadata: item.Metadata,
	})
	if err != nil {
		http.Error(w, "failed to advance pipeline item: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}
