---
title: "ADR-002 — Tenancy & Knowledge Boundaries"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, tenancy, confidentiality, knowledge, multi-employer]
---

# ADR-002 — Tenancy & Knowledge Boundaries

> How dayjob, personal projects, and a possible $nightjob stay separate-but-accessible,
> and how lessons-learned cross between them without leaking confidential payload.
> Load-bearing for [[projects/agent-os/spog-project-plan|the SPOG build]] and for any
> future second-employer decision.

**Status:** Accepted · **Date:** 2026-05-30 · **Decider:** Tim
**Related:** [[projects/agent-os/adr-001-observability-plane|ADR-001]] (project-mode
is the data-layer expression of a tenant boundary).

---

## Context

Tim runs agent work across confidentiality domains that pull in opposite directions:
- **Dayjob** (Rewst/Riftwing): employer IP, customer data, Shortcut tickets, code —
  must stay isolated for legal/ethical reasons.
- **Personal projects** (agent-os, hermes-config, mc-dashboard): Tim's own IP.
- **Possible $nightjob**: a *second employer* — categorically different from a hobby
  repo because it introduces a second confidentiality wall plus potential
  conflict-of-interest / non-compete exposure, especially if it is also MSP
  automation.

The requirement is contradictory on its face: keep these **separate** (confidentiality)
yet **accessible** (one cockpit) yet allow **lessons-learned to cross** (compounding
craft). The resolution is to stop treating it as one problem.

---

## Decision

### D1 — Two kinds of knowledge, opposite rules
Separate by *kind*, not by *project*:
- **Payload** — secrets, client data, tickets, employer code, business logic,
  tenant-specific memory. **Hard-walled. Never crosses.**
- **Pattern** — generic craft: debugging methods, ADR structure, the correlation-engine
  technique, testing discipline, tool quirks. **Meant to flow everywhere.**

### D2 — The governing rule: *promote the pattern, never the payload*
Cross-pollination happens only via a **deliberate generalize-and-scrub step**:
extract the reusable technique, strip every tenant specific (client, code, business
logic, names, ticket IDs), and publish the generalized skill to the shared library.
**Never auto-copy memory or skills between tenants.** Promotion is a conscious,
reviewable act.

### D3 — Tenant = the isolation unit
A *tenant* is one confidentiality boundary, expressed as a Hermes profile (or, for an
employer, a separate Hermes home / OS user) that owns its:
- secrets / `.env` (never shared)
- tracker (Shortcut for dayjob; GitHub Issues / Obsidian for personal)
- memory store
- agent-os project scope (ADR-001 `project.tracker` + a new `tenant` grouping)

| Tenant | Profile/home | Tracker | Secrets |
|--------|-------------|---------|---------|
| dayjob (Riftwing) | executor + riftwing-* profiles | Shortcut | rewst creds |
| personal | personal profile | GitHub Issues / Obsidian | personal creds |
| $nightjob (future) | **separate home / OS user** | its own | its own |

### D4 — Three planes
- **Isolation plane (hard walls):** secrets, data, trackers, memory — partitioned by
  tenant. Never crosses.
- **Knowledge plane (curated, porous):** a shared **craft library** at
  `~/.hermes/skills/` for generic technique; tenant-specific skills stay at
  `profiles/<tenant>/skills/` (where the `rewst-*` / `riftwing-*` skills already live).
  Crossing happens here, by D2 promotion only.
- **Access plane (unified):** one cockpit, tenant-switched (profile + worktree).
  Accessibility comes from **uniform tooling**, not from removing walls. The active
  tenant is always enforced.

### D5 — Employer↔employer policy (the strict part)
- Each employer is a **fully separate tenant**; for $nightjob, prefer a separate Hermes
  home (not just a profile), possibly a separate OS user, depending on contract.
- Employer tenants **pull** from the personal craft library but **never push** into it
  without an explicit scrub review.
- **Employers never share knowledge with each other, even generic knowledge.** The
  shared library is *Tim's individual craft*; personal↔employer flow is allowed,
  employer↔employer is not.
- The bar for "is this truly generic?" rises sharply when the other side is a
  competitor: a generic-looking skill can secretly encode an employer's competitive
  approach.

### D6 — SPOG cross-tenant view is opt-in and employer-segregated
The single pane may show a combined view *within* the personal/dayjob scope, but a
combined view **across employer tenants is disallowed** — a pane showing two employers'
work side by side is an audit liability. Default SPOG view is single-tenant; "all"
is opt-in and excludes employer↔employer mixing.

### D7 — Contracts before architecture
Taking $nightjob is first a **legal/contractual** decision, not a technical one. Before
any wiring: verify non-compete, conflict-of-interest, and IP-assignment clauses in both
employment agreements. The architecture here mitigates accidental leakage; it does not
make contractually-prohibited work permissible.

---

## Fail modes

| # | Fail mode | Guardrail |
|---|-----------|-----------|
| F1 | Payload leaks via a "generic" skill | D2 scrub review; D5 raised bar for employer tenants |
| F2 | Auto-bleed of memory between tenants | D2 — promotion is manual only; no auto-copy path exists |
| F3 | Shared secrets across tenants | D3 — secrets are tenant-local, never in shared library or repo |
| F4 | Combined SPOG view exposes two employers | D6 — employer↔employer mixing disallowed |
| F5 | Doing prohibited work because the tooling made it easy | D7 — contracts gate the decision, not convenience |
| F6 | Convenience erodes the wall over time | Walls are structural (separate homes/profiles), not policy-only |

---

## Consequences

**Positive:** compounding craft across all work without confidentiality leakage;
clean fit with the existing profile model and ADR-001 project-mode; a defensible story
if an employer ever audits; $nightjob becomes an additive tenant, not a re-architecture.

**Negative / cost:** promotion is manual friction (by design); maintaining a clean
shared-vs-tenant skill split takes discipline; a separate Hermes home per employer adds
operational overhead; the "is this generic?" judgment is a recurring human call, not an
automatable one.

**Retired:** the implicit assumption that all of Tim's agent work lives in one
undifferentiated knowledge pool.

---

## Open questions (tracked, not blocking)

- **Where does the shared craft library physically live** relative to employer homes —
  symlink in, read-only mount, or copy-on-promote? Read-only pull is the safe default.
- **GitHub Issues / personal correlation key** (carried from ADR-001) — needed before
  personal-tenant attribution works.
- **$nightjob isolation depth** — separate profile vs separate home vs separate OS user
  vs separate machine. Decide when/if it becomes real, driven by D7's contract review.

See also: [[projects/agent-os/adr-001-observability-plane|ADR-001]] ·
[[projects/agent-os/spog-project-plan|SPOG Project Plan]] ·
[[projects/agent-os/vs-julian-goldie|vs Julian Goldie]] ·
[[projects/agent-os/README|Agent OS]]
