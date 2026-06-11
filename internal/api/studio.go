package api
import (
	"context"
	b64 "encoding/base64"
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

// ProviderInfo describes a registered studio provider.
type ProviderInfo struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`         // "image", "video", "audio"
	Models      []string `json:"models"`
	RequiresKey bool     `json:"requires_key"`
	Available   bool     `json:"available"`
}

// StudioAPI holds dependencies for studio/generation endpoints.
type StudioAPI struct {
	queries       *db.Queries
	artifactsPath string
	providers     map[string]StudioProvider
	providerInfo  map[string]ProviderInfo
}

// NewStudioAPI creates a new StudioAPI with multiple providers.
// The keys map contains API keys for providers that need them: "xai", "openrouter", "gemini", "fal".
// Providers without a key are still registered but marked unavailable.
func NewStudioAPI(queries *db.Queries, artifactsPath string, keys map[string]string) *StudioAPI {
	s := &StudioAPI{
		queries:       queries,
		artifactsPath: artifactsPath,
		providers:     make(map[string]StudioProvider),
		providerInfo:  make(map[string]ProviderInfo),
	}

	// Provider 1: xAI
	// NOTE: xAI has the API key but team has no billing credits — returns 403.
	xaiKey := keys["xai"]
	s.providers["xai"] = NewXAIProvider(xaiKey)
	s.providerInfo["xai"] = ProviderInfo{
		Name:        "xai",
		Type:        "image",
		Models:      []string{"grok-2-image"},
		RequiresKey: true,
		Available:   false, // No billing credits on team account
	}

	// Provider 2: Pollinations (always available, no key needed)
	s.providers["pollinations"] = NewPollinationsProvider()
	s.providerInfo["pollinations"] = ProviderInfo{
		Name:        "pollinations",
		Type:        "image",
		Models:      []string{"flux"},
		RequiresKey: false,
		Available:   true,
	}

	// Provider 3: OpenRouter
	// NOTE: OpenRouter's image models (gemini-2.5-flash-image, gpt-5-image-mini)
	// don't actually return images through chat completions — they return text.
	// Kept in code but marked unavailable until OpenRouter adds proper image support.
	orKey := keys["openrouter"]
	s.providers["openrouter"] = NewOpenRouterProvider(orKey)
	s.providerInfo["openrouter"] = ProviderInfo{
		Name:        "openrouter",
		Type:        "image",
		Models:      []string{"google/gemini-2.5-flash-image", "openai/gpt-5-image-mini"},
		RequiresKey: true,
		Available:   false, // OpenRouter doesn't actually return images via chat completions
	}

	// Provider 4: Google Gemini
	geminiKey := keys["gemini"]
	s.providers["gemini"] = NewGeminiProvider(geminiKey, artifactsPath)
	s.providerInfo["gemini"] = ProviderInfo{
		Name:        "gemini",
		Type:        "image",
		Models:      []string{"gemini-2.5-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image-preview"},
		RequiresKey: true,
		Available:   geminiKey != "",
	}

	// Provider 5: FAL.ai
	falKey := keys["fal"]
	s.providers["fal"] = NewFALProvider(falKey)
	s.providerInfo["fal"] = ProviderInfo{
		Name:        "fal",
		Type:        "image",
		Models:      []string{"flux/schnell", "flux/dev", "flux-2-klein"},
		RequiresKey: true,
		Available:   falKey != "",
	}

	return s
}

// GenerateRequest is the JSON body for the generate endpoint.
type GenerateRequest struct {
	Prompt   string `json:"prompt"`
	Type     string `json:"type"`     // "image", "video", "audio"
	Model    string `json:"model"`
	Provider string `json:"provider"`
	AgentID  string `json:"agent_id"` // optional: associate artifact with an agent
}

// StudioRoutes returns a Chi router with studio routes.
func (s *StudioAPI) StudioRoutes() http.Handler {
	r := chi.NewRouter()

	r.Post("/generate", s.Generate)
	r.Get("/generations", s.ListGenerations)
	r.Get("/providers", s.ListProviders)

	return r
}

// ListProviders handles GET /api/studio/providers
func (s *StudioAPI) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers := make([]ProviderInfo, 0, len(s.providerInfo))
	// Return in a deterministic order
	for _, name := range []string{"xai", "pollinations", "openrouter", "gemini", "fal"} {
		if info, ok := s.providerInfo[name]; ok {
			providers = append(providers, info)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(providers)
}

// firstAvailableProvider returns the name of the first available provider.
func (s *StudioAPI) firstAvailableProvider() string {
	// Prefer pollinations since it's always available and free
	for _, name := range []string{"pollinations", "xai", "openrouter", "gemini", "fal"} {
		if info, ok := s.providerInfo[name]; ok && info.Available {
			return name
		}
	}
	return ""
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

	// Determine which provider to use
	providerName := req.Provider
	if providerName == "" {
		providerName = s.firstAvailableProvider()
	}

	provider, ok := s.providers[providerName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown provider: %s", providerName), http.StatusBadRequest)
		return
	}

	info, infoOk := s.providerInfo[providerName]
	if infoOk && !info.Available {
		http.Error(w, fmt.Sprintf("provider %s is not available (missing API key)", providerName), http.StatusBadRequest)
		return
	}

	// Call provider
	resultURL, err := provider.Generate(r.Context(), req.Prompt, req.Type, req.Model)
	if err != nil {
		http.Error(w, "generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Save to disk and create artifact record
	var relativePath string

	if strings.HasPrefix(resultURL, "file://") {
		// Provider already saved the file locally (e.g., Gemini with base64)
		relativePath = strings.TrimPrefix(resultURL, "file://")
	} else if strings.HasPrefix(resultURL, "data:") {
		// Provider returned base64 data URL (e.g., OpenRouter inline_data)
		fileData, mime, err := decodeDataURL(resultURL)
		if err != nil {
			logf("studio: failed to decode data URL: %v", err)
		}
		if fileData != nil {
			ext := extensionFromMime(mime)
			if ext == "" {
				ext = detectExtension(resultURL, req.Type)
			}
			fileUUID := uuid.New().String()
			relativePath = filepath.Join("studio", fileUUID+ext)
			fullPath := filepath.Join(s.artifactsPath, relativePath)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err == nil {
				os.WriteFile(fullPath, fileData, 0644)
			}
		}
	} else {
		// Download the generated file from the remote URL
		fileData, err := downloadFile(r.Context(), resultURL)
		if err != nil {
			logf("studio: failed to download generated file: %v", err)
		}

		if fileData != nil {
			ext := detectExtension(resultURL, req.Type)
			fileUUID := uuid.New().String()
			relativePath = filepath.Join("studio", fileUUID+ext)
			fullPath := filepath.Join(s.artifactsPath, relativePath)

			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err == nil {
				os.WriteFile(fullPath, fileData, 0644)
			}
		}
	}

	// Create DB record
	mimeType := typeToMime(req.Type)
	title := fmt.Sprintf("Generated %s: %s", req.Type, truncate(req.Prompt, 50))

	metadata := map[string]any{
		"prompt":     req.Prompt,
		"model":      req.Model,
		"provider":   providerName,
		"source_url": resultURL,
	}
	metaBytes, _ := json.Marshal(metadata)

	// Parse optional agent_id
	var agentID pgtype.UUID
	if req.AgentID != "" {
		if err := agentID.Scan(req.AgentID); err != nil {
			agentID = pgtype.UUID{Valid: false}
		}
	}

	artifact, err := s.queries.CreateArtifact(r.Context(), db.CreateArtifactParams{
		AgentID:     agentID,
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
	json.NewEncoder(w).Encode(artifactToStudioGeneration(artifact))
}

// ListGenerations handles GET /api/studio/generations?type=image&limit=20&offset=0
func (s *StudioAPI) ListGenerations(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

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
		OwnerID: ownerID,
		Column2: genType,
		Column3: pgtype.UUID{Valid: false},
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

	// Convert to StudioGeneration DTOs for the frontend
	generations := make([]studioGenerationResponse, len(artifacts))
	for i, a := range artifacts {
		generations[i] = artifactToStudioGeneration(a)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(generations)
}

// studioGenerationResponse matches the frontend's StudioGeneration interface.
type studioGenerationResponse struct {
	ID        string `json:"id"`
	Prompt    string `json:"prompt"`
	Type      string `json:"type"`
	Model     string `json:"model"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

// artifactToStudioGeneration converts a db.Artifact to a StudioGeneration DTO.
func artifactToStudioGeneration(a db.Artifact) studioGenerationResponse {
	prompt := ""
	if a.Description.Valid {
		prompt = a.Description.String
	} else if a.Title.Valid {
		prompt = a.Title.String
	}

	// Extract model from metadata
	model := ""
	var meta map[string]string
	if len(a.Metadata) > 0 {
		_ = json.Unmarshal(a.Metadata, &meta)
		model = meta["model"]
	}

	createdAt := ""
	if a.CreatedAt.Valid {
		createdAt = a.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}

	return studioGenerationResponse{
		ID:        a.ID.String(),
		Prompt:    prompt,
		Type:      a.Type,
		Model:     model,
		URL:       fmt.Sprintf("/api/artifacts/%s/file", a.ID.String()),
		CreatedAt: createdAt,
	}
}

// --- Utility functions ---

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

// decodeDataURL parses a data URL and returns the decoded bytes and MIME type.
func decodeDataURL(dataURL string) ([]byte, string, error) {
	// data:image/png;base64,xxxxx
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, "", fmt.Errorf("not a data URL")
	}
	rest := dataURL[5:]
	// Find the comma separating metadata from data
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return nil, "", fmt.Errorf("invalid data URL: no comma")
	}
	meta := rest[:commaIdx]
	data := rest[commaIdx+1:]

	// Parse MIME type from meta (e.g., "image/png;base64")
	mime := "image/png"
	if semiIdx := strings.Index(meta, ";"); semiIdx > 0 {
		mime = meta[:semiIdx]
	} else if len(meta) > 0 {
		mime = meta
	}

	// Decode base64
	decoded, err := b64.StdEncoding.DecodeString(data)
	if err != nil {
		// Try URL-safe base64
		decoded, err = b64.URLEncoding.DecodeString(data)
		if err != nil {
			return nil, mime, fmt.Errorf("base64 decode: %w", err)
		}
	}
	return decoded, mime, nil
}

// extensionFromMime returns a file extension for a MIME type.
func extensionFromMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ""
	}
}
