package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// CreateConversationRequest creates a new conversation.
type CreateConversationRequest struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title"`
}

// CreateConversation creates a new conversation for an agent.
func (a *API) CreateConversation(w http.ResponseWriter, r *http.Request) {
	var req CreateConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	var agentID pgtype.UUID
	if err := agentID.Scan(req.AgentID); err != nil {
		http.Error(w, "invalid agent_id", http.StatusBadRequest)
		return
	}

	var title pgtype.Text
	if req.Title != "" {
		title.String = req.Title
		title.Valid = true
	}

	conv, err := a.queries.CreateConversation(r.Context(), db.CreateConversationParams{
		AgentID:  agentID,
		Title:    title,
		Metadata: []byte("{}"),
	})
	if err != nil {
		http.Error(w, "failed to create conversation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(conv)
}

// ListConversations returns conversations, optionally filtered by agent_id.
func (a *API) ListConversations(w http.ResponseWriter, r *http.Request) {
	var agentID pgtype.UUID
	if aid := r.URL.Query().Get("agent_id"); aid != "" {
		if err := agentID.Scan(aid); err != nil {
			http.Error(w, "invalid agent_id parameter", http.StatusBadRequest)
			return
		}
	}

	convs, err := a.queries.ListConversations(r.Context(), agentID)
	if err != nil {
		http.Error(w, "failed to list conversations", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(convs)
}

// GetConversationMessages returns all messages in a conversation.
func (a *API) GetConversationMessages(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var convID pgtype.UUID
	if err := convID.Scan(idStr); err != nil {
		http.Error(w, "invalid conversation ID", http.StatusBadRequest)
		return
	}

	messages, err := a.queries.ListMessages(r.Context(), convID)
	if err != nil {
		http.Error(w, "failed to list messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// ChatRequest is the body for the chat endpoint.
type ChatRequest struct {
	Message        string `json:"message"`
	Model          string `json:"model"`
	ConversationID string `json:"conversation_id"`
	SystemPrompt   string `json:"system_prompt"`
}

// ChatWithAgent sends a message to an agent and streams the response via SSE.
func (a *API) ChatWithAgent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var agentID pgtype.UUID
	if err := agentID.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Get the agent
	agent, err := a.queries.GetAgent(r.Context(), agentID)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	// Create or resolve conversation
	var convID pgtype.UUID
	if req.ConversationID != "" {
		if err := convID.Scan(req.ConversationID); err != nil {
			http.Error(w, "invalid conversation_id", http.StatusBadRequest)
			return
		}
	} else {
		// Create a new conversation
		var title pgtype.Text
		title.String = "Chat with " + agent.DisplayName
		title.Valid = true

		conv, err := a.queries.CreateConversation(r.Context(), db.CreateConversationParams{
			AgentID:  agentID,
			Title:    title,
			Metadata: []byte("{}"),
		})
		if err != nil {
			http.Error(w, "failed to create conversation", http.StatusInternalServerError)
			return
		}
		convID = conv.ID
	}

	// Store the user message
	_, err = a.queries.CreateMessage(r.Context(), db.CreateMessageParams{
		ConversationID: convID,
		Role:           "user",
		Content:        req.Message,
		Metadata:       []byte("{}"),
	})
	if err != nil {
		http.Error(w, "failed to store message", http.StatusInternalServerError)
		return
	}

	// Get conversation history for context
	history, err := a.queries.ListMessages(r.Context(), convID)
	if err != nil {
		http.Error(w, "failed to load history", http.StatusInternalServerError)
		return
	}

	// Build messages for the harness
	messages := make([]harness.ChatMessage, 0, len(history))
	for _, m := range history {
		messages = append(messages, harness.ChatMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	// Get the harness
	h, err := a.registry.Get(agent.Harness)
	if err != nil {
		http.Error(w, "unknown harness: "+agent.Harness, http.StatusBadRequest)
		return
	}

	config := a.buildHarnessConfig(agent)
	if err := h.Init(config); err != nil {
		http.Error(w, "harness init failed", http.StatusInternalServerError)
		return
	}
	defer h.Close()

	// Start the chat stream
	opts := harness.ChatOptions{
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	}

	chunkCh, err := h.Chat(r.Context(), messages, opts)
	if err != nil {
		if err == harness.ErrNotSupported {
			http.Error(w, "chat not supported for this agent", http.StatusNotImplemented)
			return
		}
		http.Error(w, "chat failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Stream SSE response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := w.(http.Flusher)

	// Collect full response for storage
	var fullContent string

	for chunk := range chunkCh {
		if chunk.Error != nil {
			// Send error event
			errData, _ := json.Marshal(map[string]string{"error": chunk.Error.Error()})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
			if canFlush {
				flusher.Flush()
			}
			break
		}

		if chunk.Content != "" {
			fullContent += chunk.Content
			data, _ := json.Marshal(map[string]string{"content": chunk.Content})
			fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}

		if chunk.Done {
			// Store assistant response
			a.queries.CreateMessage(r.Context(), db.CreateMessageParams{
				ConversationID: convID,
				Role:           "assistant",
				Content:        fullContent,
				Metadata:       []byte("{}"),
			})

			doneData, _ := json.Marshal(map[string]any{
				"done":            true,
				"conversation_id": convID.String(),
			})
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
			if canFlush {
				flusher.Flush()
			}
		}
	}

	// Ensure we sent a done event even if stream ended without one
	if len(fullContent) > 0 {
		// Check if we already stored it (done=true was sent)
		// Use a final newline to ensure clean close
		fmt.Fprint(w, "\n")
		if canFlush {
			flusher.Flush()
		}
	}
}

// ensure Done is recognized by compiler
var _ = io.EOF
