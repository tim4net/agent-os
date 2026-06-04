---
title: "ADR-005 — Autonomous Two-Agent Build Loops"
created: 2026-05-30
updated: 2026-05-30
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, autonomy, cron, multi-agent]
---

# ADR-005 — Autonomous Two-Agent Build Loops

> How Lead (this Hermes/Opus) and Roux (Hermes/GLM) run the SPOG build with minimal
> human involvement, while keeping the one irreversible action — merge to `main` —
> behind a deliberate gate. Operationalizes [[projects/agent-os/build-orchestration-plan|the orchestration plan]].

**Status:** Accepted · **Date:** 2026-05-30 · **Decider:** Tim

---

## Decision

### D1 — Two polling loops, GH state is the coordination spine
- **Roux implementer loop** (on Roux's Hermes): polls open issues labeled
  `agent:roux status:todo` (deps met) and own PRs in `changes-requested`; does ONE unit
  of work per tick; pushes branch, opens/updates PR, sets `status:in-review`.
- **Lead reviewer loop** (this executor profile): polls PRs labeled `status:in-review`;
  reviews ONE per tick; gates + escalates or merges per D3.
- **Coordination = GitHub labels + PR review state only.** Atomic, machine-queryable,
  single source of truth for work-state. Not Obsidian (avoids two-sources-of-truth).

### D2 — Obsidian = shared append-only run-log (audit, not coordination)
Both agents append a one-line entry per tick to
`projects/agent-os/autonomous-run-log.md` (what they did, PR/issue, verdict). This is the
human-readable trail Tim reads after the fact. Durable record, never the live state.

### D3 — Merge policy (the one gate) — THREE review gates, Lead owns the merge
A reviewer tick runs gates of increasing depth; ALL must pass before merge:
- **Gate 1 — deterministic** (the no-token cron): `go build` green · `go test` green · no
  edits to integrator-owned files · no external-tracker write verbs (F5) · required test
  present · only Owned files touched.
- **Gate 2 — code review** (`requesting-code-review` skill, independent reviewer subagent):
  "is the code correct?" — bugs, security, logic. No agent verifies its own work.
- **Gate 3 — adversarial functional review** (`docs/adversarial-functional-review.md`,
  independent Opus context): "does it DELIVER what the issue promised, and do the tests
  PROVE it?" — AC fulfillment, test honesty (the tautological-test trap), coverage gaps,
  reality gap, demo-ware. Distinct axis from Gate 2; catches code that is correct but
  doesn't do the thing (or only appears to).

**Lead owns the merge decision** (Tim's directive: "do merges when you feel they are good;
when they aren't, make the agent fix it until it is"). When all three gates pass, Lead
merges (runs `sqlc generate`, applies integrator notes, builds, squash-merges, closes the
issue). When any gate fails: post numbered findings, flip the issue to `status:todo`, and
the authoring agent's loop fixes them — repeat until clean. This is AI-driven development:
the human is not in the per-PR merge path; the halt sentinel + run-log are the oversight.

**Self-authored guard:** for a PR Lead wrote (e.g. WP-B), Gates 2 and 3 MUST run in an
independent context (delegate_task / claude-api Opus) — Lead never merges its own work on
its own say-so.

### D4 — Merge authority: Lead merges on 3-gate pass (Tim's directive)
Tim's call: "I prefer for you to do merges when you feel they are good. When they aren't,
make the agent fix it until it is. This is an exercise in AI-driven development." So the
default operating mode is **Lead auto-merges any PR that passes all three gates (D3)** —
no human merge tap in the per-PR path. The human's controls are the halt sentinel and the
run-log, not per-merge approval.
`merge_mode` marker still exists as a brake: `lead-merge` (default — Lead merges on
3-gate pass), `notify` (post the verdict + merge command, wait for Tim — use when Tim wants
to watch), `halt` equivalent via the sentinel. Start at `lead-merge` per directive; flip to
`notify` only if Tim wants to re-enter the loop.
Self-authored PRs (Lead's own, e.g. WP-B) still require Gates 2+3 in an independent context
before Lead merges them — that guard is never relaxed by the mode.

### D5 — Safety rails (every tick, both loops)
1. **Kill switch (cross-machine):** the global brake is a GitHub sentinel — an OPEN issue
   labeled `autonomy:halt`. Both loops check it first each tick and exit silently if
   present. Stop everything: `gh issue create --repo tim4net/agent-os --title "HALT" --label autonomy:halt`
   (or label any open issue). Resume: close it / remove the label. (Lead's loop ALSO
   honors a local `/home/tim/code/agent-os/.autonomy-halt` file as a zbook-only fast stop,
   but the GitHub sentinel is the authoritative cross-machine switch since Roux runs on a
   separate host.)
2. **Bounded:** exactly one unit of work per tick, then exit. No internal loops.
3. **Isolation:** the reviewer works in a dedicated `_review` worktree, never Lead's or
   Roux's WP worktrees (no git collision with interactive work).
4. **Escalate, don't thrash:** a PR that fails review twice → stop auto-acting, ping Tim.
   Any need to change the frozen contract → stop, ping Tim (Lead-only, O7).
5. **Identity preflight** before any push/merge (orchestration §5): active gh ∈
   {tfournet,tim4net}, push=true, commit email not `@rewst.io` — abort on drift.
6. **No recursive cron:** loop sessions never schedule more cron jobs.
7. **Cost/cadence:** reviewer ~10 min, implementer ~15 min — bounded spend, no tight spin.

### D6 — Regression defense (the recurring-regression problem)
Three layers, because per-branch gates alone have repeatedly let regressions through:
1. **Post-merge green baseline (`aos-green-baseline.sh`):** after EVERY merge, the full
   suite incl. real-Postgres integration runs against `main` itself. If main goes red, the
   just-merged commit is **auto-reverted** (keeps main always-green so the next branch-off
   isn't built on broken ground), a `regression` finding is logged, and the WP goes back to
   fix. If the revert can't apply cleanly, the fleet halts (sentinel) for a human. This is
   the layer that catches "passed on its branch, broke main" — semantic conflicts and
   cross-PR interactions a per-branch gate cannot see.
2. **Integration tests must RUN, not skip:** Gate 1 spins a real PG and sets the DSN so
   integration tests actually execute. A skipped test protects nothing (the stale-env-var
   trap that let a test silently skip).
3. **Regression test required per bug fix:** any PR fixing a bug / addressing findings must
   include a test that fails on the old behavior and passes now (characterization test).
   No regression guard = Gate-2 fail. So every bug, once found, grows a permanent guard and
   cannot silently return. Pairs with the findings ledger (ADR-006): each ledgered bug class
   becomes a standing test.

---

## Fail modes

| # | Fail mode | Guardrail |
|---|-----------|-----------|
| A1 | Bad PR auto-merged to main unattended | D3 gates + D4 default `notify` + high-risk always escalates |
| A2 | Runaway loop / cost spin | D5.2 one-unit-per-tick + fixed cadence + D5.1 kill switch |
| A3 | Loop collides with interactive git work | D5.3 dedicated `_review` worktree |
| A4 | Thrash on an unfixable PR | D5.4 two-strikes → escalate |
| A5 | Contract drift via autonomous change | D5.4 contract change = human-only |
| A6 | Loop pushes as wrong identity / dayjob email | D5.5 identity preflight aborts |
| A7 | Silent failure (Tim can't tell it broke) | D2 run-log every tick + ping on escalation |

---

## Consequences
**Positive:** the build runs largely unattended; Tim reviews an audit log and taps merges
(or flips to full auto once proven); dogfoods the SPOG (these loops emit work-events too).
**Negative:** `notify` mode still needs Tim's merge taps (by design, first phase); loop
review is shallower than interactive Lead review for hard WPs — hence high-risk always
escalates.

See also: [[projects/agent-os/build-orchestration-plan|Orchestration Plan]] ·
[[projects/agent-os/spog-project-plan|SPOG Plan]] · [[projects/agent-os/README|Agent OS]]

> **Amended by [[projects/agent-os/adr-007-loop-tuning-and-ui-delegation|ADR-007]]**
> (2026-05-31): pre-submit mutation self-check, type-aware deterministic gate, Gate-3
> model-independence hard-flag, and UI-delegation (Roux may build UI against a Lead-authored mockup).
