---
title: "ADR-001 — Agent OS is an Observability Plane, not a Control Plane"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, architecture, spog, observability]
---

# ADR-001 — Agent OS is an Observability Plane, not a Control Plane

> Decision record for the re-aim of Agent OS from "a dashboard you chat agents
> through" to "the single pane of glass that observes and correlates every
> agent's real work." Companion build plan: [[projects/agent-os/spog-project-plan|SPOG Project Plan]].

**Status:** Accepted · **Date:** 2026-05-30 · **Decider:** Tim
**Supersedes:** the chat-proxy / control-plane assumptions baked into the current
`internal/harness/*` adapters and the per-agent Chat tab.

---

## Context

Agent OS today is a chat-proxy: the dashboard drives agents through harness
adapters (`hermes`, `openclaw`, `litellm`, `generic`) and streams responses back.
[[projects/agent-os/reality-check|Reality Check]] established the result — a
pretty shell over a near-empty DB, because the real work happens in the agents'
native surfaces (Claude Code in a TTY/worktree, `agy` in a terminal, Hermes in
Telegram/CLI), not in the web chat box.

The goal Tim actually wants is a **SPOG**: one place to see, search, and correlate
all agent work — dayjob (Rewst/Riftwing, tracked in Shortcut) and personal
projects (tracked in GitHub Issues or Obsidian) — without re-implementing each
agent's UX and without standing up a competing tracker.

Grounding facts (verified 2026-05-30):
- `agy` (Antigravity CLI) **1.0.1 is installed locally**. Headless/JSON flag
  surface still needs a clean `agy --help` confirmation (see Open Questions).
- `claude` print mode emits JSON with `session_id` / `num_turns` / `total_cost_usd`,
  supports `Stop` / `SessionStart` hooks, and writes full transcripts to
  `~/.claude/projects/**/*.jsonl`. Its emitter is trivial.
- `internal/api/delegations.go` already accepts a free-form `metadata json.RawMessage`
  and broadcasts SSE on the event bus. This is the seam for a generic work-event
  emitter — no schema churn needed for v1.
- `rw` already stamps the SC story id into branch names → the correlation key
  largely exists for free.

---

## Decision

### D1 — Observability plane, not control plane
Agent OS **observes** agent work; it does not **drive** agents. Agents keep
running in their native surfaces. Their *work* (sessions, artifacts, commits, PRs,
state changes, cost) flows into Agent OS as a common event stream. Agent OS is
where you see/search/correlate — not a place you operate the agents from.

Opportunistic control is allowed later (one "kick a goal/cron" action), but
**never a per-vendor chat proxy**. The existing harness chat-proxy code is
demoted (see D7).

### D2 — The unit of record is the work-event, not the chat stream
Everything ingested is a typed event about a unit of work:
`{ harness, session_id, project, external_ref?, branch?, sha?, artifacts[], status, cost?, ts }`.
Chat transcripts are an artifact *type*, not the spine.

### D3 — Thin emitters per harness, never proxies
Each harness gets a small emitter that POSTs work-events to Agent OS. A new
harness = a new emitter ("appears in the sidebar") via a contract Agent OS owns,
not an adapter that breaks when a vendor ships.
- **Hermes** — generalize the existing `/api/delegations` + metadata into the
  work-event contract.
- **Claude Code** — `Stop`/`SessionStart` hook → POST, or tail `*.jsonl`.
- **Antigravity (`agy`)** — pending headless-flag confirmation; worst case tail
  its persistent-history store the way we tail Claude's jsonl.

### D4 — Trackers are READ-ONLY into Agent OS; agents write to the real tracker
Agent OS never writes to any external tracker. Models update stories themselves,
through their own tool access (Shortcut MCP/REST, `gh`), as part of doing the
task — that is the canonical write path and it does not race `rw` or parallel
Hermes sessions. Agent OS only **mirrors** tracker state for display + correlation.

This is consistent with the standing rule ("never auto-file a competing tracker"):
that rule forbids Agent OS standing up a *second* tracker, not agents touching the
*canonical* one.

