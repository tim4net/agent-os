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
)

// ConversationSummary is a DEPRECATED, decommissioned endpoint.
//
// It previously batch-generated per-conversation `summary` values via the LLM.
// That path was dead: its only frontend wrapper (`summarizeConversations` in
// web/src/api/client.ts) had zero callers, and conversation titling is fully
// handled server-side by the immediate first-message title + deferredLLMTitle +
// the hourly title_worker, all writing the `title` column. Keeping a second LLM
// label generator writing a parallel `summary` column was redundant.
//
// This stub remains ONLY so the (integrator-owned) route mount in router.go
// still compiles for the branch build; the integrator note on this PR removes
// the route mount, this stub, and the dead frontend wrapper at merge, leaving
// main fully clean. It returns 410 Gone in the interim.
func (a *API) ConversationSummary(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "conversation summarization endpoint has been removed", http.StatusGone)
}

// generateSummary produces a one-line summary using LiteLLM proxy.
// Falls back to OpenRouter direct if LiteLLM is not configured.
func (a *API) generateSummary(ctx context.Context, conversationText string) (string, error) {
	var url, apiKey string
	var model string
	var headers map[string]string

	if a.litellmURL != "" {
		// Prefer LiteLLM proxy — follows summarization instructions well
		url = a.litellmURL + "/v1/chat/completions"
		model = a.llmModel
		headers = map[string]string{}
	} else if a.openrouterAPIKey != "" {
		// Fallback to OpenRouter directly
		url = "https://openrouter.ai/api/v1/chat/completions"
		apiKey = a.openrouterAPIKey
		model = "openai/gpt-oss-120b:free"
		headers = map[string]string{
			"HTTP-Referer": "https://agent-os.local",
			"X-Title":      "Agent OS",
		}
	} else {
		return "", fmt.Errorf("no LLM provider configured for summaries")
	}

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Summarize this conversation in 5-8 words as a short topic label. Use action-oriented phrasing like \"Fix voice transcription errors\" or \"Configure LiteLLM proxy routing\". Do NOT start with \"User asks\", \"User reports\", \"User wants\", or any person's name. Just the topic. No quotes, no punctuation at the end.",
			},
			{
				"role":    "user",
				"content": conversationText,
			},
		},
		"stream":     false,
		"max_tokens": 40,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal summary request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create summary request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("summary request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("summary request status %d (model=%s): %s", resp.StatusCode, model, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode summary response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in summary response")
	}

	summary := strings.TrimSpace(result.Choices[0].Message.Content)
	// Strip any surrounding quotes, then re-trim in case the quotes wrapped
	// padding whitespace (e.g. `" hi "`).
	summary = strings.TrimSpace(strings.Trim(summary, "\"'\u201c\u201d"))

	// An LLM that returns empty/whitespace-only content must NOT be treated as a
	// valid title. Returning ("", nil) here would let callers overwrite a good
	// title (the immediate first-message title) with a blank — which then renders
	// as "New conversation" in the sidebar. Signal an error so callers keep the
	// existing title (deferredLLMTitle / title_worker skip the update) or fall
	// back to a truncated first message (the /summarize endpoint).
	if summary == "" {
		return "", fmt.Errorf("summary response was empty")
	}

	if len(summary) > 50 {
		summary = summary[:50] + "\u2026"
	}

	return summary, nil
}
