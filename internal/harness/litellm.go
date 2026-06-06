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
	"time"
)

// LiteLLMHarness implements the Harness interface for the LiteLLM model router.
type LiteLLMHarness struct {
	baseURL    string
	httpClient *http.Client
}

func NewLiteLLMHarness() Harness {
	return &LiteLLMHarness{
		// No global Timeout — SSE streams from LLM can take minutes.
		// Per-request context deadlines handle non-streaming calls.
		httpClient: &http.Client{},
	}
}

func (l *LiteLLMHarness) Name() string { return "litellm" }

func (l *LiteLLMHarness) Init(config map[string]any) error {
	baseURL, ok := config["base_url"].(string)
	if !ok || baseURL == "" {
		return fmt.Errorf("litellm harness: base_url is required")
	}
	l.baseURL = baseURL
	return nil
}

func (l *LiteLLMHarness) Health(ctx context.Context) (*HealthStatus, error) {
	models, err := l.ListModels(ctx)
	if err != nil {
		return &HealthStatus{Status: "offline"}, nil
	}

	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.ID)
	}

	return &HealthStatus{
		Status: "online",
		Models: names,
	}, nil
}

// VersionInfo reports the LiteLLM proxy version. LiteLLM exposes its version
// inconsistently across builds, so we try sources in order of preference and
// return the first that yields a non-empty version (each tagged with its
// provenance), falling back to "unknown":
//
//  1. GET /version            -> {"version": "..."}          (some builds)
//  2. GET /health/readiness   -> {"litellm_version": "..."}  (standard field)
//  3. GET /openapi.json       -> info.version                (FastAPI builds)
//
// Every probe degrades silently to "" on any error / non-200 / missing field;
// the whole chain is bounded by the caller's context deadline.
func (l *LiteLLMHarness) VersionInfo(ctx context.Context) (*VersionInfo, error) {
	checkedAt := time.Now().UTC()
	base := strings.TrimRight(l.baseURL, "/")

	if v := l.probeJSONStringField(ctx, base+"/version", "version"); v != "" {
		return &VersionInfo{Current: v, Source: "http", CheckedAt: checkedAt}, nil
	}
	if v := l.probeJSONStringField(ctx, base+"/health/readiness", "litellm_version"); v != "" {
		return &VersionInfo{Current: v, Source: "health", CheckedAt: checkedAt}, nil
	}
	if v := l.probeOpenAPIVersion(ctx, base+"/openapi.json"); v != "" {
		return &VersionInfo{Current: v, Source: "openapi", CheckedAt: checkedAt}, nil
	}

	return &VersionInfo{Current: "", Source: "unknown", CheckedAt: checkedAt}, nil
}

// probeJSONStringField does GET url and returns the string value of the named
// top-level JSON field. Returns "" on any error, non-200, decode failure, or if
// the field is absent or not a string.
func (l *LiteLLMHarness) probeJSONStringField(ctx context.Context, url, field string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// probeOpenAPIVersion does GET url (the FastAPI /openapi.json) and returns
// info.version. The spec can be ~1MB; that is acceptable for an on-demand
// version probe (not a hot path) and is bounded by the caller's context
// deadline. Returns "" on any error / non-200 / decode failure.
func (l *LiteLLMHarness) probeOpenAPIVersion(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var spec struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		return ""
	}
	return spec.Info.Version
}

func (l *LiteLLMHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	url := l.baseURL + "/v1/chat/completions"

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
		return nil, fmt.Errorf("litellm chat: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("litellm chat: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm chat: do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("litellm chat: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan ChatChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			if data == "[DONE]" {
				ch <- ChatChunk{Done: true}
				return
			}

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
			return
		}

		// The stream ended without an explicit [DONE] or finish_reason:"stop"
		// terminal — e.g. finish_reason "length" (max_tokens reached),
		// "content_filter", or a clean EOF / dropped upstream connection. Every
		// in-loop terminal path returns, so reaching here means no Done was sent.
		// Emit a synthetic Done so the consumer persists whatever content
		// streamed instead of silently discarding it (and, for a brand-new
		// conversation, rolling back / deleting the whole thread).
		ch <- ChatChunk{Done: true}
	}()

	return ch, nil
}

func (l *LiteLLMHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := l.baseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("litellm models: create request: %w", err)
	}

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm models: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("litellm models: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("litellm models: decode: %w", err)
	}

	return result.Data, nil
}

func (l *LiteLLMHarness) Commands() []Command { return nil }

func (l *LiteLLMHarness) Close() error {
	l.httpClient.CloseIdleConnections()
	return nil
}
