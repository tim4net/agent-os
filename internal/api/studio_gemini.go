package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// GeminiProvider implements StudioProvider using the Google Gemini API
// with image generation capabilities.
type GeminiProvider struct {
	apiKey        string
	artifactsPath string
}

// NewGeminiProvider creates a new GeminiProvider.
func NewGeminiProvider(apiKey string, artifactsPath string) *GeminiProvider {
	return &GeminiProvider{apiKey: apiKey, artifactsPath: artifactsPath}
}

// gemini request/response types
type geminiRequest struct {
	Contents         []geminiContent      `json:"contents"`
	GenerationConfig geminiGenConfig      `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenConfig struct {
	ResponseModalities []string `json:"responseModalities"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiResponsePart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type geminiResponsePart struct {
	Text       string `json:"text,omitempty"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData,omitempty"`
}

// Generate calls the Gemini API to generate an image and saves the result to disk.
func (p *GeminiProvider) Generate(ctx context.Context, prompt string, genType string, model string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not configured")
	}

	if model == "" {
		model = "gemini-2.5-flash-image"
	}

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
		GenerationConfig: geminiGenConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, p.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if gemResp.Error != nil {
		return "", fmt.Errorf("gemini error: %s", gemResp.Error.Message)
	}

	if len(gemResp.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in gemini response")
	}

	// Extract base64 image data from inline data
	for _, part := range gemResp.Candidates[0].Content.Parts {
		if part.InlineData != nil && part.InlineData.Data != "" {
			imgBytes, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return "", fmt.Errorf("decode base64 image: %w", err)
			}

			// Determine extension from mime type
			ext := ".png"
			if part.InlineData.MimeType == "image/jpeg" {
				ext = ".jpg"
			} else if part.InlineData.MimeType == "image/webp" {
				ext = ".webp"
			}

			fileUUID := uuid.New().String()
			relativePath := filepath.Join("studio", fileUUID+ext)
			fullPath := filepath.Join(p.artifactsPath, relativePath)

			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return "", fmt.Errorf("create directory: %w", err)
			}

			if err := os.WriteFile(fullPath, imgBytes, 0644); err != nil {
				return "", fmt.Errorf("write file: %w", err)
			}

			// Return a special prefix to signal the file is already saved locally
			return "file://" + relativePath, nil
		}
	}

	return "", fmt.Errorf("no image data in gemini response")
}
