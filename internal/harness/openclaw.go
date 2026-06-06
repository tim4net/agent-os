package harness

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// OpenClawHarness implements the Harness interface for OpenClaw/Crawbot agents.
//
// It connects to the OpenClaw gateway via WebSocket and supports two auth modes,
// selected by the shape of config["auth_token"]:
//
//   - device-auth mode: auth_token is a JSON blob {deviceId,privateKeyPem,deviceToken}.
//     The harness performs an Ed25519 device-identity handshake and uses chat.send
//     for text chat (the modern gateway path).
//   - legacy mode: auth_token is a plain shared token. The harness uses the older
//     shared-token webchat connect + talk.* flow, preserved for back-compat and
//     other gateways.
type OpenClawHarness struct {
	baseURL    string
	authToken  string          // legacy shared-token mode
	identity   *deviceIdentity // non-nil => device-auth mode
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

	token, _ := config["auth_token"].(string)
	if token == "" {
		return nil // no auth configured
	}

	id, err := parseDeviceIdentity(token)
	switch {
	case err == nil:
		// Device-auth credential blob.
		o.identity = id
	case errors.Is(err, errNotDeviceCredential):
		// Plain shared token → legacy path.
		o.authToken = token
	default:
		// The token is a device-auth blob but its key is malformed. Surface it
		// rather than silently downgrading to no-auth (a security-relevant
		// silent failure). Never include key material in the error.
		return fmt.Errorf("openclaw harness: invalid device credential: %w", err)
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

// ---------------------------------------------------------------------------
// Device identity (device-auth mode)
// ---------------------------------------------------------------------------

// deviceIdentity holds the parsed Ed25519 identity used for device-auth.
// privKey and deviceToken are secret and must never be logged.
type deviceIdentity struct {
	deviceID    string
	deviceToken string
	privKey     ed25519.PrivateKey
	pubKey      ed25519.PublicKey // raw 32-byte public key
}

// deviceCredential is the JSON blob delivered in config["auth_token"] for
// device-auth agents (see PROTOCOL.md §Config plumbing).
type deviceCredential struct {
	DeviceID      string `json:"deviceId"`
	PrivateKeyPem string `json:"privateKeyPem"`
	DeviceToken   string `json:"deviceToken"`
}

// errNotDeviceCredential signals that auth_token is not a device-auth JSON blob
// and the caller should fall back to legacy shared-token mode.
var errNotDeviceCredential = errors.New("not a device credential")

// parseDeviceIdentity attempts to interpret token as a device-auth credential
// blob. Returns errNotDeviceCredential if token is a plain string (legacy), or a
// concrete error if it is a device blob with an unusable key.
func parseDeviceIdentity(token string) (*deviceIdentity, error) {
	var cred deviceCredential
	if err := json.Unmarshal([]byte(token), &cred); err != nil {
		return nil, errNotDeviceCredential
	}
	if cred.DeviceID == "" || cred.PrivateKeyPem == "" {
		return nil, errNotDeviceCredential
	}

	priv, err := parseEd25519PrivateKeyPEM(cred.PrivateKeyPem)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("private key is not Ed25519")
	}

	// Derive the deviceId from the public key so the connect frame is
	// self-consistent. Never trust a mismatched stored deviceId — the gateway
	// recomputes sha256(pubkey) and would reject a disagreeing id.
	derived := deriveDeviceID(pub)
	if cred.DeviceID != derived {
		slog.Warn("openclaw: stored deviceId does not match key; using derived id")
	}

	return &deviceIdentity{
		deviceID:    derived,
		deviceToken: cred.DeviceToken,
		privKey:     priv,
		pubKey:      pub,
	}, nil
}

// parseEd25519PrivateKeyPEM parses a PKCS8 PEM (as emitted by refclient.py's
// gen-identity) into an Ed25519 private key.
func parseEd25519PrivateKeyPEM(pemStr string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8: %w", err)
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, not Ed25519", key)
	}
	return edKey, nil
}

