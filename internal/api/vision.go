package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"encoding/base64"
	"strings"
)

// VisionAnalyzeRequest is the JSON body for POST /api/vision/analyze.
type VisionAnalyzeRequest struct {
	ImageURL string `json:"image_url"`
	Prompt   string `json:"prompt"`
	Model    string `json:"model,omitempty"`
}

// VisionAnalyzeResponse is the JSON response for the vision analyze endpoint.
type VisionAnalyzeResponse struct {
	Analysis string `json:"analysis"`
	Model    string `json:"model"`
}

// AnalyzeVision handles POST /api/vision/analyze.
// It accepts an image URL and prompt, calls the z.ai chat completions API
// with multimodal content (OpenAI format), and returns the model's text analysis.
//
// NOTE: z.ai glm-4.6v-flash cannot fetch external image URLs — it requires
// base64-encoded images. This handler fetches the URL, converts to base64,
// and forwards to z.ai.
func (a *API) AnalyzeVision(w http.ResponseWriter, r *http.Request) {
	var req VisionAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ImageURL == "" {
		http.Error(w, "image_url is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	if a.zaiAPIKey == "" {
		http.Error(w, "ZAI_API_KEY not configured", http.StatusServiceUnavailable)
		return
	}

	model := req.Model
	if model == "" {
		model = "glm-4.6v-flash"
	}

	// Fetch the image and convert to base64.
	// z.ai vision models cannot fetch external URLs — they need data URIs.
	imageDataURL, err := a.fetchImageAsBase64(r, req.ImageURL)
	if err != nil {
		slog.Error("vision: failed to fetch image", "url", req.ImageURL, "error", err)
		http.Error(w, fmt.Sprintf("failed to fetch image: %v", err), http.StatusBadRequest)
		return
	}

	// Build OpenAI-compatible multimodal chat completion request.
	// NOTE: z.ai vision models reject max_tokens with error 1210,
	// so we omit it entirely.
	chatReq := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": req.Prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": imageDataURL,
						},
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(chatReq)
	if err != nil {
		http.Error(w, "failed to marshal request", http.StatusInternalServerError)
		return
	}

	// z.ai vision models must use the OpenAI-compatible /api/paas/v4 endpoint,
	// NOT /api/coding/paas/v4 which is for text-only coding models.
	upstreamURL := "https://api.z.ai/api/paas/v4/chat/completions"
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}

	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+a.zaiAPIKey)

	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		slog.Error("vision: z.ai request failed", "error", err)
		http.Error(w, "vision analysis request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("vision: failed to read z.ai response", "error", err)
		http.Error(w, "failed to read upstream response", http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("vision: z.ai returned non-200", "status", resp.StatusCode, "body", string(respBody))
		http.Error(w, fmt.Sprintf("vision analysis failed: upstream status %d", resp.StatusCode), resp.StatusCode)
		return
	}

	// Parse the OpenAI-compatible chat completion response
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		slog.Error("vision: failed to parse z.ai response", "error", err)
		http.Error(w, "failed to parse vision response", http.StatusInternalServerError)
		return
	}

	if len(chatResp.Choices) == 0 {
		http.Error(w, "no analysis returned from model", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(VisionAnalyzeResponse{
		Analysis: chatResp.Choices[0].Message.Content,
		Model:    model,
	})
}

// fetchImageAsBase64 downloads the image from the given URL and returns it
// as a data URI (data:image/...;base64,...). If the URL is already a data URI,
// it is returned as-is.
func (a *API) fetchImageAsBase64(r *http.Request, imageURL string) (string, error) {
	// Already a data URI — return as-is
	if strings.HasPrefix(imageURL, "data:") {
		return imageURL, nil
	}

	// Fetch the image
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating image request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image fetch returned status %d", resp.StatusCode)
	}

	// Read image body (limit to 20MB)
	imgBody, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading image body: %w", err)
	}

	// Detect content type from response header
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png" // default
	}

	// Build data URI
	b64 := base64.StdEncoding.EncodeToString(imgBody)
	return "data:" + contentType + ";base64," + b64, nil
}
