package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FALProvider implements StudioProvider using the FAL.ai queue API.
type FALProvider struct {
	apiKey string
}

// NewFALProvider creates a new FALProvider.
func NewFALProvider(apiKey string) *FALProvider {
	return &FALProvider{apiKey: apiKey}
}

// fal request/response types
type falRequest struct {
	Prompt    string `json:"prompt"`
	ImageSize string `json:"image_size"`
}

type falQueueResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

type falResultResponse struct {
	Data struct {
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	} `json:"data"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Generate calls the FAL.ai queue API to generate an image.
func (p *FALProvider) Generate(ctx context.Context, prompt string, genType string, model string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("FAL_KEY not configured")
	}

	if model == "" {
		model = "flux/schnell"
	}

	reqBody := falRequest{
		Prompt:    prompt,
		ImageSize: "landscape_16_9",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://queue.fal.run/fal-ai/%s", model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fal API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fal API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Parse the queue response
	var queueResp falQueueResponse
	if err := json.Unmarshal(respBody, &queueResp); err != nil {
		return "", fmt.Errorf("parse queue response: %w", err)
	}

	// Poll for the result
	resultURL := fmt.Sprintf("https://queue.fal.run/fal-ai/%s/requests/%s", model, queueResp.RequestID)
	return p.pollForResult(ctx, resultURL)
}

// pollForResult polls the FAL.ai status endpoint until the image is ready.
func (p *FALProvider) pollForResult(ctx context.Context, resultURL string) (string, error) {
	const maxAttempts = 60
	const pollInterval = 2 * time.Second

	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
		if err != nil {
			return "", fmt.Errorf("create poll request: %w", err)
		}

		req.Header.Set("Authorization", "Key "+p.apiKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("fal poll request failed: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read poll response: %w", err)
		}

		var result falResultResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("parse poll response: %w", err)
		}

		if result.Error != nil {
			return "", fmt.Errorf("fal error: %s", result.Error.Message)
		}

		if result.Status == "COMPLETED" {
			if len(result.Data.Images) > 0 && result.Data.Images[0].URL != "" {
				return result.Data.Images[0].URL, nil
			}
			return "", fmt.Errorf("fal completed but no image URL in response")
		}

		// Continue polling for IN_QUEUE or IN_PROGRESS
	}

	return "", fmt.Errorf("fal: timed out waiting for result")
}
