# Work-Event Contract (v1.1 — FROZEN 2026-05-30)

> The single contract every emitter, every API handler, and every UI component codes
> to. **FROZEN** — changes are Lead-only and must be re-broadcast to all open WP
> issues (orchestration-plan O7) with a version bump (`schema` → `/v2`). Realizes
> [[ADR-001]] D2 (the unit of record is the work-event).

Doc version: v1.1 (FROZEN) · Date: 2026-05-30 · Owner: Lead (integrator)

Freeze note: v1.1 describes BOTH the full host-reporter liveness path AND the
degradation backstop, so freezing does not force the lean-vs-full build decision —
that is a WP-N build-scope call at Wave 2, not a contract change either way.

Changelog v1→v1.1 (adversarial review fixes): liveness computed on server clock only;
required `host`/`pid` for crash proof; absorbing terminal state; `liveness_mode`
promoted to top-level; conditional `status`; required idempotent `event_id`; composite
`(harness, session_id)` identity; per-tenant ingest key; artifact path/url sandboxing;
defined `server.*` semantics; dropped `status.changed`.

---

## 0. Why this exists

Heterogeneous agents (Hermes, Claude Code, `agy`) emit a **uniform work-event** into
Agent OS. Agent OS never drives the agents; it ingests + correlates. Every emitter and
consumer depends on the shapes below being stable. If a field isn't here, an emitter
must not invent it; if a consumer needs a new field, that's a Lead-gated contract change.

---

## 1. Ingestion endpoint

```
POST /api/events/work
Content-Type: application/json
X-AgentOS-Ingest-Key: <per-tenant ingest key>     # Tailscale-only network
Idempotency-Key: <event_id uuid>                  # echoes body.event_id
```

- **Auth binds tenant to credential.** Each tenant (`personal`, `dayjob`, `<employer>`)
  has its own ingest key. The server resolves the key → an allowed tenant set and
  **rejects (`403`) any event whose `tenant` is not permitted for that key**. This is
  what makes [[ADR-002]] D6 (employer tenants never co-mingle) a *mechanism*, not an
  honor-system assertion. Tailscale ACLs are defense-in-depth, not the boundary.
- **Idempotent at-least-once ingest.** POST may be retried. Ingest is **upsert by
  `event_id`** (unique): a duplicate returns the original row's `id` with `202`, never a
  second row. Blunts replay-induced duplication.
- **Validation is strict but never silently drops** a *well-formed* event ([[ADR-001]]
  fail-mode): a malformed event returns `400` with a JSON reason **and is logged**. A
  well-formed event that can't be correlated is still **persisted** (correlation is
  best-effort, done later by WP-B) — never rejected for lacking `external_ref`/`branch`.
- Response: `201` `{ "id": "<uuid>", "accepted": true }` (new) / `202` (idempotent dup).

### Validation rules (make illegal states unrepresentable)
A violation is `400` with reason, logged — never silently coerced:
- **`status` is conditional, not blanket-required.** Required only for `session.start`,
  `session.heartbeat`, `session.end`. For `artifact.created` / `server.started` /
  `server.stopped` / `note` it MUST be absent or `unknown` (rejected if `running` etc.).
- `kind: session.end` ⇒ `status` ∈ {`done`,`failed`,`cancelled`} (terminal).
- `kind: session.start`/`session.heartbeat` ⇒ `status` ∈ {`running`,`unknown`}.
- `kind: session.start` ⇒ `liveness_mode` present (§4); `session.heartbeat` ⇒
  `liveness_mode: supervised` (bounded emitters cannot heartbeat).
- `kind: session.heartbeat`/`session.end` for a `(harness, session_id)` that already has
  a terminal event are **accepted, stored, but inert for liveness** (terminal is
  absorbing — §4). No resurrection.
- artifact: exactly one of `path`|`url`; `path` must canonicalize under the artifact root
  (no traversal); `url` must not resolve to a private/link-local range (SSRF guard) — §3.
