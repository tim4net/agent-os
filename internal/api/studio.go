package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// StudioProvider is the interface for media generation backends.
type StudioProvider interface {
	Generate(ctx context.Context, prompt string, genType string, model string) (url string, err error)
}

// XAIProvider implements StudioProvider using the xAI image generation API.
type XAIProvider struct {
	apiKey string
}

// NewXAIProvider creates a new XAIProvider.
func NewXAIProvider(apiKey string) *XAIProvider {
	return &XAIProvider{apiKey: apiKey}
}

// xAI request/response types
type xaiImageRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	N      int    `json:"n"`
}

type xaiImageResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
}

// Generate calls the xAI API to generate an image.
func (p *XAIProvider) Generate(ctx context.Context, prompt string, genType string, model string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("XAI_API_KEY not configured")
	}

	if model == "" {
		model = "grok-2-image"
	}

	reqBody := xaiImageRequest{
		Prompt: prompt,
		Model:  model,
		N:      1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.x.ai/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("xAI API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("xAI API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var xaiResp xaiImageResponse
	if err := json.Unmarshal(respBody, &xaiResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(xaiResp.Data) == 0 || xaiResp.Data[0].URL == "" {
		return "", fmt.Errorf("no image URL in response")
	}

	return xaiResp.Data[0].URL, nil
}

// StudioAPI holds dependencies for studio/generation endpoints.
type StudioAPI struct {
	queries       *db.Queries
	artifactsPath string
	provider      StudioProvider
}

// NewStudioAPI creates a new StudioAPI.
func NewStudioAPI(queries *db.Queries, artifactsPath string, provider StudioProvider) *StudioAPI {
	return &StudioAPI{
		queries:       queries,
		artifactsPath: artifactsPath,
		provider:      provider,
	}
}

// GenerateRequest is the JSON body for the generate endpoint.
type GenerateRequest struct {
	Prompt string `json:"prompt"`
	Type   string `json:"type"` // "image", "video", "audio"
	Model  string `json:"model"`
}

// StudioRoutes returns a Chi router with studio routes.
func (s *StudioAPI) StudioRoutes() http.Handler {
	r := chi.NewRouter()

	r.Post("/generate", s.Generate)
	r.Get("/generations", s.ListGenerations)

	return r
}

// Generate handles POST /api/studio/generate
func (s *StudioAPI) Generate(w http.ResponseWriter, r *http.Request) {
	var req GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	validTypes := map[string]bool{"image": true, "video": true, "audio": true}
	if !validTypes[req.Type] {
		req.Type = "image" // default
	}

	// Call provider
	resultURL, err := s.provider.Generate(r.Context(), req.Prompt, req.Type, req.Model)
	if err != nil {
		http.Error(w, "generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Download the generated file
	fileData, err := downloadFile(r.Context(), resultURL)
	if err != nil {
		// If download fails, still record the artifact with the remote URL
		logf("studio: failed to download generated file: %v", err)
	}

	// Save to disk and create artifact record
	var relativePath string
	if fileData != nil {
		ext := detectExtension(resultURL, req.Type)
		fileUUID := uuid.New().String()
		relativePath = filepath.Join("studio", fileUUID+ext)
		fullPath := filepath.Join(s.artifactsPath, relativePath)

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err == nil {
			os.WriteFile(fullPath, fileData, 0644)
		}
	}

	// Create DB record
	mimeType := typeToMime(req.Type)
	title := fmt.Sprintf("Generated %s: %s", req.Type, truncate(req.Prompt, 50))

	metadata := map[string]any{
		"prompt":    req.Prompt,
		"model":     req.Model,
		"source_url": resultURL,
	}
	metaBytes, _ := json.Marshal(metadata)

	artifact, err := s.queries.CreateArtifact(r.Context(), db.CreateArtifactParams{
		AgentID:     pgtype.UUID{Valid: false},
		Type:        req.Type,
		Title:       pgtype.Text{String: title, Valid: true},
		Description: pgtype.Text{String: req.Prompt, Valid: true},
		FilePath:    pgtype.Text{String: relativePath, Valid: relativePath != ""},
		MimeType:    pgtype.Text{String: mimeType, Valid: mimeType != ""},
		Metadata:    metaBytes,
	})
	if err != nil {
		http.Error(w, "failed to save artifact: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(artifact)
}

// ListGenerations handles GET /api/studio/generations?type=image&limit=20&offset=0
func (s *StudioAPI) ListGenerations(w http.ResponseWriter, r *http.Request) {
	genType := r.URL.Query().Get("type")
	if genType == "" {
		genType = "image"
	}

	limit := int32(20)
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 {
			limit = int32(n)
		}
	}

	offset := int32(0)
	if o := r.URL.Query().Get("offset"); o != "" {
		var n int
		if _, err := fmt.Sscanf(o, "%d", &n); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	artifacts, err := s.queries.ListArtifacts(r.Context(), db.ListArtifactsParams{
		Column1: genType,
		Column2: pgtype.UUID{Valid: false},
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		http.Error(w, "failed to list generations", http.StatusInternalServerError)
		return
	}

	if artifacts == nil {
		artifacts = []db.Artifact{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifacts)
}

// downloadFile downloads a file from a URL and returns its bytes.
func downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// detectExtension returns a file extension based on URL or type.
func detectExtension(url string, genType string) string {
	// Try to get extension from URL
	u := strings.ToLower(url)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".mp4", ".mp3"} {
		if strings.Contains(u, ext) {
			return ext
		}
	}

	// Fallback based on type
	switch genType {
	case "image":
		return ".png"
	case "video":
		return ".mp4"
	case "audio":
		return ".mp3"
	default:
		return ".bin"
	}
}

// typeToMime returns a MIME type for the generation type.
func typeToMime(genType string) string {
	switch genType {
	case "image":
		return "image/png"
	case "video":
		return "video/mp4"
	case "audio":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}
