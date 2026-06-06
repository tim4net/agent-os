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

func (h *HermesHarness) VersionInfo(ctx context.Context) (*VersionInfo, error) {
	checkedAt := time.Now().UTC()
	unknown := &VersionInfo{Current: "", Source: "unknown", CheckedAt: checkedAt}

	health, err := h.Health(ctx)
	if err != nil || health == nil || health.Version == "" {
		return unknown, nil
	}
	return &VersionInfo{Current: health.Version, Source: "health", CheckedAt: checkedAt}, nil
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
			return
		}

		// Stream ended without an explicit [DONE] or finish_reason:"stop" —
		// e.g. finish_reason "length" (max_tokens), "content_filter", or a clean
		// EOF / dropped connection. Every in-loop terminal path returns, so
		// reaching here means no Done was sent. Emit a synthetic Done so the
		// consumer persists the streamed content rather than discarding it (and
		// rolling back a brand-new conversation).
		ch <- ChatChunk{Done: true}
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

// hermesCommands defines ALL slash commands from the Hermes COMMAND_REGISTRY.
// Commands handled by Agent OS backend: /new, /clear, /compact, /compress, /retry,
// /undo, /history, /title, /stop, /save.
// Everything else is forwarded as a chat message to Hermes for processing.
var hermesCommands = []Command{
	// Session
	{Command: "/start", Description: "Acknowledge platform start pings without a reply"},
	{Command: "/new", Description: "Start a new session (fresh session ID + history)"},
	{Command: "/topic", Description: "Enable or inspect Telegram DM topic sessions"},
	{Command: "/clear", Description: "Clear screen and start a new session"},
	{Command: "/redraw", Description: "Force a full UI repaint (recovers from terminal drift)"},
	{Command: "/history", Description: "Show conversation history"},
	{Command: "/save", Description: "Save the current conversation"},
	{Command: "/retry", Description: "Retry the last message (resend to agent)"},
	{Command: "/undo", Description: "Remove the last user/assistant exchange"},
	{Command: "/title", Description: "Set a title for the current session"},
	{Command: "/handoff", Description: "Hand off this session to a messaging platform"},
	{Command: "/branch", Description: "Branch the current session (explore a different path)"},
	{Command: "/compress", Description: "Manually compress conversation context"},
	{Command: "/compact", Description: "Summarize and compact conversation history"},
	{Command: "/rollback", Description: "List or restore filesystem checkpoints"},
	{Command: "/snapshot", Description: "Create or restore state snapshots of Hermes config/state"},
	{Command: "/stop", Description: "Kill all running background processes"},
	{Command: "/approve", Description: "Approve a pending dangerous command"},
	{Command: "/deny", Description: "Deny a pending dangerous command"},
	{Command: "/background", Description: "Run a prompt in the background"},
	{Command: "/agents", Description: "Show active agents and running tasks"},
	{Command: "/queue", Description: "Queue a prompt for the next turn (doesn't interrupt)"},
	{Command: "/steer", Description: "Inject a message after the next tool call without interrupting"},
	{Command: "/goal", Description: "Set a standing goal Hermes works on across turns until achieved"},
	{Command: "/subgoal", Description: "Add or manage extra criteria on the active goal"},
	{Command: "/status", Description: "Show session info"},
	{Command: "/resume", Description: "Resume a previously-named session"},
	{Command: "/sessions", Description: "Browse and resume previous sessions"},
	{Command: "/sethome", Description: "Set this chat as the home channel"},
	{Command: "/restart", Description: "Gracefully restart the gateway after draining active runs"},
	// Info
	{Command: "/whoami", Description: "Show your slash command access (admin / user)"},
	{Command: "/profile", Description: "Show active profile name and home directory"},
	{Command: "/commands", Description: "Browse all commands and skills (paginated)"},
	{Command: "/help", Description: "Show available commands"},
	{Command: "/usage", Description: "Show token usage and rate limits for the current session"},
	{Command: "/insights", Description: "Show usage insights and analytics"},
	{Command: "/platforms", Description: "Show gateway/messaging platform status"},
	{Command: "/platform", Description: "Pause, resume, or list a failing gateway platform"},
	{Command: "/copy", Description: "Copy the last assistant response to clipboard"},
	{Command: "/paste", Description: "Attach clipboard image from your clipboard"},
	{Command: "/image", Description: "Attach a local image file for your next prompt"},
	{Command: "/update", Description: "Update Hermes Agent to the latest version"},
	{Command: "/debug", Description: "Upload debug report (system info + logs) and get shareable links"},
	// Configuration
	{Command: "/config", Description: "Show current configuration"},
	{Command: "/model", Description: "Switch model for this session"},
	{Command: "/codex-runtime", Description: "Toggle codex app-server runtime for OpenAI/Codex models"},
	{Command: "/gquota", Description: "Show Google Gemini Code Assist quota usage"},
	{Command: "/personality", Description: "Set a predefined personality"},
	{Command: "/statusbar", Description: "Toggle the context/model status bar"},
	{Command: "/verbose", Description: "Cycle tool progress display: off -> new -> all -> verbose"},
	{Command: "/footer", Description: "Toggle gateway runtime-metadata footer on final replies"},
	{Command: "/yolo", Description: "Toggle YOLO mode (skip all dangerous command approvals)"},
	{Command: "/reasoning", Description: "Manage reasoning effort and display"},
	{Command: "/fast", Description: "Toggle fast mode"},
	{Command: "/skin", Description: "Show or change the display skin/theme"},
	{Command: "/indicator", Description: "Pick the TUI busy-indicator style"},
	{Command: "/voice", Description: "Toggle voice mode"},
	{Command: "/busy", Description: "Control what Enter does while Hermes is working"},
	// Tools & Skills
	{Command: "/tools", Description: "Manage tools: /tools [list|disable|enable] [name...]"},
	{Command: "/toolsets", Description: "List available toolsets"},
	{Command: "/skills", Description: "Search, install, inspect, or manage skills"},
	{Command: "/bundles", Description: "List skill bundles (aliases /<name> for multiple skills)"},
	{Command: "/cron", Description: "Manage scheduled tasks"},
	{Command: "/curator", Description: "Background skill maintenance (status, run, pin, archive, list-archived)"},
	{Command: "/kanban", Description: "Multi-profile collaboration board (tasks, links, comments)"},
	{Command: "/reload", Description: "Reload .env variables into the running session"},
	{Command: "/reload-mcp", Description: "Reload MCP servers from config"},
	{Command: "/reload-skills", Description: "Re-scan ~/.hermes/skills/ for newly installed or removed skills"},
	{Command: "/browser", Description: "Connect browser tools to your live Chromium-family browser via CDP"},
	{Command: "/plugins", Description: "List installed plugins and their status"},
	// Exit
	{Command: "/quit", Description: "Exit the CLI"},
}

