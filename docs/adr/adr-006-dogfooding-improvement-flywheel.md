---
title: "ADR-006 — Dogfooding & the Agent Improvement Flywheel"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, dogfooding, feedback-loop, self-improvement, evaluation]
---

# ADR-006 — Dogfooding & the Agent Improvement Flywheel

> Agent OS is being built BY the kind of agents it is designed to observe (Lead/Opus,
> Roux/GLM, Roux's future model swaps). So its first dataset is its own construction, and
> its first product validation is whether *observing* that construction makes the builders
> *better* — regardless of model. This ADR defines how the build becomes visible in Agent
> OS (visibility loop) and how those observations feed back to improve the coding agents
> (improvement loop). It is the concrete realization of Layer 7 (the compounding feedback
> loop) and the answer to the Julian comparison's "impressive day 1, same day 100."

**Status:** Accepted (design) · **Date:** 2026-05-30 · **Decider:** Tim
**Related:** [[projects/agent-os/adr-001-observability-plane|ADR-001]] (the plane),
[[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]] (promote-the-pattern),
[[projects/agent-os/adr-005-autonomous-build-loops|ADR-005]] (the loops generating the data),
[[projects/agent-os/vs-julian-goldie|vs Julian Goldie]] (the moat this defends).

---

## The two loops

```
        ┌─────────────────── VISIBILITY LOOP ───────────────────┐
build agents (Roux/Lead) → emit work-events + reviews + merges → Agent OS surfaces them
        └────────────────────────────────────────────────────────┘
                                   │  observations become data
                                   ▼
        ┌─────────────────── IMPROVEMENT LOOP ──────────────────┐
recurring findings → distilled guardrail rules → injected into agent prompts/skills
   → fewer repeat mistakes → measured by first-pass acceptance → compounding
        └────────────────────────────────────────────────────────┘
```

The visibility loop is table-stakes dogfooding. The improvement loop is the actual moat.

---

## Decisions

### D1 — The build emits into its own observability plane (recursive dogfooding)
The emitters (WP-C/D, the supervisor) watch **Roux and Lead building Agent OS**, not just
future generic agents. While we build, the fleet monitor shows our own sessions, the
work-units correlate our own WP issue ↔ PR ↔ branch ↔ session, the cost panel shows what
*this build* costs in tokens. The system's first live data is itself. If observing our own
build is useful, the product is validated; if it's noise, we learn that now, cheaply.

### D2 — Review findings are FIRST-CLASS STRUCTURED DATA, not prose in PR comments
**(time-sensitive — see Recommendation).** Every gate verdict (Gate 1 deterministic, Gate 2
code-review, Gate 3 adversarial-functional) emits a structured record, not just a PR
comment + run-log line:
```
review.finding { pr, issue/wp, gate(1|2|3), agent, model, severity,
                 finding_class, root_cause(spec|agent|model|infra), summary, ts }
```
Rationale: PR comments are unqueryable prose. The improvement loop (D3) and the eval
harness (D4) are **impossible without structured findings**. The review loop is generating
this signal right now, every tick — captured as prose it is lost for analysis. This is the
single highest-leverage capture in the project because the build is the richest, most
honestly-labeled dataset we will ever have ("agent X using model Y made mistake class Z,
caught by gate G").

### D3 — The improvement loop is PROMPT/SKILL MUTATION, not model fine-tuning
The way agents get better here is **model-agnostic by construction**: a recurring finding
becomes a *guardrail rule* injected into the agent's loop prompt or a skill — not a weight
update. Flow:
1. Findings (D2) aggregate by `finding_class`.
2. A class that recurs ≥N times for an agent/WP-type is a *systematic* gap, not noise.
3. It is distilled into a pre-emptive rule ("before opening a PR for ingestion work, assert
   idempotency with a real duplicate-POST test") and added to the agent's loop prompt /
   the relevant skill.
4. Next time, the agent is told *before* it makes the mistake. Repeat-rate drops.

This is the negative-space twin of [[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]]'s
"promote the pattern": ADR-002 promotes *successful* technique into shared skills; D3
promotes *observed failure modes* into guardrails. Both compound. And because the
correction lives in the prompt/skill layer, it applies whether the implementer is GLM,
Opus, Codex, or next year's model — **the guardrails get tighter as the fleet learns, which
is exactly Tim's "agents in deterministic guardrails = moat."** The model is swappable; the
accumulated guardrails are the durable asset.

### D4 — The SPOG doubles as a live fleet-evaluation harness
Because every agent's work + every review verdict lands in one data model, Agent OS can
measure the fleet objectively:
- **First-Pass Acceptance Rate (FPAR)** per agent/model: did the PR clear all 3 gates on
  the first try, or bounce N times?
- **Findings-per-PR by class and gate** — the failure-mode fingerprint of each model.
- **Gate effectiveness** — which gate catches what. Is Gate 3 (Opus, expensive) earning its
  cost by catching classes Gate 2 misses (e.g. the WP-B tautological test)? Data decides
  whether to keep/cut it — not vibes.
- **Cost-per-merged-WP** (implementation + review tokens) — the real price of a feature.
- **Repeat-failure rate** — same `finding_class` recurring after a guardrail was added means
  the guardrail isn't working (D3 didn't take).
This makes model selection empirical: swap Roux's GLM-5.0 → 5.1 (or anything), and measure
the FPAR/cost/failure-mode delta on real work. The SPOG becomes a standing eval of your own
agents — ties directly into the `hermes-main-model-swap` discipline.

### D5 — Build-specific surfaces in Agent OS (new views, when the observability WPs land)
- **Build pipeline view:** each WP as a card moving through Issue → Implementing → Gate 1 →
  Gate 2 → Gate 3 → Merged, with the agent, model, cost, and bounce-count on it. (This is
  the kanban concept, but fed by *real* gate state, not manual drag.)
- **Agent scorecard:** per agent/model — FPAR, avg findings/PR, cost/WP, top failure classes.
- **Findings ledger:** the queryable D2 dataset, filterable by class/gate/agent, with a
  "promoted to guardrail?" flag closing the D3 loop visibly.
These are additive views on the WP-J/K/L/B data, not new subsystems.

### D6 — Honesty guardrails on the loop itself (adversarial self-review)
The improvement loop can rot in specific ways; named so the design resists them:
- **Goodhart on FPAR:** if "pass rate" becomes the target, reviewers get gamed lenient.
  Mitigation: the adversarial reviewer (Gate 3) is independent and is *not* rewarded for
  passing PRs; FPAR is a diagnostic, never an agent's objective.
- **Feedback overfitting:** injecting every past finding bloats prompts and over-indexes on
  rare mistakes. Mitigation: only promote findings that recur ≥N times; cap the injected
  guardrail set; expire guardrails whose finding_class stops recurring.
- **Misattribution:** blaming "the model" for what is actually spec ambiguity. Mitigation:
  every finding carries `root_cause(spec|agent|model|infra)` — a spec-root finding fixes the
  *contract/issue*, not the agent.
- **Cutting the gate that's secretly load-bearing:** if Gate 3 is the only thing catching
  demo-ware and we cut it for cost without data, quality silently drops (the original
  agent-os wound). Mitigation: D4 gate-effectiveness data gates any such cut.
- **Tenancy:** build data is personal-tenant (fine). If this pattern ever extends to dayjob
  work, findings could encode dayjob specifics — ADR-002 scrub-before-promote applies to
  guardrails too.

---

## Why this is the moat (not a feature)

Julian's stack is a bundle that is identical on day 100. This flywheel is the mechanism by
which Agent OS is *measurably better* on day 100: more guardrails, lower repeat-failure,
higher FPAR, known cost/quality per model. It is impossible to build on a pile of
disconnected tools because it requires findings + work + cost + outcomes in **one data
model** — exactly the integration only this architecture has. The build dogfoods it, the
findings improve it, the eval harness proves it. That compounding, and its measurability,
is the durable advantage.

---

## Sequencing (depends on the in-flight observability WPs)

This ADR is design; it plugs into WPs already planned. Order:
1. **NOW (time-sensitive):** structured finding capture (D2) added to the review loop, so
   the build's own findings are preserved as data from the first gate run. Lossy otherwise.
2. After WP-A/B land: emitters carry the build's own sessions into work-units (D1).
3. After WP-J/K/L: agent scorecard + pipeline + findings-ledger views (D5), eval metrics (D4).
4. Continuous: the D3 promote-failure-to-guardrail step, run as part of the curator/closeout
   rhythm (recurring findings → loop-prompt/skill updates).

New backlog WPs implied (to be filed when their dependencies merge): WP-T findings store +
`review.finding` capture; WP-U agent scorecard / eval API; WP-V build-pipeline view;
WP-W guardrail-promotion tool (findings → loop-prompt/skill diff for Tim's approval).

See also: [[projects/agent-os/adr-005-autonomous-build-loops|ADR-005]] ·
[[projects/agent-os/adr-002-tenancy-knowledge-boundaries|ADR-002]] ·
[[projects/agent-os/spog-project-plan|SPOG Plan]] · [[projects/agent-os/README|Agent OS]]

> **Amended by [[projects/agent-os/adr-007-loop-tuning-and-ui-delegation|ADR-007]]**
> (2026-05-31): mutation self-check operationalizes the guardrail-promotion loop —
> recurring classes (tautological-test, silent-failure) are now caught at authoring, not just review.