// deriveDeviceID returns the lowercase hex of sha256(raw 32-byte public key),
// matching the gateway's deriveDeviceIdFromPublicKey.
func deriveDeviceID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// buildV3Payload builds the exact pipe-joined v3 signature payload the gateway
// reconstructs and verifies. Field order is fixed; platform and deviceFamily
// are normalized to lowercase (the gateway lowercases them before verifying).
func buildV3Payload(deviceID, clientID, clientMode, role string, scopes []string, signedAtMs int64, token, nonce, platform, deviceFamily string) string {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return strings.Join([]string{
		"v3",
		deviceID,
		clientID,
		clientMode,
		role,
		strings.Join(scopes, ","),
		strconv.FormatInt(signedAtMs, 10),
		token,
		nonce,
		norm(platform),
		norm(deviceFamily),
	}, "|")
}

// signV3Payload signs the payload with the Ed25519 private key and returns the
// signature as base64url with no padding.
func signV3Payload(priv ed25519.PrivateKey, payload string) string {
	sig := ed25519.Sign(priv, []byte(payload))
	return base64.RawURLEncoding.EncodeToString(sig)
}

// deviceScopes is the operator scope set requested in device-auth mode. The CSV
// order in the signed payload must match the order sent in connect.scopes.
var deviceScopes = []string{"operator.read", "operator.write"}

const (
	deviceClientID   = "gateway-client"
	deviceClientMode = "backend"
	deviceRole       = "operator"
	devicePlatform   = "linux"
)

// buildConnectParams builds the device-auth connect request params, signing the
// v3 payload over the supplied signedAtMs and challenge nonce.
func (id *deviceIdentity) buildConnectParams(signedAtMs int64, nonce string) map[string]any {
	payload := buildV3Payload(
		id.deviceID, deviceClientID, deviceClientMode, deviceRole, deviceScopes,
		signedAtMs, id.deviceToken, nonce, devicePlatform, "",
	)
	signature := signV3Payload(id.privKey, payload)

	return map[string]any{
		"minProtocol": 4,
		"maxProtocol": 4,
		"client": map[string]any{
			"id":       deviceClientID,
			"version":  "0.6.0",
			"platform": devicePlatform,
			"mode":     deviceClientMode,
		},
		"role":   deviceRole,
		"scopes": deviceScopes,
		"caps":   []string{"tool-events"},
		"auth":   map[string]any{"deviceToken": id.deviceToken},
		"device": map[string]any{
			"id":        id.deviceID,
			"publicKey": base64.RawURLEncoding.EncodeToString(id.pubKey),
			"signature": signature,
			"signedAt":  signedAtMs,
			"nonce":     nonce,
		},
	}
}

// ---------------------------------------------------------------------------
// WebSocket wire types + transport
// ---------------------------------------------------------------------------

