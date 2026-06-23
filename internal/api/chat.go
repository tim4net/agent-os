package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

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
		OwnerID:  ownerID,
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
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var agentID pgtype.UUID
	if aid := r.URL.Query().Get("agent_id"); aid != "" {
		if err := agentID.Scan(aid); err != nil {
			http.Error(w, "invalid agent_id parameter", http.StatusBadRequest)
			return
		}
	}

	convs, err := a.queries.ListConversations(r.Context(), db.ListConversationsParams{OwnerID: ownerID, Column2: agentID})
	if err != nil {
		http.Error(w, "failed to list conversations", http.StatusInternalServerError)
		return
	}

	if convs == nil {
		convs = []db.ListConversationsRow{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(convs)
}

// GetConversationMessages returns all messages in a conversation.
func (a *API) GetConversationMessages(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")

	var convID pgtype.UUID
	if err := convID.Scan(idStr); err != nil {
		http.Error(w, "invalid conversation ID", http.StatusBadRequest)
		return
	}

	messages, err := a.queries.ListMessages(r.Context(), db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "failed to list messages", http.StatusInternalServerError)
		return
	}

	if messages == nil {
		messages = []db.Message{}
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
	ProjectID      string `json:"project_id,omitempty"`
	// Mode selects an answer-style mode (e.g. "perplexity" for a
	// web-search-grounded, cited answer). Empty/unknown = default chat.
	Mode string `json:"mode,omitempty"`
}

// conversationMeta is the structured metadata stored in conversations.metadata.
type conversationMeta struct {
	HermesSessionID string `json:"hermes_session_id,omitempty"`
}

// getConversationMeta parses the JSON metadata bytes into conversationMeta.
func getConversationMeta(raw []byte) conversationMeta {
	var meta conversationMeta
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &meta)
	}
	return meta
}

// marshalConversationMeta serializes conversationMeta to JSON bytes.
func marshalConversationMeta(meta conversationMeta) []byte {
	b, _ := json.Marshal(meta)
	return b
}

