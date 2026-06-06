# OpenClaw Device-Auth Protocol — verified reference for AgentOS harness migration

**Status:** Protocol reverse-engineered from crawbot's compiled gateway (openclaw `2026.6.1`,
running on hpms1 under user `crawbot`, rootless podman container `openclaw`) and **proven
end-to-end** with a working reference client (`scripts/refclient.py`) that completed a full
`chat.send` round-trip and received "PONG" back from the live agent.

This document is the build spec for migrating `internal/harness/openclaw.go` off the obsolete
shared-token webchat path onto device-identity auth + the `chat.send` text API.

---

## Why the current harness fails (three independent bugs)

The deployed harness (`internal/harness/openclaw.go`, `buildConnectParams` + `runChat`) does:

1. **Shared-token webchat connect** — `client.id/mode = "webchat"` + `auth.token`. The upgraded
   gateway hard-caps a token-only, no-device-identity, webchat-classified connection arriving over
   the tailnet (proxied, non-loopback) at `operator.read`. `chat.send`/`talk.*` need `operator.write`
   → `missing scope: operator.write`. **Auth model is obsolete.**
2. **Wrong RPC for text chat** — harness calls `talk.session.create` with `mode:"text"`. In this
   gateway `talk.*` is the **voice/realtime** API; valid modes are `realtime|stt-tts|transcription`
   (no `"text"`). Text chat is a different method: **`chat.send`**.
3. **Wrong streaming events** — harness listens for `talk.event` / `output.text.delta`. The text
   chat stream emits **`chat`** and **`agent`** events (see Streaming below).

All three are fixed by this migration.

---

## Device identity

- Ed25519 keypair.
- `deviceId` = `sha256(raw 32-byte public key)` as lowercase hex.
- Public key on the wire = **raw 32 bytes, base64url, no padding** (NOT PEM, NOT SPKI).
- Private key used only locally to sign the connect challenge.

Go: `crypto/ed25519`. `ed25519.GenerateKey` → `pub` is the raw 32 bytes already.
`deviceId = hex(sha256(pub))`. Wire pubkey = `base64.RawURLEncoding.EncodeToString(pub)`.

## Handshake (exact wire sequence)

WS endpoint: `wss://<base_url_host>/ws` (TLS verify skipped for the tailnet cert, as today).

1. **Open WS.** Gateway emits an event `{"type":"event","event":"connect.challenge","payload":{"nonce":"<uuid>"}}`.
   Wait up to ~3s for it; capture `nonce`. (If absent, nonce stays `""` — but crawbot always sends one.)
2. **Build the v3 signature payload** — a single pipe-joined string, field order is exact:

   ```
   v3|<deviceId>|<clientId>|<clientMode>|<role>|<scopesCSV>|<signedAtMs>|<token>|<nonce>|<platform>|<deviceFamily>
   ```

   - `clientId` MUST be from the gateway's closed enum. Use **`gateway-client`**.
   - `clientMode` use **`backend`**.
   - `role` = `operator`.
   - `scopesCSV` = `operator.read,operator.write` (comma-joined, **in the same order** you send in `scopes`).
   - `signedAtMs` = `Date.now()` ms (gateway rejects skew > DEVICE_SIGNATURE_SKEW_MS; keep it fresh).
   - `token` = the device token (the `auth.deviceToken` value). Empty string if none.
   - `nonce` = the challenge nonce.
   - `platform` = `linux`, lowercased; `deviceFamily` = `""`.
   - **`platform` and `deviceFamily` are normalized to lowercase** by the gateway before verifying,
     so sign them lowercased.

   Gateway tries v3 first, then a v2 fallback (same string without the trailing
   `|platform|deviceFamily` and `v2` prefix). Implement **v3**.
3. **Sign** the payload bytes (UTF-8) with the Ed25519 private key. Signature on the wire =
   **base64url, no padding**.
4. **Send `connect`** request:

   ```json
   {"type":"req","id":"c1","method":"connect","params":{
     "minProtocol":4,"maxProtocol":4,
     "client":{"id":"gateway-client","version":"<v>","platform":"linux","mode":"backend"},
     "role":"operator",
     "scopes":["operator.read","operator.write"],
     "caps":["tool-events"],
     "auth":{"deviceToken":"<deviceToken>"},
     "device":{
       "id":"<deviceId>",
       "publicKey":"<rawPubBase64Url>",
       "signature":"<sigBase64Url>",
       "signedAt":<signedAtMs>,
       "nonce":"<nonce>"
     }
   }}
   ```

   Success → `{"type":"res","id":"c1","ok":true,"payload":{"type":"hello-ok",...}}`.

