package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// SlashCommandResult is the JSON response for slash command execution.
type SlashCommandResult struct {
	Type    string `json:"type"`    // "new", "clear", "compact", "retry", "undo", "history", "title", "stop", "save", "compress"
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

	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
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
		result, err = a.slashNew(r.Context(), ownerID, req.AgentID, args)
	case "/clear":
		result, err = a.slashClear(r.Context(), ownerID, req.ConversationID)
	case "/compact":
		result, err = a.slashCompact(r.Context(), ownerID, req.ConversationID)
	case "/compress":
		// Alias for /compact
		result, err = a.slashCompact(r.Context(), ownerID, req.ConversationID)
	case "/retry":
		result, err = a.slashRetry(r.Context(), ownerID, req.AgentID, req.ConversationID)
	case "/undo":
		result, err = a.slashUndo(r.Context(), ownerID, req.ConversationID)
	case "/history":
		result, err = a.slashHistory(r.Context(), ownerID, req.ConversationID)
	case "/title":
		result, err = a.slashTitle(r.Context(), ownerID, req.ConversationID, args)
	case "/stop":
		// Stop is handled client-side (abort the fetch), but acknowledge
		result = &SlashCommandResult{
			Type:    "stop",
			Message: "Streaming stopped",
		}
	case "/save":
		result, err = a.slashSave(r.Context(), ownerID, req.ConversationID)
	default:
		// Forward unknown slash commands to the agent as chat messages.
		// The agent (Hermes, OpenClaw, etc.) handles its own slash commands.
		result = &SlashCommandResult{
			Type:    "forward",
			Message: fmt.Sprintf("Forwarding %s to agent", cmd),
			Data: map[string]any{
				"forward_text": command,
			},
		}
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
func (a *API) slashNew(ctx context.Context, ownerID pgtype.UUID, agentIDStr string, title string) (*SlashCommandResult, error) {
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
		OwnerID:  ownerID,
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
func (a *API) slashClear(ctx context.Context, ownerID pgtype.UUID, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /clear")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	// Verify conversation exists
	_, err := a.queries.GetConversation(ctx, db.GetConversationParams{ID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("conversation not found: %w", err)
	}

	// Delete all messages in the conversation
	deleted, err := a.queries.DeleteMessagesByConversation(ctx, db.DeleteMessagesByConversationParams{ConversationID: convID, OwnerID: ownerID})
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
func (a *API) slashCompact(ctx context.Context, ownerID pgtype.UUID, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /compact")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	// Get all messages in the conversation
	messages, err := a.queries.ListMessages(ctx, db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
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
	deleted, err := a.queries.DeleteMessagesByConversation(ctx, db.DeleteMessagesByConversationParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("failed to clear old messages: %w", err)
	}

	// Insert the summary as a system message
	_, err = a.queries.CreateMessage(ctx, db.CreateMessageParams{
		OwnerID:        ownerID,
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

// slashRetry re-sends the last user message (removes last assistant reply, returns the user text).
func (a *API) slashRetry(ctx context.Context, ownerID pgtype.UUID, agentIDStr, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /retry")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	// Get the last user message
	lastMsg, err := a.queries.GetLastUserMessage(ctx, db.GetLastUserMessageParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("no user message to retry: %w", err)
	}

	// Delete the last exchange (user + assistant)
	deleted, err := a.queries.DeleteLastExchange(ctx, db.DeleteLastExchangeParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("failed to remove last exchange: %w", err)
	}

	return &SlashCommandResult{
		Type:    "retry",
		Message: fmt.Sprintf("Retrying last message (removed %d messages)", deleted),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"removed":         deleted,
			"retry_message":   lastMsg.Content,
		},
	}, nil
}

// slashUndo removes the last user/assistant exchange.
func (a *API) slashUndo(ctx context.Context, ownerID pgtype.UUID, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /undo")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	deleted, err := a.queries.DeleteLastExchange(ctx, db.DeleteLastExchangeParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("failed to undo: %w", err)
	}

	return &SlashCommandResult{
		Type:    "undo",
		Message: fmt.Sprintf("Removed last exchange (%d messages)", deleted),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"removed":         deleted,
		},
	}, nil
}

// slashHistory returns the conversation messages as structured data.
func (a *API) slashHistory(ctx context.Context, ownerID pgtype.UUID, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /history")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	messages, err := a.queries.ListMessages(ctx, db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("failed to load history: %w", err)
	}

	// Format as readable text
	var sb strings.Builder
	for _, m := range messages {
		role := strings.Title(m.Role)
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("**%s**: %s\n\n", role, content))
	}

	return &SlashCommandResult{
		Type:    "history",
		Message: fmt.Sprintf("Showing %d messages", len(messages)),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"count":           len(messages),
			"history_text":    sb.String(),
		},
	}, nil
}

// slashTitle sets the conversation title.
func (a *API) slashTitle(ctx context.Context, ownerID pgtype.UUID, convIDStr, title string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /title")
	}
	if title == "" {
		return nil, fmt.Errorf("title is required (e.g. /title My Conversation)")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	conv, err := a.queries.UpdateConversation(ctx, db.UpdateConversationParams{
		ID:      convID,
		OwnerID: ownerID,
		Title:   pgtype.Text{String: title, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update title: %w", err)
	}

	return &SlashCommandResult{
		Type:    "title",
		Message: fmt.Sprintf("Title set to: %s", title),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"title":           conv.Title.String,
		},
	}, nil
}

// slashSave exports the conversation to Obsidian.
func (a *API) slashSave(ctx context.Context, ownerID pgtype.UUID, convIDStr string) (*SlashCommandResult, error) {
	if convIDStr == "" {
		return nil, fmt.Errorf("conversation_id is required for /save")
	}

	var convID pgtype.UUID
	if err := convID.Scan(convIDStr); err != nil {
		return nil, fmt.Errorf("invalid conversation_id: %w", err)
	}

	conv, err := a.queries.GetConversation(ctx, db.GetConversationParams{ID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("conversation not found: %w", err)
	}

	messages, err := a.queries.ListMessages(ctx, db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}

	// Build title and filename
	title := "Conversation Export"
	if conv.Title.Valid {
		title = conv.Title.String
	}

	date := time.Now().UTC().Format("2006-01-02")
	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	slug = regexp.MustCompile(`-+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	filename := fmt.Sprintf("%s-%s.md", date, slug)

	// Build markdown
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	for _, m := range messages {
		role := m.Role
		if role == "user" {
			role = "👤 User"
		} else if role == "assistant" {
			role = "🤖 Assistant"
		}
		sb.WriteString(fmt.Sprintf("## %s\n\n%s\n\n---\n\n", role, m.Content))
	}

	// Write to Obsidian vault
	vaultDir := filepath.Join(a.obsidianPath, "projects", "agent-os", "conversations")
	if err := os.MkdirAll(vaultDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create vault directory: %w", err)
	}

	fullPath := filepath.Join(vaultDir, filename)
	if err := os.WriteFile(fullPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("failed to write export file: %w", err)
	}

	relPath := filepath.Join("projects", "agent-os", "conversations", filename)
	slog.Info("slash /save: exported conversation to obsidian", "conversation_id", convID.String(), "path", relPath)

	return &SlashCommandResult{
		Type:    "save",
		Message: fmt.Sprintf("Exported to Obsidian: %s", relPath),
		Data: map[string]any{
			"conversation_id": convID.String(),
			"path":            relPath,
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
		"model": a.llmModel,
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
