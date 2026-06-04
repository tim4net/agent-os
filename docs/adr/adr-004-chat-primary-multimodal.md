---
title: "ADR-004 — Chat is the Primary Multimodal Surface; TUI is Fallback"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, chat, voice, mobile, artifacts, ghostty-web]
---

# ADR-004 — Chat is the Primary Multimodal Surface; TUI is Fallback

> Resolves the chat-vs-observability tension. Web chat is the primary way Tim
> interacts with agents — chosen for capabilities a terminal structurally lacks:
> voice, mobile, and rich graphical-artifact rendering. An embedded real terminal
> (ghostty-web) is an explicit *fallback*, not the primary path.
> Corrects [[projects/agent-os/adr-001-observability-plane|ADR-001]] D7 (which
> earlier over-reached into "demote chat").

**Status:** Accepted · **Date:** 2026-05-30 · **Decider:** Tim
**Build:** WP-Q / WP-R / WP-S in [[projects/agent-os/spog-project-plan|the SPOG Project Plan]].

---

## Context

Earlier framing drifted toward "demote chat," conflating two different things: the
chat **proxy ambition** (scraping a terminal agent's interactive TUI into a web box —
a fragile per-vendor adapter that rots) versus chat **as a surface** (talking to a
genuinely conversational agent). Tim's decision: **web chat is primary**, because it
delivers three things a TUI cannot — voice I/O, a clean mobile experience, and inline
rendering of graphical artifacts (screenshots, diagrams, images). A TUI is fine as a
fallback for full interactive control.

---

## Decision

### D1 — Web chat is the primary multimodal surface
Chat is the landing/primary surface. It is chosen specifically for voice, mobile, and
rich artifact rendering — capabilities the terminal structurally lacks. This is not a
regression to "chat-proxy as product"; it is a product decision about the best surface
for human↔agent interaction.

**Durability is part of why.** A web chat backed by persisted conversation state (DB +
session resume) survives app restarts, browser refreshes, and network disconnects — you
reconnect and the conversation is still there. A raw PTY/TUI session is fragile to
exactly these: the backend process dying or a dropped websocket can lose the session.
This is a structural reason chat is primary and TUI is fallback (and shapes D4).

### D2 — Drive agents via documented headless/streaming APIs, never TUI-scraping
The rotting trap was emulating a terminal agent's interactive UX. The stable path is
driving agents through their **official** non-interactive interfaces:
- **Hermes/Roux** — native API (already conversational). v1 web-chat target.
- **Claude Code** — `claude -p --output-format stream-json` + session resume (a stable,
  documented headless interface). Post-v1 web-chat target.
- **`agy`** — headless surface TBD (carried open question); web-chat only if it has a
  clean one, else TUI-fallback-only.
If an agent has no clean headless interface, it is **TUI-fallback-only**, never scraped.

### D3 — v1 scope: web chat for conversational agents; terminal agents observed + TUI
In v1, web chat is primary for the natively conversational agents (Hermes/Roux).
Terminal agents (Claude, `agy`) are already covered by the emitters (observability) and
the TUI fallback. Driving them through headless web chat (D2) is documented as post-v1,
so adapter work is **off the critical path**.

### D4 — TUI fallback via ghostty-web; read-only attach by default
An embedded real terminal (ghostty-web, already proven in `hermes-mc-dashboard`) is the
fallback for full interactive control the headless API doesn't expose (permission
dialogs, plan mode, slash commands, rewind).
- **Default: read-only attach** to an *already-running* session (e.g. `tmux attach -r`
  to a cockpit session). Compose with the fleet monitor: click a running session → watch
  its real TUI. **Prefer tmux-backed attach over a raw PTY** — tmux persists the session
  server-side, so a dropped websocket or dashboard restart re-attaches to the live
  session instead of killing it (the durability lesson from D1 applied to the fallback).
- **Interactive spawn** (type into a fresh PTY in a chosen worktree) is **opt-in and
  tenancy-gated** (ADR-002): off / restricted by default in a **dayjob** tenant, since a
  browser tab that can spawn a shell in a dayjob worktree is a real confidentiality and
  RCE surface. Tailscale-only mitigates but does not eliminate this.

### D5 — Inline artifact rendering is a hard requirement
Chat must render graphical artifacts inline — screenshots, diagrams, images, rendered
markdown — by fetching and displaying **actual content** from the artifact/event stream,
not the metadata-only preview that [[projects/agent-os/gap-analysis|Gap Analysis]] caught
before. This is the concrete payoff of "better representation of graphical artifacts."

### D6 — Voice and mobile are first-class but not free
- **Voice I/O** depends on a **verified working STT/TTS route**. Existing `voice.go` is
  untested and LiteLLM may not support Whisper/TTS. Voice is "verify the route, then
  wire," a conscious dependency — not assumed-working.
- **Responsive/mobile** layout is a requirement for the chat surface specifically (the
  dashboard is desktop-only today).

---

## Fail modes

| # | Fail mode | Guardrail |
|---|-----------|-----------|
| F1 | Rebuilding the rotting TUI-scraping adapter | D2 — only official headless/streaming interfaces; no clean interface ⇒ TUI-fallback-only |
| F2 | Silently losing interactive affordances in headless chat | D4 — TUI fallback covers the gap; chat shows a "need full control? open TUI" affordance |
| F3 | Claiming "voice works" untested | D6 — verify STT/TTS route before exposing voice; gap-analysis already flagged it |
| F4 | Artifacts shown as metadata, not rendered (the old gap) | D5 — fetch + render actual content inline |
| F5 | Dayjob worktree shell-spawn from a browser tab | D4 — interactive spawn opt-in + tenancy-gated; read-only attach default |
| F6 | Mobile claimed but desktop-only | D6 — responsive layout is explicit acceptance criteria |
| F7 | TUI fallback session lost on restart/disconnect | D1/D4 — chat state is DB-persisted; TUI fallback uses tmux-backed attach so the session survives server-side across reconnects |

---

## Consequences

**Positive:** the surface Tim actually wants to use (voice, mobile, artifacts); avoids
the adapter-rot trap by using official headless interfaces; TUI fallback preserves full
control without making it the primary path; the read-only-attach default neutralizes the
RCE/confidentiality fork that interactive-spawn would have forced.

**Negative / cost:** voice and inline-artifact rendering are real work with a real
dependency (STT/TTS route); responsive layout is new scope; the headless-chat path for
Claude/`agy` is genuinely deferred, so those agents are "observed + TUI" until then.

**Corrects:** ADR-001 D7's earlier "demote chat" over-reach — only the *proxy ambition*
is retired, chat itself is primary.

---

## Open questions (tracked, not blocking)

- **`agy` headless surface** (shared with ADR-001) — gates whether `agy` ever gets web
  chat vs TUI-only.
- **STT/TTS route** — does LiteLLM (or a direct provider) give a working Whisper/TTS
  path on Tim's stack? Verify before WP-R.
- **Interactive-spawn policy for personal tenant** — allowed freely, or always behind an
  explicit toggle even for personal worktrees?

See also: [[projects/agent-os/adr-001-observability-plane|ADR-001]] ·
[[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]] ·
[[projects/agent-os/adr-003-app-instance-registry|ADR-003]] ·
[[projects/agent-os/spog-project-plan|SPOG Project Plan]] ·
[[projects/agent-os/README|Agent OS]]
