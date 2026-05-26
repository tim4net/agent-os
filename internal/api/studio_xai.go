package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

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