// wsMessage is the JSON-RPC style message used by the OpenClaw gateway.
type wsMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Ok      bool            `json:"ok,omitempty"`
	Error   *wsErr          `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type wsErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ocConn wraps a gateway WebSocket with JSON-RPC request/response correlation
// and an event channel. It is shared by both the device-auth and legacy flows.
type ocConn struct {
	conn    *websocket.Conn
	ctx     context.Context
	mu      sync.Mutex
	pending map[string]chan wsMessage
	eventCh chan wsMessage
	done    chan struct{}
}

// dial opens the gateway WebSocket (TLS verification skipped for the tailnet
// cert) and starts the reader goroutine.
func (o *OpenClawHarness) dial(ctx context.Context, wsURL string) (*ocConn, error) {
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
		return nil, fmt.Errorf("ws connect: %w", err)
	}

	c := &ocConn{
		conn:    conn,
		ctx:     ctx,
		pending: make(map[string]chan wsMessage),
		eventCh: make(chan wsMessage, 64),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *ocConn) readLoop() {
	for {
		var msg wsMessage
		if err := wsjson.Read(c.ctx, c.conn, &msg); err != nil {
			select {
			case c.eventCh <- wsMessage{Type: "error", Error: &wsErr{Message: err.Error()}}:
			case <-c.done:
			}
			return
		}

		switch msg.Type {
		case "res":
			c.mu.Lock()
			if ch, ok := c.pending[msg.ID]; ok {
				ch <- msg
				delete(c.pending, msg.ID)
			}
			c.mu.Unlock()
		case "event":
			select {
			case c.eventCh <- msg:
			default:
				slog.Warn("openclaw: event channel full, dropping", "event", msg.Event)
			}
		}
	}
}

// sendRPC issues a request and waits for the correlated response (or 30s timeout).
func (c *ocConn) sendRPC(method string, params any) (wsMessage, error) {
	id := fmt.Sprintf("aos-%d", time.Now().UnixNano())
	paramsJSON, _ := json.Marshal(params)

	resCh := make(chan wsMessage, 1)
	c.mu.Lock()
	c.pending[id] = resCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := wsjson.Write(c.ctx, c.conn, wsMessage{
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

// waitChallenge waits up to timeout for a connect.challenge event and returns
// its nonce, ignoring other events. Returns "" if none arrives in time.
func (c *ocConn) waitChallenge(timeout time.Duration) string {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case evt := <-c.eventCh:
			if evt.Event == "connect.challenge" {
				var p struct {
					Nonce string `json:"nonce"`
				}
				_ = json.Unmarshal(evt.Payload, &p)
				return p.Nonce
			}
			// ignore other events, keep waiting for the challenge
		case <-deadline.C:
			return ""
		}
	}
}

func (c *ocConn) close() {
	close(c.done)
	c.conn.Close(websocket.StatusNormalClosure, "done")
}

// ---------------------------------------------------------------------------
// Chat
// ---------------------------------------------------------------------------

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
		var err error
		if o.identity != nil {
			err = o.runChatDeviceAuth(ctx, wsURL, userMsg, ch)
		} else {
			err = o.runChatLegacy(ctx, wsURL, userMsg, ch)
		}
		if err != nil {
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

// runChatDeviceAuth performs the device-identity handshake then chat.send,
// streaming the reply from chat events. See PROTOCOL.md §Handshake / §chat.send.
func (o *OpenClawHarness) runChatDeviceAuth(ctx context.Context, wsURL, userMsg string, ch chan<- ChatChunk) error {
	c, err := o.dial(ctx, wsURL)
	if err != nil {
		return err
	}
	defer c.close()

	slog.Debug("openclaw: websocket connected (device-auth)", "url", wsURL)

	// 1. Wait for the challenge nonce, then build + send a signed connect.
	nonce := c.waitChallenge(3 * time.Second)
	signedAt := time.Now().UnixMilli()
	connectParams := o.identity.buildConnectParams(signedAt, nonce)

	res, err := c.sendRPC("connect", connectParams)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if ok, msg := connectSucceeded(res); !ok {
		return fmt.Errorf("connect rejected: %s", msg)
	}
	slog.Debug("openclaw: device-auth connected (hello-ok)")

	// 2. chat.send (requires operator.write). idempotencyKey doubles as the runId.
	sessionKey := fmt.Sprintf("aos-%d", time.Now().UnixNano())
	runID := uuid.NewString()
	sres, err := c.sendRPC("chat.send", map[string]any{
		"sessionKey":     sessionKey,
		"message":        userMsg,
		"idempotencyKey": runID,
	})
	if err != nil {
		return fmt.Errorf("chat.send: %w", err)
	}
	if !sres.Ok {
		return fmt.Errorf("chat.send rejected")
	}
	// Prefer the runId echoed by the gateway so we match its stream exactly.
	var sp struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(sres.Payload, &sp); err == nil && sp.RunID != "" {
		runID = sp.RunID
	}
	slog.Debug("openclaw: chat.send accepted", "runId", runID)

	// 3. Stream the reply from chat events, matched by runId.
	decoder := &chatStreamDecoder{runID: runID}
	timeout := time.NewTimer(120 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case evt := <-c.eventCh:
			if evt.Type == "error" {
				if evt.Error != nil {
					return fmt.Errorf("ws: %s", evt.Error.Message)
				}
				return fmt.Errorf("ws: unknown error")
			}
			// Only the chat channel is consumed for text; ignore tick/health/agent.
			if evt.Event != "chat" {
				continue
			}
			chunks, done, derr := decoder.handle(evt.Payload)
			if derr != nil {
				slog.Debug("openclaw: skipping undecodable chat event", "error", derr)
				continue
			}
			for _, cc := range chunks {
				ch <- cc
			}
			if done {
				return nil
			}

		case <-timeout.C:
			return fmt.Errorf("openclaw: response timeout (120s)")

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// connectSucceeded reports whether a connect response is a successful hello-ok.
func connectSucceeded(res wsMessage) (bool, string) {
	if !res.Ok {
		if res.Error != nil {
			return false, res.Error.Message
		}
		return false, "connect not ok"
	}
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(res.Payload, &p)
	if p.Type != "hello-ok" {
		return false, fmt.Sprintf("unexpected connect payload type %q", p.Type)
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// Streaming decode (device-auth chat events)
// ---------------------------------------------------------------------------

// chatEventPayload mirrors a "chat" event payload (PROTOCOL.md §Streaming events).
type chatEventPayload struct {
	RunID      string       `json:"runId"`
	SessionKey string       `json:"sessionKey"`
	Seq        int          `json:"seq"`
	State      string       `json:"state"`
	DeltaText  string       `json:"deltaText"`
	Message    *chatMessage `json:"message"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []chatContent `json:"content"`
}

type chatContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (p *chatEventPayload) fullText() string {
	if p.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range p.Message.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// chatStreamDecoder turns a sequence of chat-event payloads (for one runId) into
// ChatChunks. It tracks text already emitted so the terminal "final" event only
// emits trailing text not previously sent.
type chatStreamDecoder struct {
	runID string
	sent  strings.Builder
}

// handle decodes one chat-event payload, returning the chunks to emit, whether
// the stream is terminal, and any decode error.
func (d *chatStreamDecoder) handle(payload json.RawMessage) (chunks []ChatChunk, done bool, err error) {
	var p chatEventPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, false, err
	}
	// Ignore events belonging to a different run.
	if d.runID != "" && p.RunID != "" && p.RunID != d.runID {
		return nil, false, nil
	}

	switch p.State {
	case "delta":
		if p.DeltaText != "" {
			d.sent.WriteString(p.DeltaText)
			chunks = append(chunks, ChatChunk{Content: p.DeltaText})
		}
	case "final":
		full := p.fullText()
		sent := d.sent.String()
		trailing := full
		if strings.HasPrefix(full, sent) {
			trailing = full[len(sent):]
		}
		if trailing != "" {
			chunks = append(chunks, ChatChunk{Content: trailing})
		}
		chunks = append(chunks, ChatChunk{Done: true})
		done = true
	}
	return chunks, done, nil
}

// ---------------------------------------------------------------------------
// Legacy shared-token webchat flow (preserved for back-compat / other gateways)
// ---------------------------------------------------------------------------

type talkEvent struct {
	Type    string          `json:"type"`
	Final   bool            `json:"final,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type textPayload struct {
	Text string `json:"text"`
}

func (o *OpenClawHarness) runChatLegacy(ctx context.Context, wsURL, userMsg string, ch chan<- ChatChunk) error {
	c, err := o.dial(ctx, wsURL)
	if err != nil {
		return err
	}
	defer c.close()

	slog.Debug("openclaw: websocket connected (legacy)", "url", wsURL)

	// Step 1: wait briefly for a challenge, then send the shared-token connect.
	c.waitChallenge(3 * time.Second)
	if _, err := c.sendRPC("connect", o.buildLegacyConnectParams()); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	slog.Debug("openclaw: gateway connected (legacy)")

	// Step 2: create talk session
	sessionKey := fmt.Sprintf("aos-%d", time.Now().UnixNano())

	sessionRes, err := c.sendRPC("talk.session.create", map[string]any{
		"sessionKey": sessionKey,
		"mode":       "text",
		"transport":  "gateway-relay",
		"brain":      "agent-consult",
	})
	if err != nil {
		// Fallback: try talk.client.create
		sessionRes, err = c.sendRPC("talk.client.create", map[string]any{
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

	// Step 3: send user message via steer
	steerParams := map[string]any{
		"sessionKey": sessionKey,
		"text":       userMsg,
	}
	if session.SessionID != "" {
		steerParams["sessionId"] = session.SessionID
	}

	if _, err := c.sendRPC("talk.session.steer", steerParams); err != nil {
		// Fallback: try talk.client.steer
		if _, err2 := c.sendRPC("talk.client.steer", map[string]any{
			"sessionKey": sessionKey,
			"text":       userMsg,
		}); err2 != nil {
			return fmt.Errorf("steer: %w", err2)
		}
	}

	slog.Debug("openclaw: message sent, streaming response (legacy)")

	// Step 4: stream response
	var fullContent string
	timeout := time.NewTimer(120 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case evt := <-c.eventCh:
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

func (o *OpenClawHarness) buildLegacyConnectParams() map[string]any {
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
