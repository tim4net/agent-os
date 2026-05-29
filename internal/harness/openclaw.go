package harness

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// OpenClawHarness implements the Harness interface for OpenClaw/Crawbot agents.
// It connects to the OpenClaw gateway via WebSocket and uses the talk API
// (talk.session.create + talk.session.steer) for text-based chat.
type OpenClawHarness struct {
	baseURL   string
	authToken string
	httpClient *http.Client
}

func NewOpenClawHarness() Harness {
	return &OpenClawHarness{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (o *OpenClawHarness) Name() string { return "openclaw" }

func (o *OpenClawHarness) Init(config map[string]any) error {
	baseURL, ok := config["base_url"].(string)
	if !ok || baseURL == "" {
		return fmt.Errorf("openclaw harness: base_url is required")
	}
	o.baseURL = baseURL

	if token, ok := config["auth_token"].(string); ok {
		o.authToken = token
	}

	return nil
}

func (o *OpenClawHarness) Health(ctx context.Context) (*HealthStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("openclaw health: create request: %w", err)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		if _, ok := err.(net.Error); ok {
			return &HealthStatus{Status: "offline"}, nil
		}
		return &HealthStatus{Status: "offline"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return &HealthStatus{Status: "online"}, nil
	}
	return &HealthStatus{Status: "degraded"}, nil
}

// wsMessage is the JSON-RPC style message used by OpenClaw gateway.
type wsMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wsErr          `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type wsErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type talkEvent struct {
	Type    string          `json:"type"`
	Final   bool            `json:"final,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type textPayload struct {
	Text string `json:"text"`
}

func (o *OpenClawHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	// Extract last user message
	var userMsg string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userMsg = messages[i].Content
			break
		}
	}
	if userMsg == "" && len(messages) > 0 {
		userMsg = messages[len(messages)-1].Content
	}
	if userMsg == "" {
		return nil, fmt.Errorf("openclaw: no user message to send")
	}

	wsURL := o.buildWSURL()

	ch := make(chan ChatChunk, 64)
	go func() {
		defer close(ch)
		if err := o.runChat(ctx, wsURL, userMsg, ch); err != nil {
			slog.Warn("openclaw chat error", "error", err)
			select {
			case ch <- ChatChunk{Error: err}:
			default:
			}
		}
	}()

	return ch, nil
}

func (o *OpenClawHarness) buildWSURL() string {
	base := o.baseURL
	if strings.HasPrefix(base, "https://") {
		base = "wss://" + base[8:]
	} else if strings.HasPrefix(base, "http://") {
		base = "ws://" + base[7:]
	}
	base = strings.TrimRight(base, "/")
	return base + "/ws"
}