### Gateway-side device-auth checks (so the client matches them)
- `deriveDeviceIdFromPublicKey(device.publicKey) === device.id` (sha256 of raw pubkey).
- `device.signedAt` within signature skew window.
- `device.nonce` non-empty AND equals the issued challenge nonce.
- `verifyDeviceSignature(publicKey, payloadV3, signature)` passes.
- `auth.deviceToken` verified via `verifyDeviceToken` against the paired device's `tokens.operator.token`,
  and requested scopes within the device's `approvedScopes` baseline.

## Text chat — `chat.send` (NOT talk.*)

After `hello-ok`:

```json
{"type":"req","id":"m1","method":"chat.send","params":{
  "sessionKey":"<stable-per-conversation key>",
  "message":"<user text>",
  "idempotencyKey":"<uuid runId>"
}}
```

Optional params seen in the gateway client: `agentId`, `sessionId`, `thinking`, `deliver`, `timeoutMs`.
Minimal working set is `sessionKey` + `message` + `idempotencyKey`. Response:
`{"ok":true,"payload":{"runId":"<uuid>","status":"started"}}`. The reply then **streams as events**.

`chat.send` requires `operator.write` (confirmed in `core-descriptors`).

### Streaming events (the reply)
Two event channels carry the assistant text; pick ONE and stick to it. **`chat` is cleanest:**

- `event:"chat"` payloads:
  - `{ "runId", "sessionKey", "seq", "state":"delta", "deltaText":"PONG",
      "message":{"role":"assistant","content":[{"type":"text","text":"PONG"}]} }`
  - terminal: `{ "state":"final", "message":{"role":"assistant","content":[{"type":"text","text":"<full>"}]} }`
- `event:"agent"` payloads (parallel, finer-grained):
  - `{ "runId", "stream":"lifecycle", "data":{"phase":"start"} }`
  - `{ "runId", "stream":"assistant", "data":{"text":"PONG","delta":"PONG"} }`
  - `{ "runId", "stream":"lifecycle", "data":{"phase":"end"} }`

Recommended harness mapping:
- Accumulate from `chat` events: on `state:"delta"` emit `deltaText` as a `ChatChunk{Content}`.
- On `state:"final"` emit any remaining text and `ChatChunk{Done:true}`.
- Match by `runId` from the `chat.send` response to ignore unrelated sessions' events.
- Keep a hard timeout (the existing 120s) as a backstop.

Other ambient events to ignore: `tick`, `health`.

## Valid client id / mode enums (gateway rejects anything else)
IDs: `webchat-ui, openclaw-control-ui, openclaw-tui, webchat, cli, gateway-client, openclaw-macos,
openclaw-ios, openclaw-android, node-host, test, fingerprint, openclaw-probe`.
Modes: `webchat, cli, ui, backend, node, probe, test`.
Use **`gateway-client` / `backend`**.

---

## Config plumbing (how the harness receives the identity)

`buildHarnessConfig` (router.go:85) injects the **first granted credential** as `config["auth_token"]`
for openclaw agents, and a legacy metadata token as fallback. To avoid any router/schema change, the
device-auth material is delivered **as a single JSON credential blob** in that same `auth_token` slot:

```json
{"deviceId":"<hex>","privateKeyPem":"<pkcs8 pem>","deviceToken":"<base64url>"}
```

`OpenClawHarness.Init` must:
1. Read `config["auth_token"]` (string) and `config["base_url"]`.
2. If the string **parses as JSON with a `deviceId` + `privateKeyPem`** → device-auth mode.
3. Else → legacy plain-token mode (keep the old shared-token connect for back-compat / other gateways).

This keeps `buildHarnessConfig` untouched and is backward compatible: existing agents with a plain
token still work; crawbot gets the JSON blob.

(The private key is stored encrypted at rest in the vault exactly like any other credential secret;
it is only decrypted at request time inside `buildHarnessConfig` → `Init`. Never log it.)

---

## Gateway-side pairing (done by the operator, not in AgentOS code)

A device must be **paired with `operator.write`** before its token authenticates. This is a one-time
admin action performed on hpms1 (already done for the proven identity; see `scripts/seed_device.py`).
The paired-device record (in `/home/crawbot/.openclaw/devices/paired.json`) carries
`approvedScopes:["operator.read","operator.write"]` and `tokens.operator.token`. `verifyDeviceToken`
re-reads this file per connection, so no gateway restart is needed.

The harness does NOT perform pairing or bootstrap-token redemption at runtime — it presents an
already-paired identity. (Bootstrap/QR onboarding exists in the gateway but is out of scope here.)

---

## Proven reference

`scripts/refclient.py` (Python, in this folder) implements the above and was run live against
`wss://crawbot.taila4541b.ts.net/ws`:

```
connect res: ok:true hello-ok protocol:4 server.version:2026.6.1
chat.send res: ok:true {runId, status:"started"}
event=agent stream:assistant data.text:"PONG"
event=chat  state:delta deltaText:"PONG"  → state:final message.content[].text:"PONG"
```

The Go harness must reproduce this exact behavior. Treat `refclient.py` as the executable spec.
