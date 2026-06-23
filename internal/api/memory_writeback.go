package api

// API-side glue for the automatic memory writeback (issue #127).
//
// The core writeback logic lives in internal/service (MemoryWriteback), with an
// injected DistillerFunc so it is unit-testable without an LLM. This file
// provides the real LLM distiller (a closure over the API's LiteLLM/OpenRouter
// config) and wires the MemoryWriteback into the API so the chat handler can
// fire it after a turn completes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/service"
)

// memoryDistillPrompt instructs the LLM to extract durable knowledge — facts,
// decisions, and a short summary — from a conversation transcript, as a concise
// Markdown note. The note must be self-contained (no "as discussed" references)
// because it will be retrieved verbatim by RAG on future turns.
const memoryDistillPrompt = `You are a memory distillation assistant for an AI agent operating system.
Given a conversation transcript, extract the DURABLE knowledge an agent would need on future turns — facts, decisions, preferences, and outcomes. Ignore pleasantries, filler, and transient back-and-forth.

Produce a concise Markdown note with ONLY the sections that have content:

## Summary
One or two sentences: what was the conversation about and what was concluded?

## Key Facts
- Discrete, reusable facts (one per bullet).

## Decisions
- Any decision made or confirmed.

## Action Items
- Anything that still needs doing, with the owner if known.

Rules:
- Be factual and terse. No opinions, no hedging ("maybe", "perhaps").
- Write so a reader with no prior context can use it.
- If the conversation has no durable content, output only: "## Summary` + "`" + `\n\nNo durable knowledge extracted."`

// MemoryDistiller returns a service.DistillerFunc backed by the API's LLM
// provider (LiteLLM proxy, falling back to OpenRouter). The returned function
// is safe to call from background goroutines. If no provider is configured it
// returns an error for every call; the writeback falls back to the heuristic
// distiller, so the feedback loop stays closed.
func (a *API) MemoryDistiller() service.DistillerFunc {
	return func(ctx context.Context, transcript string) (string, error) {
		return a.distillMemory(ctx, transcript)
	}
}

// distillMemory calls the configured LLM to distill a transcript into a
// structured Markdown note.
func (a *API) distillMemory(ctx context.Context, transcript string) (string, error) {
	var url, apiKey, model string
	headers := map[string]string{}

	switch {
	case a.litellmURL != "":
		url = a.litellmURL + "/v1/chat/completions"
		model = a.llmModel
	case a.openrouterAPIKey != "":
		url = "https://openrouter.ai/api/v1/chat/completions"
		apiKey = a.openrouterAPIKey
		model = "openai/gpt-oss-120b:free"
		headers["HTTP-Referer"] = "https://agent-os.local"
		headers["X-Title"] = "Agent OS"
	default:
		return "", fmt.Errorf("no LLM provider configured for memory distillation")
	}

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": memoryDistillPrompt},
			{"role": "user", "content": transcript},
		},
		"stream":     false,
		"max_tokens": 2048,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal distill request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create distill request: %w", err)
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
		return "", fmt.Errorf("distill request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("distill request status %d (model=%s): %s", resp.StatusCode, model, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode distill response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in distill response")
	}

	note := strings.TrimSpace(result.Choices[0].Message.Content)
	// Thinking models may exhaust the token budget on chain-of-thought and
	// return empty content. Surface an error so the caller uses the heuristic
	// fallback rather than persisting an empty note.
	if note == "" {
		return "", fmt.Errorf("distill returned empty content (finish_reason=%s)", result.Choices[0].FinishReason)
	}
	return note, nil
}

// SetMemoryWriteback attaches an automatic-writeback worker to the API. Called
// once at server boot (cmd/server/main.go) after the API and its LLM config are
// known. When unset, chat turns simply skip writeback — the feature degrades to
// the pre-#127 behavior.
func (a *API) SetMemoryWriteback(wb *service.MemoryWriteback) {
	a.writeback = wb
}

// triggerMemoryWriteback fires the writeback for a finished conversation in a
// background goroutine, so it never blocks the SSE response stream. It is
// nil-safe: if no writeback is wired (e.g. in tests), it is a no-op.
func (a *API) triggerMemoryWriteback(convID, ownerID pgtype.UUID) {
	if a.writeback == nil {
		return
	}
	go func() {
		// Decouple from the request lifecycle: the SSE writer is about to
		// close. Give the distiller+persist a generous but bounded window.
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		if err := a.writeback.Writeback(ctx, convID, ownerID); err != nil {
			// A deleted conversation (rolled back after this goroutine was
			// scheduled) yields a "no rows" error — debug-level, not a warning.
			if strings.Contains(err.Error(), "no rows") {
				slog.Debug("memory-writeback: conversation no longer exists",
					"conversation_id", convID.String())
				return
			}
			slog.Warn("memory-writeback: failed",
				"conversation_id", convID.String(), "error", err)
		}
	}()
}
