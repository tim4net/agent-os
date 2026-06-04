---
title: "ADR-003 — App Instance Registry"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, registry, app-instances, observability, health]
---

# ADR-003 — App Instance Registry

> Visibility into the web apps Tim builds with this system: a tenant-scoped registry
> of dev/prod instances with real health, deploy correlation, and a launcher that
> opens them — without turning Agent OS into a browser.
> Extends [[projects/agent-os/adr-001-observability-plane|ADR-001]] (an app instance is
> another observed entity) and obeys [[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]]
> (tenant-scoped, no secrets).

**Status:** Accepted · **Date:** 2026-05-30 · **Decider:** Tim
**Build:** WP-I in [[projects/agent-os/spog-project-plan|the SPOG Project Plan]].

---

## Context

Tim builds web apps with this system (Vite/Go/Podman services on hpms1, ephemeral dev
servers, cloudflared quick-tunnels for Riftwing local stacks). Today there is no single
place that answers "what instances exist, are they up, what's deployed, where do I open
them?" He wants a registry of dev/prod instances, openable from within the OS.

Two prior lessons constrain the design:
- **Fake status is a known wound.** [[projects/agent-os/reality-check|Reality Check]]:
  agents always showed "online" because status was a DB flag, not a probe. The registry
  must not repeat this.
- **Don't rebuild someone else's UX.** [[projects/agent-os/adr-001-observability-plane|ADR-001]]
  rejected the chat-proxy/control-plane for the same reason an iframe-everything embed
  would fail: you reinvent the tool badly.

---

## Decision

### D1 — An app instance is a first-class observed entity
Registry row: `{ id, tenant, project_id, name, env(dev|staging|prod), url,
branch?, sha?, source(manual|emitted|discovered), embeddable(bool|unknown),
health(up|down|degraded|unknown), last_probed_at, last_deployed_at, notes }`.
It sits alongside work-units and tracker-items in the observability plane.

### D2 — "Open" = launch-in-tab by default; embed is opt-in best-effort
- Default action is a **deep link that opens in a new browser tab**. Agent OS is not a
  browser.
- **Embed (iframe) is opt-in per instance**, allowed only for instances Tim controls and
  has verified embeddable (`embeddable=true`). Self-hosted dev servers can set
  `frame-ancestors` to permit it; everything else stays launch-in-tab.
- The UI never *attempts* an embed for `embeddable=unknown/false` — it would just show a
  broken frame (X-Frame-Options / CSP / SameSite / mixed-content). It offers the tab link
  instead.

### D3 — Health is a REAL probe, never a DB flag
Health comes from an actual HTTP(S) check (configurable path, default `/` or a per-instance
health URL) on an interval, recording `health` + `last_probed_at`. No instance is shown
"up" without a successful recent probe. `unknown` is a valid, honest state for
never-probed/unreachable instances. This is the explicit anti-repeat of the fake-status
wound.

### D4 — Instances are observed, not just entered
- **Emitted:** a work-event that starts a server ("dev server at :5173 for branch X")
  auto-registers/updates the instance (`source=emitted`), carrying branch/sha for
  correlation. Consistent with the emitter model.
- **Discovered (optional, later):** opportunistic discovery of running Podman
  containers / listening dev ports on hpms1 (`source=discovered`).
- **Manual:** prod URLs typed once (`source=manual`).

### D5 — Deploy correlation is the value-add
Because instances carry `project + branch + sha`, the registry links to the work-units
that built what's running: "this dev instance is running SC-91130's branch, last deployed
by <agent>, story In Review." A bare URL list is not the point; the correlation is.

### D6 — Tenant-scoped, credentials never stored
- Every instance belongs to a tenant (ADR-002). Dayjob prod URLs are confidential payload:
  they live in the dayjob tenant and never surface in a personal/all view.
- The registry stores **URLs + metadata only — never credentials/tokens/cookies.** Probing
  uses unauthenticated health endpoints or Tailscale-network reachability, not stored auth.

---

## Fail modes

| # | Fail mode | Guardrail |
|---|-----------|-----------|
| F1 | **Fake health** (DB flag, always "up") | D3 — real HTTP probe + `last_probed_at`; `unknown` is honest |
| F2 | **Broken embeds** (X-Frame-Options/CSP/SameSite/mixed-content) | D2 — embed only when `embeddable=true`; default launch-in-tab; never attempt-and-fail |
| F3 | **Reinventing a browser** | D2 — launcher, not a render engine |
| F4 | **Dayjob prod URL leaks into personal/all view** | D6 — tenant-scoped; confidential URLs partitioned |
| F5 | **Storing prod credentials** | D6 — URLs + metadata only, never secrets |
| F6 | **Stale instances cluttering the registry** | TTL/auto-expire for `emitted`/`discovered` instances not seen in N probes; manual instances persist |
| F7 | **Health prober hammering prod** | Conservative interval, jitter, per-instance opt-out, timeout caps |

---

## Consequences

**Positive:** one honest glance at every app's state (huge for the Riftwing local-stack
workflow — is the stack up, what port, fresh tunnel?); deploy correlation ties running
instances back to the work that built them; fits the observability plane and tenancy model
with no new paradigm.

**Negative / cost:** the health prober is a live subsystem (intervals, timeouts, jitter) and
a real source of incidents if it hammers prod; embeddability verification is a manual
per-instance step; discovery (D4) is non-trivial and rightly deferred.

---

## Open questions (tracked, not blocking)

- **Probe reachability across tenants** — does the personal-tenant prober reach dayjob
  Tailscale endpoints, and *should* it? Default: probing is tenant-local.
- **cloudflared quick-tunnel freshness** — quick-tunnel URLs rotate; treat as `emitted`
  with short TTL and re-register on each tunnel start (ties into the existing Riftwing
  tunnel-debugging workflow).
- **Embeddability auto-detection** — could probe `X-Frame-Options`/CSP headers and set
  `embeddable` automatically instead of manual; nice-to-have, not v1.

See also: [[projects/agent-os/adr-001-observability-plane|ADR-001]] ·
[[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]] ·
[[projects/agent-os/spog-project-plan|SPOG Project Plan]] ·
[[projects/agent-os/README|Agent OS]]