func (o *OpenClawHarness) runChat(ctx context.Context, wsURL, userMsg string, ch chan<- ChatChunk) error {
	// Connect with TLS skip for self-signed/Tailscale certs
	// Set Origin header to match the gateway's allowed origins
	headers := http.Header{}
	headers.Set("Origin", o.baseURL)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	slog.Debug("openclaw: websocket connected", "url", wsURL)

	// Track pending RPC requests
	var mu sync.Mutex
	pending := make(map[string]chan wsMessage)
	eventCh := make(chan wsMessage, 64)

	// Reader goroutine
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			var msg wsMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				select {
				case eventCh <- wsMessage{Type: "error", Error: &wsErr{Message: err.Error()}}:
				case <-done:
				}
				return
			}

			switch msg.Type {
			case "res":
				mu.Lock()
				if c, ok := pending[msg.ID]; ok {
					c <- msg
					delete(pending, msg.ID)
				}
				mu.Unlock()
			case "event":
				select {
				case eventCh <- msg:
				default:
					slog.Warn("openclaw: event channel full, dropping", "event", msg.Event)
				}
			}
		}
	}()

	// RPC helper
	sendRPC := func(method string, params any) (wsMessage, error) {
		id := fmt.Sprintf("aos-%d", time.Now().UnixNano())
		paramsJSON, _ := json.Marshal(params)

		resCh := make(chan wsMessage, 1)
		mu.Lock()
		pending[id] = resCh
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(pending, id)
			mu.Unlock()
		}()

		if err := wsjson.Write(ctx, conn, wsMessage{
			Type:   "req",
			ID:     id,
			Method: method,
			Params: paramsJSON,
		}); err != nil {
			return wsMessage{}, fmt.Errorf("write %s: %w", method, err)
		}

		select {
		case res := <-resCh:
			if res.Error != nil {
				return res, fmt.Errorf("%s: %s", method, res.Error.Message)
			}
			return res, nil
		case <-time.After(30 * time.Second):
			return wsMessage{}, fmt.Errorf("%s: timeout", method)
		}
	}

	// Step 1: Handle connect challenge or send connect directly
	connectParams := o.buildConnectParams()

	// Wait briefly for a challenge event, then send connect
	challengeTimer := time.NewTimer(3 * time.Second)
	defer challengeTimer.Stop()

	select {
	case evt := <-eventCh:
		if evt.Event == "connect.challenge" {
			slog.Debug("openclaw: received challenge, sending connect")
		} else {
			// Unexpected event, just proceed
			slog.Debug("openclaw: unexpected event before connect", "event", evt.Event)
		}
	case <-challengeTimer.C:
		slog.Debug("openclaw: no challenge, sending connect directly")
	}

	if _, err := sendRPC("connect", connectParams); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	slog.Debug("openclaw: gateway connected")

	// Step 2: Create talk session
	sessionKey := fmt.Sprintf("aos-%d", time.Now().UnixNano())

	sessionRes, err := sendRPC("talk.session.create", map[string]any{
		"sessionKey": sessionKey,
		"mode":       "text",
		"transport":  "gateway-relay",
		"brain":      "agent-consult",
	})
	if err != nil {
		// Fallback: try talk.client.create
		sessionRes, err = sendRPC("talk.client.create", map[string]any{
			"sessionKey": sessionKey,
		})
		if err != nil {
			return fmt.Errorf("session create: %w", err)
		}
	}

	var session struct {
		SessionID      string `json:"sessionId"`
		RelaySessionID string `json:"relaySessionId"`
	}
	_ = json.Unmarshal(sessionRes.Result, &session)

	slog.Debug("openclaw: session created", "sessionId", session.SessionID, "relaySessionId", session.RelaySessionID)

	// Step 3: Send user message via steer
	steerParams := map[string]any{
		"sessionKey": sessionKey,
		"text":       userMsg,
	}
	if session.SessionID != "" {
		steerParams["sessionId"] = session.SessionID
	}

	if _, err := sendRPC("talk.session.steer", steerParams); err != nil {
		// Fallback: try talk.client.steer
		if _, err2 := sendRPC("talk.client.steer", map[string]any{
			"sessionKey": sessionKey,
			"text":       userMsg,
		}); err2 != nil {
			return fmt.Errorf("steer: %w", err2)
		}
	}

	slog.Debug("openclaw: message sent, streaming response")

	// Step 4: Stream response
	var fullContent string
	timeout := time.NewTimer(120 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case evt := <-eventCh:
			if evt.Type == "error" {
				if evt.Error != nil {
					return fmt.Errorf("ws: %s", evt.Error.Message)
				}
				return fmt.Errorf("ws: unknown error")
			}

			// Gateway relay wraps talk events in talk.event
			if evt.Event == "talk.event" {
				var te talkEvent
				if err := json.Unmarshal(evt.Payload, &te); err != nil {
					continue
				}
				o.handleTalkEvent(te, ch, &fullContent)
				if te.Final || te.Type == "turn.ended" || te.Type == "session.closed" {
					ch <- ChatChunk{Done: true}
					return nil
				}
				continue
			}

			// Direct run events (text streaming via run status)
			if evt.Event != "" {
				var payload map[string]any
				json.Unmarshal(evt.Payload, &payload)

				if state, _ := payload["state"].(string); state == "final" {
					if msg, _ := payload["message"].(string); msg != "" {
						fullContent += msg
						ch <- ChatChunk{Content: msg}
					}
					ch <- ChatChunk{Done: true}
					return nil
				}

				if text, _ := payload["text"].(string); text != "" {
					fullContent += text
					ch <- ChatChunk{Content: text}
				}
			}

		case <-timeout.C:
			if fullContent != "" {
				ch <- ChatChunk{Done: true}
				return nil
			}
			return fmt.Errorf("openclaw: response timeout (120s)")

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (o *OpenClawHarness) handleTalkEvent(te talkEvent, ch chan<- ChatChunk, fullContent *string) {
	textEvents := map[string]bool{
		"output.text.delta": true,
		"output.text.done":  true,
		"transcript.delta":  true,
		"transcript.done":   true,
	}

	if textEvents[te.Type] {
		var tp textPayload
		if err := json.Unmarshal(te.Payload, &tp); err == nil && tp.Text != "" {
			*fullContent += tp.Text
			ch <- ChatChunk{Content: tp.Text}
		}
	}
}

func (o *OpenClawHarness) buildConnectParams() map[string]any {
	params := map[string]any{
		"minProtocol": 4,
		"maxProtocol": 4,
		"client": map[string]any{
			"id":       "webchat",
			"version":  "0.6.0",
			"platform": "linux",
			"mode":     "webchat",
		},
		"role":   "operator",
		"scopes": []string{"operator.admin", "operator.read", "operator.write"},
		"caps":   []string{"tool-events"},
	}
	if o.authToken != "" {
		params["auth"] = map[string]any{"token": o.authToken}
	}
	return params
}

func (o *OpenClawHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return nil, ErrNotSupported
}

// openclawCommands defines slash commands for the OpenClaw/Crawbot agent.
// Commands handled by Agent OS backend: /new, /clear, /compact, /retry, /undo,
// /history, /title, /stop, /save.
// Everything else is forwarded as a chat message to OpenClaw for processing.
var openclawCommands = []Command{
	// Session management (Agent OS handles these)
	{Command: "/new", Description: "Start a new session"},
	{Command: "/clear", Description: "Clear messages in current conversation"},
	{Command: "/compact", Description: "Summarize and compact conversation history"},
	{Command: "/retry", Description: "Retry the last message (resend to agent)"},
	{Command: "/undo", Description: "Remove the last user/assistant exchange"},
	{Command: "/history", Description: "Show conversation history"},
	{Command: "/title", Description: "Set a title for the current session"},
	{Command: "/stop", Description: "Stop current streaming response"},
	{Command: "/save", Description: "Export conversation to Obsidian"},
	// OpenClaw-specific (forwarded to OpenClaw)
	{Command: "/dreams", Description: "View or manage OpenClaw dreams"},
	{Command: "/help", Description: "Show available commands"},
}

func (o *OpenClawHarness) Commands() []Command { return openclawCommands }

func (o *OpenClawHarness) Close() error {
	o.httpClient.CloseIdleConnections()
	return nil
}
