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
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// SlashCommandResult is the JSON response for slash command execution.
type SlashCommandResult struct {
	Type    string `json:"type"`    // "new", "clear", "compact"
	Message string `json:"message"` // Human-readable result
	Data    any    `json:"data"`    // Optional payload
}

// HandleSlashCommand detects and executes a slash command.
func (a *API) HandleSlashCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command        string `json:"command"`
		AgentID        string `json:"agent_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	// Parse the slash command
	parts := strings.SplitN(command, " ", 2)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	var result *SlashCommandResult
	var err error

	switch cmd {
	case "/new":
		result, err = a.slashNew(r.Context(), req.AgentID, args)
	case "/clear":
		result, err = a.slashClear(r.Context(), req.ConversationID)
	case "/compact":
		result, err = a.slashCompact(r.Context(), req.ConversationID)
	default:
		http.Error(w, fmt.Sprintf("unknown command: %s", cmd), http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("slash command failed", "command", cmd, "error", err)
		http.Error(w, "command failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// slashNew creates a new conversation and returns its ID.
func (a *API) slashNew(ctx context.Context, agentIDStr string, title string) (*SlashCommandResult, error) {
	if agentIDStr == "" {
		return nil, fmt.Errorf("agent_id is required for /new")
	}

	var agentID pgtype.UUID
	if err := agentID.Scan(agentIDStr); err != nil {
		return nil, fmt.Errorf("invalid agent_id: %w", err)
	}

	var titleText pgtype.Text
	if title != "" {
		titleText.String = title
		titleText.Valid = true
	} else {
		titleText.String = "New Conversation"
		titleText.Valid = true
	}

	conv, err := a.queries.CreateConversation(ctx, db.CreateConversationParams{
		AgentID:  agentID,
		Title:    titleText,
		Metadata: []byte("{}"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	return &SlashCommandResult{
		Type:    "new",
		Message: "Created new conversation",
		Data: map[string]string{
			"conversation_id": conv.ID.String(),
		},
	}, nil
}

// slashClear deletes all messages in the current conversation.
func (a *API) slashClear(ctx context.Context, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /clear")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	// Verify conversation exists
	_, err := a.queries.GetConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("conversation not found: %w", err)
	}

	// Delete all messages in the conversation
	deleted, err := a.queries.DeleteMessagesByConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to clear messages: %w", err)
	}

	return &SlashCommandResult{
		Type:    "clear",
		Message: fmt.Sprintf("Cleared %d messages", deleted),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"removed":         deleted,
		},
	}, nil
}

// slashCompact summarizes the conversation and replaces old messages with a summary.
func (a *API) slashCompact(ctx context.Context, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /compact")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	// Get all messages in the conversation
	messages, err := a.queries.ListMessages(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}

	if len(messages) == 0 {
		return &SlashCommandResult{
			Type:    "compact",
			Message: "No messages to compact",
			Data: map[string]any{
				"conversation_id": convID.String(),
				"removed":         0,
			},
		}, nil
	}

	// Build conversation text for summarization
	var sb strings.Builder
	sb.WriteString("Summarize the following conversation concisely, preserving key facts, decisions, and context:\n\n")
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, m.Content))
	}

	// Call LiteLLM to generate a summary
	summary, err := a.callLiteLLMForSummary(ctx, sb.String())
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	// Delete old messages
	deleted, err := a.queries.DeleteMessagesByConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to clear old messages: %w", err)
	}

	// Insert the summary as a system message
	_, err = a.queries.CreateMessage(ctx, db.CreateMessageParams{
		ConversationID: convID,
		Role:           "system",
		Content:        fmt.Sprintf("[Conversation Summary]\n%s", summary),
		Metadata:       []byte(`{"type": "compact_summary"}`),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store summary: %w", err)
	}

	return &SlashCommandResult{
		Type:    "compact",
		Message: fmt.Sprintf("Compacted %d messages into summary", deleted),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"removed":         deleted,
			"summary":         summary,
		},
	}, nil
}

// callLiteLLMForSummary calls the LiteLLM endpoint to generate a summary.
func (a *API) callLiteLLMForSummary(ctx context.Context, prompt string) (string, error) {
	if a.litellmURL == "" {
		return "", fmt.Errorf("LiteLLM URL not configured")
	}

	// Build OpenAI-compatible request
	reqBody := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 1000,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(summaryCtx, "POST", a.litellmURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LiteLLM returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from LiteLLM")
	}

	return result.Choices[0].Message.Content, nil
}
