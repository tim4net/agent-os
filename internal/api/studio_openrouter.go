package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenRouterProvider implements StudioProvider using OpenRouter's chat completions API
// with image-generation models like google/gemini-2.5-flash-image.
type OpenRouterProvider struct {
	apiKey string
}

// NewOpenRouterProvider creates a new OpenRouterProvider.
func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
	return &OpenRouterProvider{apiKey: apiKey}
}

// openRouter request/response types
type openRouterRequest struct {
	Model             string              `json:"model"`
	Messages          []openRouterMessage `json:"messages"`
	ResponseModalities []string           `json:"response_modalities,omitempty"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// openRouterContentPart handles both array and string content responses.
type openRouterContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	InlineData *struct {
		MimeType string `json:"mime_type"`
		Data     string `json:"data"`
	} `json:"inline_data,omitempty"`
}

// Generate calls the OpenRouter API to generate an image via chat completions.
func (p *OpenRouterProvider) Generate(ctx context.Context, prompt string, genType string, model string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY not configured")
	}

	if model == "" {
		model = "google/gemini-2.5-flash-image"
	}

	reqBody := openRouterRequest{
		Model: model,
		Messages: []openRouterMessage{
			{Role: "user", Content: prompt},
		},
		ResponseModalities: []string{"IMAGE"},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openrouter API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if orResp.Error != nil {
		return "", fmt.Errorf("openrouter error: %s", orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in openrouter response")
	}

	// Content can be a string or an array of parts — handle both
	rawContent := orResp.Choices[0].Message.Content

	// Try array of content parts first
	var parts []openRouterContentPart
	if err := json.Unmarshal(rawContent, &parts); err == nil {
		for _, part := range parts {
			if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
				return part.ImageURL.URL, nil
			}
			if part.InlineData != nil && part.InlineData.Data != "" {
				return "data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data, nil
			}
		}
	}

	// If content is a plain string, it might be a URL or base64
	var contentStr string
	if err := json.Unmarshal(rawContent, &contentStr); err == nil {
		// Check if it looks like a URL
		if strings.HasPrefix(contentStr, "http") {
			return contentStr, nil
		}
		// Check if it's base64 image data
		if strings.HasPrefix(contentStr, "data:") {
			return contentStr, nil
		}
		return "", fmt.Errorf("openrouter returned text instead of image: %s", truncate(contentStr, 100))
	}

	return "", fmt.Errorf("no image in openrouter response")
}