- `event_id` is a required UUID; `host` is required; `pid` required when
  `liveness_mode: supervised`.
- `ts` must be within ±10 min of server time (clock-skew guard); out of range ⇒ `400`.
- `cost_usd` ≥ 0 when present.
- `external_ref`, when present, should match `^(SC-\d+|#\d+)$` — **warn + store, do not
  reject** (best-effort correlation; a malformed ref just won't join).
- `payload` capped at 64 KB.

### Back-compat shim
`POST /api/delegations` (existing Hermes bridge) keeps working: WP-A reworks it to
translate the legacy delegation payload into a work-event (synthesizing `event_id`,
`host`, `liveness_mode: bounded`) and forward internally. Old callers see no change.

---

## 2. The work-event (request body emitters send)

```jsonc
{
  "schema": "agentos.work_event/v1",        // REQUIRED, literal — version gate
  "event_id": "b1e2…",                       // REQUIRED uuid — idempotency key
  "harness": "claude",                       // REQUIRED enum, see §4
  "session_id": "75e2167f-…",                // REQUIRED, stable per agent session
  "host": "zbook",                           // REQUIRED hostname (liveness keying)
  "kind": "session.end",                     // REQUIRED enum, see §4
  "ts": "2026-05-30T17:40:00Z",              // REQUIRED RFC3339 UTC (display/order; ±10m of server)

  // --- session-lifecycle fields ---
  "status": "done",                          // REQUIRED for session.*/ see §1 validation
  "liveness_mode": "supervised",             // REQUIRED on session.start; supervised|bounded (§4)
  "pid": 4163388,                            // REQUIRED when liveness_mode=supervised

  // --- correlation hints (all OPTIONAL; best-effort join, never required) ---
  "project_hint": "agent-os",                // repo/dir name or slug; resolver maps→project_id
  "external_ref": "SC-91130",                // "SC-<n>" (shortcut) | "#<n>" (gh issue)
  "branch": "wp-g/issue-42-spog-timeline",
  "sha": "46d5075",
  "cwd": "/home/tim/work/riftwing/sc-91130",

  // --- optional context ---
  "tenant": "personal",                      // enum §4; MUST be permitted by the ingest key (§1)
  "title": "SPOG timeline UI",
  "artifacts": [ { "type": "image", "path": "/data/artifacts/x.png", "name": "before.png" } ],
  "cost_usd": 0.0787,                         // OPTIONAL; cumulative for the session; non-decreasing
  "payload": { "telemetry": { /* §5 */ }, "…": "emitter extras (kept verbatim, not core-interpreted)" }
}
```

Rules:
- Unknown top-level keys are rejected (`400`) to catch typos; emitter extras go under
  `payload` (free-form, preserved verbatim, **never** the home of a field core interprets).
- `schema` must equal `agentos.work_event/v1`; other versions `400` until a Lead-gated bump.
- **Session identity is the composite `(harness, session_id)`** everywhere — never
  `session_id` alone (cross-harness collision would merge two agents into one phantom).

---

## 3. Artifact descriptor

```jsonc
{
  "type": "image",              // enum: image | video | audio | code | text | url | other
  "path": "/data/artifacts/x.png",   // server-reachable path UNDER the artifact root, OR
  "url":  "https://…",          // …a URL (exactly one of path|url)
  "name": "before.png",         // OPTIONAL display name
  "mime": "image/png"           // OPTIONAL
}
```
- **`path`** must canonicalize under the configured artifact root (default `/data/artifacts`);
  traversal / absolute paths outside it are rejected (`400`) — prevents arbitrary host-file
  read when the UI fetches content.
- **`url`** is fetched only via an allowlist/proxy that blocks private + link-local ranges
  (SSRF guard); or rendered client-side. Inline rendering (chat WP-Q, lineage WP-O) uses
  **actual content** ([[ADR-004]] D5), so these guards are mandatory, not optional.

---

## 4. Enums + liveness (frozen vocab + the anti-fake-status core)

| Field | Allowed values | Notes |
|-------|----------------|-------|
| `harness` | `hermes` `claude` `antigravity` `codex` `generic` | new harness = add value (Lead-gated, additive) |
| `kind` | `session.start` `session.heartbeat` `session.end` `artifact.created` `server.started` `server.stopped` `note` | (`status.changed` dropped in v1.1 — undefined semantics) |
| `status` | `running` `done` `failed` `cancelled` `unknown` | conditional per §1 validation |
| `liveness_mode` | `supervised` `bounded` | top-level; set on `session.start`, immutable for the session |
| `tenant` | `personal` `dayjob` `<employer-slug>` | MUST match the ingest key's allowed set (§1) |
| `tracker` (project) | `shortcut` `github_issues` `obsidian` `agent_os_native` | [[ADR-001]] D5 pluggable source |

### Liveness is a PURE FUNCTION of (persisted events, server clock now)
No in-memory timer is the authority — so a dashboard restart never loses or fakes state.
Derivation per `(harness, session_id)`:
```
terminal_seen := exists event kind=session.end (status terminal)
if terminal_seen           -> state = (that status); ABSORBING (later non-terminal events inert)
elif liveness_mode=supervised:
    last_hb := max(received_at where kind in {session.start,session.heartbeat})
    state = running if (now - last_hb) < liveness_timeout(5m) else stale
elif liveness_mode=bounded:
    # hooks can't heartbeat; clock timeout would false-stale long runs
    if host-process-reporter says (host,pid|session) alive -> running
    elif reporter says gone (and no session.end) -> stale (presumed crashed)
    else (no reporter coverage) -> stale once age > unprovable_grace, NEVER running without proof
    backstop: bounded_max_age (6h) -> stale
```
- **All windows use `received_at` (server clock) ONLY.** `ts` is emitter-supplied
  display/ordering metadata and is explicitly NOT used for liveness (clock-skew → fake
  status). Frozen invariant.
- **`running` requires positive proof**: a supervised heartbeat within timeout, or a host
  reporter confirming the process is alive. Absence of proof ⇒ `stale`, never "online"
  ([[ADR-001]] F10 / [[ADR-003]] D3).
- **Host process reporter** (WP-N, generalized): runs per tailnet host, POSTs
  process-liveness keyed by `(host, pid)` / cwd, so a *remote* bounded Claude crash is
  detected within a poll cycle — not the 6h backstop. `bounded_max_age` is only the
  last-resort ceiling when no reporter covers that host.

Kind → effect:
- `session.start` → open `(harness,session_id)`, `running`, record `liveness_mode`.
- `session.heartbeat` (supervised) → advances `last_hb` (inert if terminal already seen).
- `session.end` → close, terminal status (absorbing).
- `server.started` → upsert an app instance (WP-I) `up` with `host`/`cwd`/`branch`/`sha`.
- `server.stopped` → mark that instance `down`.
- `artifact.created` → attach artifact to the session/work-unit. `note` → annotation only.

### Known limitation (documented, accepted for v1)
If the **supervisor itself** crashes after the agent exits cleanly but before emitting
`session.end`, no terminal event arrives → the session is mislabeled `stale` indefinitely
(not fake-`running` — the cardinal sin is still avoided). Acceptable for v1; a future
supervised reaper can reconcile via the host reporter.

---

## 5. Telemetry sub-block (the Hermes-style status bar — WP-P)

Optional, at `payload.telemetry`. Every field independently optional; a missing field
renders `—`, **never fabricated** ([[ADR-004]] WP-P / F10).

```jsonc
"telemetry": {
  "model": "claude-opus-4-8",
  "context_window": 200000,
  "tokens_used": 142378,
  "turn_ms": 10276,
  "turns": 7
}
```
- **Cost has ONE source of truth: top-level `cost_usd`.** Telemetry carries no cost field
  (DRY). WP-P status bar and WP-K rollups both read `cost_usd`; "latest `received_at`
  wins" when reconciling, and per-session `cost_usd` is expected non-decreasing.
- Context % = `tokens_used / context_window`; UI colors green `<0.70`, amber `0.70–0.85`,
  red `>0.85`.
- **`liveness_mode` is NOT here** — it is a top-level field (§2/§4). `payload` never holds
  core-interpreted fields.

---

## 6. Persisted row (`work_events` table — migration 000014, informative)

| column | type | source |
|--------|------|--------|
| `id` | uuid pk | server |
| `event_id` | uuid UNIQUE | body (idempotency) |
| `schema_version` | text | from `schema` |
| `harness` | text | body |
| `session_id` | text | body |
| `host` | text | body |
| `pid` | int null | body (supervised) |
| `kind` | text | body |
| `status` | text null | body (conditional) |
| `liveness_mode` | text null | body (on session.* ) |
| `project_id` | uuid null | resolved from `project_hint`/`cwd` at ingest |
| `tenant` | text | body, validated against ingest key |
| `external_ref` | text null | body |
| `branch` | text null | body |
| `sha` | text null | body |
| `cwd` | text null | body |
| `title` | text null | body |
| `cost_usd` | numeric null | body |
| `payload` | jsonb | body.payload (incl. telemetry) |
| `ts` | timestamptz | body (display/order only) |
| `received_at` | timestamptz | server clock — **the only liveness clock** |

Indexes: UNIQUE(`event_id`); composite index on (`harness`,`session_id`,`received_at`)
for the liveness derivation. (`projects` gains `tracker` enum + `external_ref` + `tenant`
in migration 000015.)

---

## 7. Correlation key (WP-B) & SSE

- **Correlation key:** `project_id` + `external_ref` + `branch` + `sha`. A `work_unit`
  groups events sharing the key; events that join no tracker item / no key are
  `uncorrelated` (surfaced, never dropped — [[ADR-001]] F3).
- **SSE event names (frozen):** `work_event`, `work_unit_updated`, `instance_updated`,
  `session_updated`, `incident`. UIs subscribe via the existing `GET /api/events` bus.

---

## 8. What's frozen vs. extensible (ETC — separate things that change for different reasons)

Three layers, decoupled so a change to one doesn't churn the others:
- **Domain shape** (the WorkEvent's *meaning* — field semantics, enums, correlation key,
  liveness derivation): the stable policy. Frozen; change = Lead-gated version bump
  (`schema` → `agentos.work_event/v2`).
- **Wire binding** (HTTP endpoint, JSON encoding, auth headers): a detail; may evolve
  (new transport) without a domain bump, as long as it maps to the same domain shape.
- **Persistence** (`work_events` columns/indexes): an integrator-owned detail; may change
  for performance without touching emitters or the domain.

Emitters and UIs depend on the **domain shape only**. Core depends on a `TrackerSource`
interface (read-only: `List/Get` items + `synced_at`), with `shortcut` (WP-E) and
`github_issues` (WP-F) as substitutable implementations — adding a tracker is a new
implementation, never a core edit (DIP / [[ADR-001]] D5). The interface being read-only
*structurally* enforces "dashboard never writes trackers."

- **Frozen (domain — change = version bump):** top-level field names/types, `schema`
  literal, `event_id` idempotency, `(harness,session_id)` identity, enum *meanings*,
  conditional-`status` rules, liveness derivation + `received_at`-only clock + absorbing
  terminal, correlation key, SSE event names, telemetry field names, single-source cost,
  tenant-bound-to-key, artifact path/url sandboxing.
- **Additive without a bump:** new `harness`/`kind`/`tenant`/`tracker` enum *values*
  (append-only), new keys under `payload`, new `TrackerSource` implementations.
- **Never:** silently dropping a well-formed event; fabricating a telemetry value;
  asserting `running` without proof; emitting a contradictory kind/status; writing to an
  external tracker ([[ADR-001]] D4); placing a core-interpreted field in `payload`.