// ChatWithAgent sends a message to an agent and streams the response via SSE.
func (a *API) ChatWithAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

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
	agent, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: agentID, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	// Get the harness
	h, err := a.registry.Get(agent.Harness)
	if err != nil {
		http.Error(w, "unknown harness: "+agent.Harness, http.StatusBadRequest)
		return
	}

	config := a.buildHarnessConfig(r.Context(), agent)
	if err := h.Init(config); err != nil {
		http.Error(w, "harness init failed", http.StatusInternalServerError)
		return
	}
	defer h.Close()

	// Determine if this harness is a HermesHarness (supports sessions)
	hermesHarness, isHermes := h.(*harness.HermesHarness)

	// Create or resolve conversation
	var convID pgtype.UUID
	var convMeta conversationMeta

	// Tracks whether THIS request created the conversation. If the first turn of
	// a brand-new conversation fails before we start streaming, we roll it back
	// (delete) so a failed send never leaves an orphan conversation in history.
	// We never roll back a pre-existing conversation the user is continuing.
	conversationCreated := false

	if req.ConversationID != "" {
		if err := convID.Scan(req.ConversationID); err != nil {
			http.Error(w, "invalid conversation_id", http.StatusBadRequest)
			return
		}
		// Load existing conversation metadata
		conv, err := a.queries.GetConversation(r.Context(), db.GetConversationParams{ID: convID, OwnerID: ownerID})
		if err != nil {
			http.Error(w, "conversation not found", http.StatusNotFound)
			return
		}
		convMeta = getConversationMeta(conv.Metadata)
	} else {
		// Create a new conversation
		var title pgtype.Text
		// Use timestamped title to avoid Hermes session title collisions
		// ("New conversation" already-in-use errors after the first one).
		title.String = "AgentOS " + time.Now().Format("2006-01-02 15:04:05")
		title.Valid = true

		// For Hermes agents, create a session and store its ID in metadata
		if isHermes {
			sessionID, sessErr := hermesHarness.CreateSession(r.Context(), title.String)
			if sessErr != nil {
				slog.Warn("failed to create Hermes session, falling back to raw chat", "error", sessErr)
				// Non-fatal: will fall back to Chat() below
			} else {
				convMeta.HermesSessionID = sessionID
				slog.Debug("created Hermes session", "session_id", sessionID)
			}
		}

		conv, err := a.queries.CreateConversation(r.Context(), db.CreateConversationParams{
			OwnerID:  ownerID,
			AgentID:  agentID,
			Title:    title,
			Metadata: marshalConversationMeta(convMeta),
		})
		if err != nil {
			http.Error(w, "failed to create conversation", http.StatusInternalServerError)
			return
		}
		convID = conv.ID
		conversationCreated = true
	}

	// failPreStream rolls back a brand-new conversation (and its cascaded user
	// message) before returning an error, so a chat turn that fails before the
	// SSE stream starts never leaves an orphan conversation in history. For an
	// existing conversation it is a no-op rollback — we only delete what this
	// request created. Use this for every error return between here and the
	// point where SSE headers are written.
	failPreStream := func(msg string, code int) {
		if conversationCreated {
			delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if delErr := a.queries.DeleteConversation(delCtx, db.DeleteConversationParams{ID: convID, OwnerID: ownerID}); delErr != nil {
				slog.Warn("failed to roll back orphan conversation after pre-stream error",
					"conversation_id", convID.String(), "error", delErr)
			} else {
				slog.Debug("rolled back orphan conversation after pre-stream error",
					"conversation_id", convID.String())
			}
		}
		http.Error(w, msg, code)
	}

	// Store the user message
	userMsg, err := a.queries.CreateMessage(r.Context(), db.CreateMessageParams{
		OwnerID:        ownerID,
		ConversationID: convID,
		Role:           "user",
		Content:        req.Message,
		Metadata:       []byte("{}"),
	})
	if err != nil {
		failPreStream("failed to store message", http.StatusInternalServerError)
		return
	}

	// If this is a new conversation (no conversation_id in request),
	// immediately update the title to a truncated version of the first message.
	if req.ConversationID == "" {
		truncated := req.Message
		if len(truncated) > 60 {
			// Try to truncate at a word boundary near 60 chars
			truncated = truncated[:60]
			if idx := strings.LastIndex(truncated, " "); idx > 30 {
				truncated = truncated[:idx]
			}
		}
		// Remove newlines from title
		truncated = strings.ReplaceAll(truncated, "\n", " ")
		truncated = strings.TrimSpace(truncated)

		// Ensure uniqueness per agent — append #2, #3, etc. if duplicate
		uniqueTitle := a.makeUniqueTitle(r.Context(), agentID, convID, truncated, ownerID)

		var titleText pgtype.Text
		titleText.String = uniqueTitle
		titleText.Valid = true
		if _, titleErr := a.queries.UpdateConversation(r.Context(), db.UpdateConversationParams{
			ID:      convID,
			Title:   titleText,
			OwnerID: ownerID,
		}); titleErr != nil {
			slog.Warn("failed to set immediate title", "error", titleErr)
		}

		// Fire off background LLM title generation after a 30s delay
		go a.deferredLLMTitle(convID, agentID, ownerID)
	}

	// Send user message ID as first SSE event
	userIDData, _ := json.Marshal(map[string]string{"user_message_id": userMsg.ID.String()})

	// Decide: session chat (Hermes with session ID) vs raw chat (everything else)
	var chunkCh <-chan harness.ChatChunk
	var ragSources []string

	useSession := isHermes && convMeta.HermesSessionID != ""

	// Mode augmentation (e.g. Perplexity-style search-grounded answers). The
	// instruction is injected into the system prompt for the legacy path and
	// prepended to the message for the Hermes session path (which has no
	// separate system-prompt channel). Unknown/default modes are a no-op.
	modePrompt := modeSystemPrompt(req.Mode)

	if useSession {
		// Use the Hermes session API — the agent manages its own conversation
		// context internally, so we don't need to build message history.
		msgToSend := req.Message
		if modePrompt != "" {
			msgToSend = modePrompt + "\n\n" + req.Message
		}
		chunkCh, err = hermesHarness.SessionChat(r.Context(), convMeta.HermesSessionID, msgToSend)
		if err != nil {
			if err == harness.ErrNotSupported {
				failPreStream("chat not supported for this agent", http.StatusNotImplemented)
				return
			}
			failPreStream("chat failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Legacy path: build full message history for raw LLM chat
		// Resolve optional project_id from request for RAG scoping.
		var projectID pgtype.UUID
		if req.ProjectID != "" {
			if err := projectID.Scan(req.ProjectID); err != nil {
				slog.Warn("RAG: invalid project_id in chat request", "project_id", req.ProjectID, "error", err)
			}
		}

		// RAG: search memory_index for relevant context using the user's message
		systemPrompt := req.SystemPrompt

		// Prepend the mode augmentation (e.g. Perplexity) so it takes
		// precedence in the composed system prompt. No-op for default mode.
		if modePrompt != "" {
			if systemPrompt == "" {
				systemPrompt = modePrompt
			} else {
				systemPrompt = modePrompt + "\n\n" + systemPrompt
			}
		}

		if ragCtx := a.searchMemoryForContext(req.Message, ownerID, projectID); ragCtx != nil {
			if systemPrompt == "" {
				systemPrompt = ragCtx.SystemBlock
			} else {
				systemPrompt = systemPrompt + "\n\n" + ragCtx.SystemBlock
			}
			ragSources = ragCtx.Sources
			slog.Debug("RAG: injected memory context into system prompt", "query_length", len(req.Message), "sources", len(ragSources))
		}

		// Get conversation history for context
		history, err := a.queries.ListMessages(r.Context(), db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
		if err != nil {
			failPreStream("failed to load history", http.StatusInternalServerError)
			return
		}

		// Build messages for the harness
		messages := make([]harness.ChatMessage, 0, len(history)+1)

		// If the agent has a system_prompt, prepend it as a system message
		if agent.SystemPrompt.Valid && agent.SystemPrompt.String != "" {
			messages = append(messages, harness.ChatMessage{
				Role:    "system",
				Content: agent.SystemPrompt.String,
			})
		}

		for _, m := range history {
			messages = append(messages, harness.ChatMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}

		// Start the chat stream with the actual messages
		opts := harness.ChatOptions{
			Model:        req.Model,
			SystemPrompt: systemPrompt,
		}

		chunkCh, err = h.Chat(r.Context(), messages, opts)
		if err != nil {
			if err == harness.ErrNotSupported {
				failPreStream("chat not supported for this agent", http.StatusNotImplemented)
				return
			}
			failPreStream("chat failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Now that chat is confirmed supported, start the SSE stream
	// Set SSE headers before first write
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send user message ID as first SSE event
	fmt.Fprintf(w, "event: info\ndata: %s\n\n", userIDData)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	flusher, canFlush := w.(http.Flusher)

	// Collect full response for storage
	var fullContent string
	streamErrored := false
	doneSent := false

	for chunk := range chunkCh {
		if chunk.Error != nil {
			// Send error event
			errData, _ := json.Marshal(map[string]string{"error": chunk.Error.Error()})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
			if canFlush {
				flusher.Flush()
			}
			streamErrored = true
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

		if chunk.ToolName != "" {
			// Forward tool lifecycle events to the frontend
			toolData, _ := json.Marshal(map[string]string{
				"tool_name":   chunk.ToolName,
				"tool_status": chunk.ToolStatus,
			})
			fmt.Fprintf(w, "event: tool\ndata: %s\n\n", toolData)
			if canFlush {
				flusher.Flush()
			}
		}

		if chunk.Done {
			// Store assistant response
			assistantMsg, storeErr := a.queries.CreateMessage(r.Context(), db.CreateMessageParams{
				OwnerID:        ownerID,
				ConversationID: convID,
				Role:           "assistant",
				Content:        fullContent,
				Metadata:       []byte("{}"),
			})
			if storeErr != nil {
				slog.Warn("failed to store assistant message", "error", storeErr)
			}

			doneData, _ := json.Marshal(map[string]any{
				"done":            true,
				"conversation_id": convID.String(),
				"context_sources": ragSources,
				"user_message_id": userMsg.ID.String(),
				"assistant_message_id": func() string {
					if storeErr == nil {
						return assistantMsg.ID.String()
					}
					return ""
				}(),
			})
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
			if canFlush {
				flusher.Flush()
			}
			doneSent = true
		}
	}

	// Roll back an orphan: if THIS request created the conversation but no done
	// event was ever sent (an error chunk, or an empty stream that never
	// completed), the client never received the conversation_id — so delete the
	// conversation (cascading its lone user message) rather than leave a one-sided
	// ghost in history. We only roll back conversations this request created —
	// never an existing thread the user was continuing — and never one the client
	// already learned about via a done event.
	if conversationCreated && !doneSent {
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if delErr := a.queries.DeleteConversation(delCtx, db.DeleteConversationParams{ID: convID, OwnerID: ownerID}); delErr != nil {
			slog.Warn("failed to roll back orphan conversation after failed stream",
				"conversation_id", convID.String(), "stream_errored", streamErrored, "error", delErr)
		} else {
			slog.Debug("rolled back orphan conversation after failed stream",
				"conversation_id", convID.String(), "stream_errored", streamErrored)
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

// RAGContext holds the result of a memory search for RAG injection.
type RAGContext struct {
	SystemBlock string   // formatted block for injection into system prompt
	Sources     []string // list of file paths/titles used
}

// searchMemoryForContext searches the memory_index for notes relevant to the
// given query and returns a formatted context block suitable for injection
// into the system prompt. Returns nil if no results are found.
// Uses a separate context with a 10s timeout to avoid Chi timeout pitfalls.
// ownerID is required (owner-scoped data isolation).
// projectID is optional (zero-value = search all projects).
func (a *API) searchMemoryForContext(query string, ownerID, projectID pgtype.UUID) *RAGContext {
	if query == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results, err := a.queries.SearchMemory(ctx, db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: query,
		Limit:              3,
		ProjectID:          projectID,
	})
	if err != nil {
		slog.Warn("RAG: memory search failed", "error", err)
		return nil
	}

	if len(results) == 0 {
		return nil
	}

	var sources []string
	var sb strings.Builder
	sb.WriteString("Relevant context from knowledge base:\n---\n")
	for i, r := range results {
		title := r.FilePath
		if r.Title.Valid && r.Title.String != "" {
			title = r.Title.String
		}
		sources = append(sources, title)
		content := ""
		if r.Content.Valid {
			content = r.Content.String
		}
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] %s (%s):\n%s\n---\n", i+1, title, r.FilePath, content))
	}

	return &RAGContext{
		SystemBlock: sb.String(),
		Sources:     sources,
	}
}

// makeUniqueTitle ensures the proposed title is unique for the given agent.
// If another conversation with the same agent has the same title, it appends
// #2, #3, etc. until unique.
func (a *API) makeUniqueTitle(ctx context.Context, agentID pgtype.UUID, convID pgtype.UUID, proposed string, ownerID pgtype.UUID) string {
	base := proposed
	suffix := 2
	for {
		var count int
		err := a.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM conversations WHERE agent_id = $1 AND title = $2 AND id != $3 AND owner_id = $4`,
			agentID, base, convID, ownerID,
		).Scan(&count)
		if err != nil {
			slog.Warn("title uniqueness check failed, using proposed title", "error", err)
			return proposed
		}
		if count == 0 {
			return base
		}
		base = fmt.Sprintf("%s #%d", proposed, suffix)
		suffix++
		// Safety limit
		if suffix > 100 {
			return proposed
		}
	}
}

// deferredLLMTitle waits 30 seconds then generates an LLM-based title for
// the conversation, updating the title column in the DB.
func (a *API) deferredLLMTitle(convID pgtype.UUID, agentID pgtype.UUID, ownerID pgtype.UUID) {
	time.Sleep(30 * time.Second)

	// Thinking models (e.g. local-qwen/Qwen 3.6) take 60-90s for inference.
	// The 300s matches title_worker.go and summaries.go timeouts.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	// Load conversation messages for context
	msgs, err := a.queries.ListMessages(ctx, db.ListMessagesParams{ConversationID: convID, OwnerID: ownerID})
	if err != nil || len(msgs) == 0 {
		return
	}

	// Build compact text representation (first 6 messages)
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
			continue
		}
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", role, content))
	}

	summary, err := a.generateSummary(ctx, sb.String())
	if err != nil {
		slog.Warn("deferred LLM title generation failed", "conversation_id", convID.String(), "error", err)
		return
	}

	// Make the LLM title unique per agent
	uniqueTitle := a.makeUniqueTitle(ctx, agentID, convID, summary, ownerID)

	var titleText pgtype.Text
	titleText.String = uniqueTitle
	titleText.Valid = true
	if _, updateErr := a.queries.UpdateConversation(ctx, db.UpdateConversationParams{
		ID:      convID,
		Title:   titleText,
		OwnerID: ownerID,
	}); updateErr != nil {
		// Conversation may have been deleted between chat and title generation — not an error
		if updateErr.Error() == "no rows in result set" {
			slog.Debug("skipped title update for deleted conversation", "conversation_id", convID.String())
		} else {
			slog.Warn("failed to update conversation title with LLM summary", "conversation_id", convID.String(), "error", updateErr)
		}
	} else {
		slog.Debug("updated conversation title via LLM", "conversation_id", convID.String(), "title", uniqueTitle)
	}
}
