# TASK: Migrate the OpenClaw harness to device-identity auth + chat.send

You are implementing a backend change in this Go repo (AgentOS). Work ONLY in this worktree
(`feat/openclaw-device-auth`). Do not touch other worktrees or push to main.

## Context — what's broken and why
The `openclaw` harness (`internal/harness/openclaw.go`) talks to the "crawbot" agent's OpenClaw
gateway. The gateway was upgraded to a **device-identity pairing** security model. The current
harness uses three obsolete things and is fully non-functional against the live gateway:

1. It connects as a shared-token `webchat` client → the gateway caps that path at `operator.read`,
   so chat fails with `missing scope: operator.write`.
2. It calls `talk.session.create` with `mode:"text"` → `talk.*` is the *voice* API in this gateway;
   text chat is a different method, **`chat.send`**.
3. It listens for `talk.event`/`output.text.delta` → text replies actually stream on **`chat`** and
   **`agent`** events.

All three are fixed by this task.

## The spec is PROVEN and executable
Read these two files FIRST — they are authoritative:
- `docs/crawbot-deviceauth/PROTOCOL.md` — full wire protocol, field-by-field, with the exact
  v3 signature payload, connect params, `chat.send` params, and streaming event shapes.
- `docs/crawbot-deviceauth/refclient.py` — a Python reference client that **actually completed a
  live `chat.send` round-trip and received "PONG"** from crawbot. Treat it as the executable spec.
  Your Go code must reproduce its exact behavior.

## What to implement (in `internal/harness/openclaw.go`)

### 1. Dual-mode `Init`
`config["auth_token"]` may now be EITHER:
- a plain string token (legacy / other gateways) → keep the existing shared-token connect path, OR
- a JSON blob `{"deviceId","privateKeyPem","deviceToken"}` → device-auth mode.

Detect by attempting to `json.Unmarshal` the string into a struct with those fields; if it has a
non-empty `deviceId` and `privateKeyPem`, use device-auth mode. Otherwise legacy. Store the parsed
identity on the harness struct. NEVER log `privateKeyPem` or `deviceToken`.

### 2. Device-auth connect (when in device-auth mode)
Implement the handshake from PROTOCOL.md §Handshake:
- On WS open, wait up to ~3s for a `connect.challenge` event; capture `payload.nonce`.
- Build the v3 payload string EXACTLY (pipe-joined, field order, lowercased platform/deviceFamily):
  `v3|deviceId|gateway-client|backend|operator|operator.read,operator.write|<signedAtMs>|<deviceToken>|<nonce>|linux|`
  (trailing empty deviceFamily).
- Sign payload bytes with Ed25519 private key (`crypto/ed25519`; parse the PKCS8 PEM via
  `crypto/x509`). Signature → base64.RawURLEncoding.
- Send `connect` with `client.id="gateway-client"`, `client.mode="backend"`, `role="operator"`,
  `scopes=["operator.read","operator.write"]`, `caps=["tool-events"]`, `auth.deviceToken`, and the
  `device{id,publicKey(raw 32B base64url),signature,signedAt,nonce}` object.
- `deviceId` must equal `hex(sha256(rawPubKey))` — derive the raw pubkey from the private key so the
  client is self-consistent (don't trust a mismatched stored deviceId; recompute and assert).
- Expect `res ok:true` with `payload.type=="hello-ok"`. On `ok:false`, surface the error message.

### 3. Text chat via `chat.send`
Replace the `talk.session.create`/`talk.session.steer` flow (device-auth mode) with:
- `chat.send` params `{sessionKey, message, idempotencyKey}` (idempotencyKey = a fresh UUID = runId).
- Use a stable per-Chat-call `sessionKey` (e.g. `aos-<unixnano>` is fine; the harness is stateless
  per call today — preserve that).
- Capture `runId` from the `chat.send` response.

### 4. Streaming
Consume `chat` events matched by `runId`:
- `state:"delta"` → emit `ChatChunk{Content: payload.deltaText}`.
- `state:"final"` → emit any trailing text from `message.content[].text` not already sent, then
  `ChatChunk{Done:true}` and return.
- Keep the existing 120s backstop timeout and ctx cancellation.
- Ignore `tick`, `health`, and `agent` events for text (the `chat` channel is sufficient and cleaner).
  (You MAY use `agent` `stream:"assistant"` `data.delta` instead, but pick ONE; PROTOCOL.md recommends `chat`.)

### Legacy path
Keep the existing shared-token webchat connect + `talk.*` flow intact for the non-JSON token case so
back-compat and other gateways are unaffected. Factor cleanly (e.g. `runChatDeviceAuth` vs
`runChatLegacy`) — do not regress the legacy path.

## Tests (REQUIRED — this is the gate)
Add `internal/harness/openclaw_test.go` with table/unit tests that do NOT require a live gateway:
1. **Identity/signature**: given a known Ed25519 key, assert `deviceId == hex(sha256(pub))` and that
   the v3 payload string is built with exact field order/lowercasing, and that a produced signature
   verifies against the public key (`ed25519.Verify`).
2. **Init mode detection**: JSON blob → device-auth mode; plain string → legacy mode; empty → no auth.
3. **Connect frame shape**: build the connect params for device-auth mode and assert the JSON has
   `client.id=="gateway-client"`, `mode=="backend"`, `auth.deviceToken` set, and a well-formed
   `device` object with raw-base64url publicKey and a verifiable signature over the v3 payload.
4. **Streaming decode**: feed synthetic `chat` event frames (`delta` then `final`) into the response
   handler and assert the emitted `ChatChunk`s are `Content:"PONG"` then `Done:true`.
   Mirror the payloads in PROTOCOL.md / refclient.py output exactly.

Prefer testing pure helpers (extract payload-building, signing, and event-decoding into functions you
can unit-test without a socket). A full WS integration test is NOT required (no gateway in CI).

## Definition of done (Gate 1 — you must run these and paste real output)
- `gofmt -l internal/harness/openclaw.go internal/harness/openclaw_test.go` → empty (no diff).
- `go vet ./internal/harness/...` → clean.
- `go build ./...` → clean.
- `go test ./internal/harness/...` → PASS, with your new tests present and passing.
- The web build is unaffected (no TS changes expected).

Do NOT commit secrets or the `_crawbot-deviceauth/` scratch dir. Only `internal/harness/openclaw.go`,
`internal/harness/openclaw_test.go`, and `docs/crawbot-deviceauth/*` should change.

Report back: the unified diff of `openclaw.go`, the new test file, and the literal output of the
four Gate-1 commands above. Do not claim success without pasting real `go test` output.
