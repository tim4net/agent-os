package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// summaryRequest is the JSON body for POST /api/conversations/summarize
type summaryRequest struct {
	ConversationIDs []string `json:"conversation_ids"`
}

// summaryResponse maps conversation IDs to their summaries
type summaryResponse struct {
	Summaries map[string]string `json:"summaries"`
}

// ConversationSummary handles POST /api/conversations/summarize.
// It takes a list of conversation IDs, fetches messages for each,
// and uses a fast model to generate one-line summaries.
func (a *API) ConversationSummary(w http.ResponseWriter, r *http.Request) {
	var req summaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.ConversationIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summaryResponse{Summaries: map[string]string{}})
		return
	}

	if a.litellmURL == "" && a.openrouterAPIKey == "" {
		http.Error(w, "no LLM provider configured for summaries", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	summaries := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Use a semaphore to limit concurrent LLM calls
	sem := make(chan struct{}, 3)

	for _, idStr := range req.ConversationIDs {
		wg.Add(1)
		go func(idStr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var convID pgtype.UUID
			if err := convID.Scan(idStr); err != nil {
				slog.Warn("invalid conversation ID for summary", "id", idStr)
				return
			}

			// Check if conversation already has a saved summary
			conv, err := a.queries.GetConversation(ctx, convID)
			if err != nil {
				return
			}
			if conv.Summary.Valid && conv.Summary.String != "" {
				mu.Lock()
				summaries[idStr] = conv.Summary.String
				mu.Unlock()
				return
			}

			msgs, err := a.queries.ListMessages(ctx, convID)
			if err != nil || len(msgs) == 0 {
				return
			}

			// Build a compact text representation of the conversation
			// Limit to first 6 messages to keep context small
			limit := len(msgs)
			if limit > 6 {
				limit = 6
			}

			var sb strings.Builder
			for i := 0; i < limit; i++ {
				m := msgs[i]
				role := m.Role
				if role == "user" {
					role = "User"
				} else if role == "assistant" {
					role = "Assistant"
				} else {
					continue // skip system
				}
				// Truncate long messages to 200 chars for the summary prompt
				content := m.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				sb.WriteString(fmt.Sprintf("%s: %s\n", role, content))
			}

			prompt := sb.String()
			summary, err := a.generateSummary(ctx, prompt)
			if err != nil {
				slog.Warn("failed to generate summary", "conversation_id", idStr, "error", err)
				// Fallback: use first user message truncated
				for _, m := range msgs {
					if m.Role == "user" {
						fallback := m.Content
						if len(fallback) > 60 {
							fallback = fallback[:60] + "…"
						}
						// Persist fallback summary to DB
						var summaryText pgtype.Text
						summaryText.String = fallback
						summaryText.Valid = true
						if _, saveErr := a.queries.UpdateConversationSummary(ctx, db.UpdateConversationSummaryParams{
							ID:      convID,
							Summary: summaryText,
						}); saveErr != nil {
							slog.Warn("failed to persist fallback summary", "conversation_id", idStr, "error", saveErr)
						}
						mu.Lock()
						summaries[idStr] = fallback
						mu.Unlock()
						return
					}
				}
				return
			}

			// Persist summary to DB
			var summaryText pgtype.Text
			summaryText.String = summary
			summaryText.Valid = true
			if _, saveErr := a.queries.UpdateConversationSummary(ctx, db.UpdateConversationSummaryParams{
				ID:      convID,
				Summary: summaryText,
			}); saveErr != nil {
				slog.Warn("failed to persist summary", "conversation_id", idStr, "error", saveErr)
			}

			mu.Lock()
			summaries[idStr] = summary
			mu.Unlock()
		}(idStr)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaryResponse{Summaries: summaries})
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
	// Strip any surrounding quotes
	summary = strings.Trim(summary, "\"'\u201c\u201d")
	if len(summary) > 50 {
		summary = summary[:50] + "\u2026"
	}

	return summary, nil
}