### D5 — Pluggable tracker source per project (not Shortcut-specific)
Each project declares its `tracker`:
| `tracker` | Used by | Mirror behavior | Agent write path |
|-----------|---------|-----------------|------------------|
| `shortcut` | Rewst/Riftwing (dayjob) | read-only mirror via SC REST | agent's Shortcut MCP/REST |
| `github_issues` | personal repos (hermes-config, agent-os, mc-dashboard) | read-only mirror via GitHub API | agent's `gh` |
| `obsidian` | planning / notes-based projects | read project notes from vault | agents edit vault markdown |
| `agent_os_native` | projects with no external tracker | Agent OS owns kanban/goals (`owns_state`) | Agent OS UI |

This generalizes the original two-mode idea (`mirrors_external_tracker` vs
`owns_state`) so dayjob ticket data and personal-project tracking share one
ingestion abstraction. Shortcut is **source #1** to build because its join key
already exists.

### D6 — Correlation is the product
A pure tracker mirror shows "SC-91130 is In Review." The SPOG value-add is
attribution: *which agent* drove that change. Agent OS correlates the agent's
work-event (`branch` / `external_ref`) against the tracker's state change so a
Claude session + its SC story + its PR + its artifact surface as **one linked
work-unit**. Correlation key: `project + external_ref + branch + sha`.

### D7 — Retire the chat *proxy ambition*, keep chat *first-class*
The trap is reimplementing a terminal agent's UX in a web chat box (a per-vendor
translation layer that rots — ADR-001 D1). That ambition is retired. **Chat itself
stays a first-class, primary surface** — it is the landing view and the
command-line-to-your-fleet feel Tim wants — for the agents that are genuinely
conversational (Roux/Hermes), now enriched with the telemetry strip (WP-P) and
work-unit correlation. For terminal-native agents (Claude Code, `agy`, hermes CLI),
the answer is NOT a reimplemented chat box but an embedded **real terminal** via
ghostty-web (ADR-004) — the actual TUI, hosted, not proxied.

---

## Fail modes this design must respect

1. **Silent drop of SC-less / unstructured sessions.** Ad-hoc sessions not started
   via `rw` carry no `external_ref`; correlation is best-effort. **Show them
   uncorrelated, never drop them** — silent parser drops are a P1 anti-pattern.
2. **The mirror pretending to be the source.** Display must be visibly
   non-authoritative: "last synced <t>" + a link to the canonical story/issue.
   Re-creating the "is this real?" wound is the failure to avoid.
3. **A second writer racing the canonical tracker.** Prevented by D4 — Agent OS
   has no tracker write path at all in v1.
4. **Vendor-coupled emitters rotting.** Mitigated by D3 — the contract is owned by
   Agent OS; emitters are thin and replaceable; transcript-tailing is the
   universal fallback.
5. **Dayjob ticket data living on a personal box.** It's your hardware over
   Tailscale — acceptable, but this ADR records it as a *conscious* decision, not
   an accident.

---

## Consequences

**Positive:** plays to Agent OS's only real moat (deep integration on one data
model — the thing Julian Goldie structurally can't build, see
[[projects/agent-os/vs-julian-goldie|vs Julian Goldie]]); fills the empty schema
with *real* data instead of demo rows; no write-race risk; new agents/trackers are
additive.

**Negative / cost:** correlation logic is genuinely the hard part and is where
bugs will live; transcript-tailing emitters are slightly fragile to vendor format
changes; attribution quality is only as good as the join key hygiene (`rw`
branch-naming discipline).

**Retired:** chat-proxy as the product center; the assumption that "agent" == "a
thing you chat with in Agent OS."

---

## Open questions (tracked, not blocking)

- **`agy` headless surface.** Confirm `agy` has a non-interactive/JSON output mode
  for a clean emitter; else fall back to history-store tailing. (`agy` 1.0.1 is
  installed — this is a 5-minute `--help` spike, owned by WP-D in the plan.)
- **GitHub Issues correlation key.** Personal repos don't have `rw`'s SC-in-branch
  convention. Decide a lightweight convention (issue # in branch, or commit
  trailer `Refs: #N`) so personal-project attribution works too.
- **Obsidian-as-tracker semantics.** Read-only project-note mirror vs
  agent-writable — decide per project; default read-only.

See also: [[projects/agent-os/spog-project-plan|SPOG Project Plan]] ·
[[projects/agent-os/reality-check|Reality Check]] ·
[[projects/agent-os/vs-julian-goldie|vs Julian Goldie]] ·
[[projects/agent-os/README|Agent OS]]
