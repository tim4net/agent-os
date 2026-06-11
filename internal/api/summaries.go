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
		"max_tokens": 4096,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal summary request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
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
				Content            string `json:"content"`
				ReasoningContent   string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode summary response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in summary response")
	}

	summary := strings.TrimSpace(result.Choices[0].Message.Content)

	// Thinking models (e.g. Qwen 3.6) may consume all output tokens on
	// chain-of-thought reasoning, leaving `content` empty. Fall back to
	// parsing the reasoning_content for a candidate summary line.
	if summary == "" && result.Choices[0].FinishReason == "length" {
		summary = extractSummaryFromThinking(result.Choices[0].Message.ReasoningContent)
	}
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

// extractSummaryFromThinking attempts to extract a useful summary line from a
// thinking model's chain-of-thought output when the actual content field is
// empty (because all output tokens were consumed by reasoning).
func extractSummaryFromThinking(reasoning string) string {
	if reasoning == "" {
		return ""
	}
	lines := strings.Split(reasoning, "\n")
	// Walk backwards — the last few lines of reasoning often contain the
	// actual answer the model was converging on.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		// Skip empty lines, XML-like tags, and very short fragments
		if line == "" || strings.HasPrefix(line, "<") || len(line) < 10 {
			continue
		}
		// Take the first reasonable line we find as the summary candidate
		if len(line) > 100 {
			line = line[:100]
		}
		return line
	}
	return ""
}
