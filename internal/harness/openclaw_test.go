package harness

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// newTestKey returns a fresh Ed25519 keypair for tests.
func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// pkcs8PEM marshals an Ed25519 private key to a PKCS8 PEM string, matching the
// format refclient.py emits via serialization.PrivateFormat.PKCS8.
func pkcs8PEM(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// deviceBlob builds the JSON credential blob the gateway delivers in auth_token.
func deviceBlob(t *testing.T, deviceID string, priv ed25519.PrivateKey, deviceToken string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"deviceId":      deviceID,
		"privateKeyPem": pkcs8PEM(t, priv),
		"deviceToken":   deviceToken,
	})
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	return string(b)
}

// --- 1. Identity / signature ------------------------------------------------

func TestDeriveDeviceID_IsHexSHA256OfRawPub(t *testing.T) {
	pub, _ := newTestKey(t)

	got := deriveDeviceID(pub)

	sum := sha256.Sum256(pub)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("deriveDeviceID = %q, want %q", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("deviceId hex len = %d, want 64", len(got))
	}
}

func TestBuildV3Payload_ExactFieldOrderAndLowercasing(t *testing.T) {
	got := buildV3Payload(
		"DEVID", "gateway-client", "backend", "operator",
		[]string{"operator.read", "operator.write"},
		1700000000123, "TOKEN", "NONCE", "Linux", "Desktop",
	)

	// Pipe-joined, exact field order; platform + deviceFamily lowercased.
	want := "v3|DEVID|gateway-client|backend|operator|operator.read,operator.write|1700000000123|TOKEN|NONCE|linux|desktop"
	if got != want {
		t.Fatalf("payload mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildV3Payload_EmptyDeviceFamilyTrailingPipe(t *testing.T) {
	got := buildV3Payload(
		"d", "gateway-client", "backend", "operator",
		[]string{"operator.read", "operator.write"},
		42, "", "n", "linux", "",
	)
	// Empty token and empty deviceFamily must still appear as empty fields:
	// trailing pipe after platform, empty token segment between signedAt and nonce.
	want := "v3|d|gateway-client|backend|operator|operator.read,operator.write|42||n|linux|"
	if got != want {
		t.Fatalf("payload mismatch\n got: %q\nwant: %q", got, want)
	}
	if !strings.HasSuffix(got, "|") {
		t.Fatalf("expected trailing empty deviceFamily pipe, got %q", got)
	}
}

func TestSignV3Payload_VerifiesAgainstPublicKey(t *testing.T) {
	pub, priv := newTestKey(t)
	payload := buildV3Payload(
		deriveDeviceID(pub), "gateway-client", "backend", "operator",
		[]string{"operator.read", "operator.write"},
		1700000000123, "tok", "nonce", "linux", "",
	)

	sig := signV3Payload(priv, payload)

	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("signature is not base64url-no-padding: %v", err)
	}
	if !ed25519.Verify(pub, []byte(payload), raw) {
		t.Fatalf("signature failed to verify against public key")
	}
}

// --- 2. Init mode detection -------------------------------------------------

func TestInit_JSONBlob_SelectsDeviceAuthMode(t *testing.T) {
	pub, priv := newTestKey(t)
	o := &OpenClawHarness{}

	err := o.Init(map[string]any{
		"base_url":   "https://crawbot.example.ts.net",
		"auth_token": deviceBlob(t, deriveDeviceID(pub), priv, "dtok-123"),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if o.identity == nil {
		t.Fatal("expected device-auth mode (identity set), got nil")
	}
	if o.authToken != "" {
		t.Fatalf("authToken should be empty in device-auth mode, got %q", o.authToken)
	}
	if o.identity.deviceToken != "dtok-123" {
		t.Fatalf("deviceToken = %q, want dtok-123", o.identity.deviceToken)
	}
	if o.identity.deviceID != deriveDeviceID(pub) {
		t.Fatalf("identity.deviceID = %q, want %q", o.identity.deviceID, deriveDeviceID(pub))
	}
}

func TestInit_PlainToken_SelectsLegacyMode(t *testing.T) {
	o := &OpenClawHarness{}

	err := o.Init(map[string]any{
		"base_url":   "https://gw.example.com",
		"auth_token": "plain-shared-token",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if o.identity != nil {
		t.Fatal("plain token must not enable device-auth mode")
	}
	if o.authToken != "plain-shared-token" {
		t.Fatalf("authToken = %q, want plain-shared-token", o.authToken)
	}
}

func TestInit_EmptyToken_NoAuth(t *testing.T) {
	o := &OpenClawHarness{}

	err := o.Init(map[string]any{"base_url": "https://gw.example.com"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if o.identity != nil {
		t.Fatal("no token must not enable device-auth mode")
	}
	if o.authToken != "" {
		t.Fatalf("authToken = %q, want empty", o.authToken)
	}
}

func TestInit_DeviceBlobMalformedKey_Errors(t *testing.T) {
	// A JSON blob that clearly intends device-auth (has deviceId + privateKeyPem)
	// but carries a broken PEM must surface a hard error, not silently downgrade.
	blob := `{"deviceId":"abc","privateKeyPem":"-----BEGIN PRIVATE KEY-----\nnotbase64\n-----END PRIVATE KEY-----\n","deviceToken":"t"}`
	o := &OpenClawHarness{}

	err := o.Init(map[string]any{
		"base_url":   "https://gw.example.com",
		"auth_token": blob,
	})
	if err == nil {
		t.Fatal("expected error for malformed device key, got nil")
	}
	if o.identity != nil {
		t.Fatal("identity must not be set when key is malformed")
	}
}

func TestParseDeviceIdentity_RecomputesMismatchedDeviceID(t *testing.T) {
	pub, priv := newTestKey(t)
	// Stored deviceId is deliberately wrong; the parser must trust the key, not the label.
	blob := deviceBlob(t, "0000deadbeef", priv, "tok")

	id, err := parseDeviceIdentity(blob)
	if err != nil {
		t.Fatalf("parseDeviceIdentity: %v", err)
	}
	if id.deviceID != deriveDeviceID(pub) {
		t.Fatalf("deviceID = %q, want recomputed %q", id.deviceID, deriveDeviceID(pub))
	}
}

func TestParseDeviceIdentity_PlainToken_NotCredential(t *testing.T) {
	_, err := parseDeviceIdentity("just-a-token")
	if !errors.Is(err, errNotDeviceCredential) {
		t.Fatalf("err = %v, want errNotDeviceCredential", err)
	}
}

// --- 3. Connect frame shape -------------------------------------------------

func TestBuildConnectParams_DeviceAuthFrameShape(t *testing.T) {
	pub, priv := newTestKey(t)
	id := &deviceIdentity{
		deviceID:    deriveDeviceID(pub),
		deviceToken: "device-tok",
		privKey:     priv,
		pubKey:      pub,
	}

	const signedAt = int64(1700000000123)
	const nonce = "nonce-xyz"
	params := id.buildConnectParams(signedAt, nonce)

	// Round-trip through JSON exactly as the wire frame would be encoded.
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal connect params: %v", err)
	}
	var frame struct {
		Client struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		} `json:"client"`
		Role   string   `json:"role"`
		Scopes []string `json:"scopes"`
		Auth   struct {
			DeviceToken string `json:"deviceToken"`
		} `json:"auth"`
		Device struct {
			ID        string `json:"id"`
			PublicKey string `json:"publicKey"`
			Signature string `json:"signature"`
			SignedAt  int64  `json:"signedAt"`
			Nonce     string `json:"nonce"`
		} `json:"device"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal connect frame: %v", err)
	}

	if frame.Client.ID != "gateway-client" {
		t.Errorf("client.id = %q, want gateway-client", frame.Client.ID)
	}
	if frame.Client.Mode != "backend" {
		t.Errorf("client.mode = %q, want backend", frame.Client.Mode)
	}
	if frame.Role != "operator" {
		t.Errorf("role = %q, want operator", frame.Role)
	}
	if strings.Join(frame.Scopes, ",") != "operator.read,operator.write" {
		t.Errorf("scopes = %v, want [operator.read operator.write]", frame.Scopes)
	}
	if frame.Auth.DeviceToken != "device-tok" {
		t.Errorf("auth.deviceToken = %q, want device-tok", frame.Auth.DeviceToken)
	}
	if frame.Device.ID != id.deviceID {
		t.Errorf("device.id = %q, want %q", frame.Device.ID, id.deviceID)
	}
	if frame.Device.SignedAt != signedAt {
		t.Errorf("device.signedAt = %d, want %d", frame.Device.SignedAt, signedAt)
	}
	if frame.Device.Nonce != nonce {
		t.Errorf("device.nonce = %q, want %q", frame.Device.Nonce, nonce)
	}

	// publicKey must be the raw 32 bytes, base64url, no padding.
	rawPub, err := base64.RawURLEncoding.DecodeString(frame.Device.PublicKey)
	if err != nil {
		t.Fatalf("device.publicKey not base64url-no-padding: %v", err)
	}
	if len(rawPub) != ed25519.PublicKeySize {
		t.Fatalf("device.publicKey decoded to %d bytes, want %d", len(rawPub), ed25519.PublicKeySize)
	}
	if !strings.EqualFold(hex.EncodeToString(sha256Sum(rawPub)), id.deviceID) {
		t.Fatalf("deviceId is not sha256(publicKey)")
	}

	// signature must verify against the v3 payload the gateway will rebuild.
	wantPayload := buildV3Payload(
		id.deviceID, "gateway-client", "backend", "operator",
		[]string{"operator.read", "operator.write"},
		signedAt, "device-tok", nonce, "linux", "",
	)
	sig, err := base64.RawURLEncoding.DecodeString(frame.Device.Signature)
	if err != nil {
		t.Fatalf("device.signature not base64url-no-padding: %v", err)
	}
	if !ed25519.Verify(pub, []byte(wantPayload), sig) {
		t.Fatalf("device.signature does not verify over the v3 payload")
	}
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

func TestParseHelloOKPayloadVersion_UsesServerVersion(t *testing.T) {
	payload := json.RawMessage(`{"type":"hello-ok","protocol":4,"server":{"version":"2026.6.1"}}`)

	version, msg := parseHelloOKPayloadVersion(payload)
	if msg != "" {
		t.Fatalf("parseHelloOKPayloadVersion msg = %q, want empty", msg)
	}
	if version != "2026.6.1" {
		t.Fatalf("version = %q, want 2026.6.1", version)
	}
}

// --- 4. Streaming decode ----------------------------------------------------

func TestChatStreamDecoder_DeltaThenFinal_EmitsPongThenDone(t *testing.T) {
	const runID = "run-1"
	dec := &chatStreamDecoder{runID: runID}

	// Frame shapes mirror PROTOCOL.md §Streaming events and refclient.py output.
	delta := json.RawMessage(fmt.Sprintf(
		`{"runId":%q,"sessionKey":"aos-1","seq":1,"state":"delta","deltaText":"PONG","message":{"role":"assistant","content":[{"type":"text","text":"PONG"}]}}`,
		runID))
	final := json.RawMessage(fmt.Sprintf(
		`{"runId":%q,"state":"final","message":{"role":"assistant","content":[{"type":"text","text":"PONG"}]}}`,
		runID))

	chunks, done, err := dec.handle(delta)
	if err != nil {
		t.Fatalf("handle(delta): %v", err)
	}
	if done {
		t.Fatal("delta should not be terminal")
	}
	if len(chunks) != 1 || chunks[0].Content != "PONG" || chunks[0].Done {
		t.Fatalf("delta chunks = %+v, want one Content:PONG", chunks)
	}

	chunks, done, err = dec.handle(final)
	if err != nil {
		t.Fatalf("handle(final): %v", err)
	}
	if !done {
		t.Fatal("final should be terminal")
	}
	// "PONG" was already sent via delta, so final emits only Done:true.
	if len(chunks) != 1 || !chunks[0].Done || chunks[0].Content != "" {
		t.Fatalf("final chunks = %+v, want one Done:true", chunks)
	}
}

func TestChatStreamDecoder_FinalWithoutDelta_EmitsFullThenDone(t *testing.T) {
	dec := &chatStreamDecoder{runID: "r"}
	final := json.RawMessage(
		`{"runId":"r","state":"final","message":{"role":"assistant","content":[{"type":"text","text":"PONG"}]}}`)

	chunks, done, err := dec.handle(final)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !done {
		t.Fatal("final should be terminal")
	}
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks (text + done), got %+v", chunks)
	}
	if chunks[0].Content != "PONG" {
		t.Errorf("chunks[0].Content = %q, want PONG", chunks[0].Content)
	}
	if !chunks[1].Done {
		t.Errorf("chunks[1] should be Done:true, got %+v", chunks[1])
	}
}

func TestChatStreamDecoder_IgnoresUnrelatedRunID(t *testing.T) {
	dec := &chatStreamDecoder{runID: "mine"}
	other := json.RawMessage(`{"runId":"theirs","state":"delta","deltaText":"nope"}`)

	chunks, done, err := dec.handle(other)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if done || len(chunks) != 0 {
		t.Fatalf("unrelated runId should be ignored, got chunks=%+v done=%v", chunks, done)
	}
}

func TestChatStreamDecoder_MultiPartFinalConcatenatesText(t *testing.T) {
	dec := &chatStreamDecoder{runID: "r"}
	final := json.RawMessage(
		`{"runId":"r","state":"final","message":{"role":"assistant","content":[{"type":"text","text":"PO"},{"type":"text","text":"NG"}]}}`)

	chunks, done, err := dec.handle(final)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !done || len(chunks) != 2 || chunks[0].Content != "PONG" {
		t.Fatalf("want concatenated PONG + done, got %+v", chunks)
	}
}