func (h *HermesHarness) Commands() []Command {
	return hermesCommands
}

// CreateSession creates a new Hermes session via POST /api/sessions.
// Returns the session ID (e.g. "api_...") on success.
func (h *HermesHarness) CreateSession(ctx context.Context, title string) (string, error) {
	url := h.baseURL + "/api/sessions"

	body := map[string]string{"title": title}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("hermes create-session: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("hermes create-session: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hermes create-session: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("hermes create-session: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("hermes create-session: decode response: %w", err)
	}

	if result.Session.ID == "" {
		return "", fmt.Errorf("hermes create-session: empty session ID in response")
	}

	return result.Session.ID, nil
}

// SessionChat sends a message to a Hermes session via
// POST /api/sessions/{sessionID}/chat/stream and returns a channel of
// ChatChunks parsed from the custom SSE event format.
//
// SSE events handled:
//   - event: assistant.delta → {delta: "text chunk"}  → ChatChunk{Content}
//   - event: done            → terminal                → ChatChunk{Done: true}
//   - event: error           → {message}               → ChatChunk{Error}
//
// Keepalive lines (starting with ":") are silently skipped.
func (h *HermesHarness) SessionChat(ctx context.Context, sessionID, message string) (<-chan ChatChunk, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/chat/stream", h.baseURL, sessionID)

	body := map[string]string{"message": message}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("hermes session-chat: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("hermes session-chat: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hermes session-chat: do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("hermes session-chat: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan ChatChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var currentEvent string

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines (SSE event separators)
			if line == "" {
				currentEvent = ""
				continue
			}

			// Skip keepalive comments
			if strings.HasPrefix(line, ":") {
				continue
			}

			// Parse event type
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
				continue
			}

			// Parse data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			switch currentEvent {
			case "assistant.delta":
				var payload struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					ch <- ChatChunk{Error: fmt.Errorf("parse assistant.delta: %w", err)}
					return
				}
				if payload.Delta != "" {
					ch <- ChatChunk{Content: payload.Delta}
				}

			case "assistant.completed":
				// Final assembled content — we already streamed deltas, so nothing to emit
				// unless we haven't seen any deltas (single-shot response).
				var payload struct {
					Content   string `json:"content"`
					Completed bool   `json:"completed"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					// Non-fatal, just log
					continue
				}
				// If we got completed content but never streamed deltas, emit it
				// This handles edge cases where the gateway sends the full response in one shot.
				// We don't have a way to know if deltas were sent, so we skip this to avoid
				// duplicate content. The done event will signal completion.

			case "run.completed":
				// Terminal event — the run is fully complete
				ch <- ChatChunk{Done: true}
				return

			case "done":
				// Terminal event
				ch <- ChatChunk{Done: true}
				return

			case "error":
				var payload struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					ch <- ChatChunk{Error: fmt.Errorf("session error (unparseable): %s", data)}
					return
				}
				ch <- ChatChunk{Error: fmt.Errorf("session error: %s", payload.Message)}
				return

			case "tool.started", "tool.completed", "tool.progress":
				var payload struct {
					Name       string `json:"name"`
					ToolName   string `json:"tool_name"`
					ToolCallID string `json:"tool_call_id"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					// Non-fatal, skip malformed tool event
					continue
				}
				status := "started"
				if currentEvent == "tool.completed" {
					status = "completed"
				}
				name := payload.Name
				if name == "" {
					name = payload.ToolName
				}
				if name != "" && status != "progress" {
					ch <- ChatChunk{ToolName: name, ToolStatus: status}
				}

			default:
				// Unknown event type — skip
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- ChatChunk{Error: fmt.Errorf("read session stream: %w", err)}
			return
		}

		// Session stream ended without a terminal run.completed/done/error event
		// — e.g. a clean EOF or a dropped gateway connection mid-run. Every
		// in-loop terminal path returns, so reaching here means no Done was sent.
		// Emit a synthetic Done so the consumer persists any streamed content
		// instead of discarding it (and rolling back a brand-new conversation).
		ch <- ChatChunk{Done: true}
	}()

	return ch, nil
}

func (h *HermesHarness) Close() error {
	h.httpClient.CloseIdleConnections()
	return nil
}
