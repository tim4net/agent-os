package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HermesHarness implements the Harness interface for Hermes/Roux agents.
type HermesHarness struct {
	baseURL    string
	litellmURL string
	apiKey     string
	httpClient *http.Client
}

func NewHermesHarness() Harness {
	return &HermesHarness{
		// No global Timeout — SSE streams from LLM can take minutes.
		// Per-request context deadlines handle non-streaming calls.
		httpClient: &http.Client{},
	}
}

func (h *HermesHarness) Name() string { return "hermes" }

func (h *HermesHarness) Init(config map[string]any) error {
	baseURL, ok := config["base_url"].(string)
	if !ok || baseURL == "" {
		return fmt.Errorf("hermes harness: base_url is required")
	}
	h.baseURL = baseURL

	// litellm_url is optional for chat but needed for models
	if v, ok := config["litellm_url"].(string); ok {
		h.litellmURL = v
	}
	// api_key for Bearer auth
	if v, ok := config["api_key"].(string); ok {
		h.apiKey = v
	}
	return nil
}

func (h *HermesHarness) Health(ctx context.Context) (*HealthStatus, error) {
	url := h.baseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("hermes health: create request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return &HealthStatus{Status: "offline"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &HealthStatus{Status: "degraded"}, nil
	}

	var result struct {
		Status   string `json:"status"`
		Platform string `json:"platform"`
		Version  string `json:"version,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &HealthStatus{Status: "online"}, nil
	}

	return &HealthStatus{
		Status:  "online",
		Version: result.Version,
	}, nil
}

func (h *HermesHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	url := h.baseURL + "/v1/chat/completions"

	// Build OpenAI-compatible request
	reqMessages := make([]map[string]string, 0, len(messages)+1)
	if opts.SystemPrompt != "" {
		reqMessages = append(reqMessages, map[string]string{
			"role":    "system",
			"content": opts.SystemPrompt,
		})
	}
	for _, m := range messages {
		reqMessages = append(reqMessages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	body := map[string]any{
		"model":    opts.Model,
		"messages": reqMessages,
		"stream":   true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("hermes chat: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("hermes chat: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hermes chat: do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("hermes chat: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan ChatChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// Only process data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				ch <- ChatChunk{Done: true}
				return
			}

			// Parse the SSE chunk (OpenAI format)
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				ch <- ChatChunk{Error: fmt.Errorf("parse chunk: %w", err)}
				return
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					ch <- ChatChunk{Content: choice.Delta.Content}
				}
				if choice.FinishReason != nil && *choice.FinishReason == "stop" {
					ch <- ChatChunk{Done: true}
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- ChatChunk{Error: fmt.Errorf("read stream: %w", err)}
		}
	}()

	return ch, nil
}

func (h *HermesHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if h.litellmURL == "" {
		return nil, ErrNotSupported
	}

	url := h.litellmURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("hermes models: create request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hermes models: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hermes models: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("hermes models: decode: %w", err)
	}

	return result.Data, nil
}

// hermesCommands defines the slash commands supported by the Hermes agent.
var hermesCommands = []Command{
	{Command: "/new", Description: "Start a new conversation"},
	{Command: "/compact", Description: "Summarize and compact conversation history"},
	{Command: "/clear", Description: "Clear all messages in current conversation"},
}

func (h *HermesHarness) Commands() []Command {
	return hermesCommands
}

func (h *HermesHarness) Close() error {
	h.httpClient.CloseIdleConnections()
	return nil
}
