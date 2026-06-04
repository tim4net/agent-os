---
title: "ADR-007 — Loop Tuning, Gate Independence & UI Delegation"
created: 2026-05-31
updated: 2026-05-31
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, autonomy, gates, ui, lessons-learned]
---

# ADR-007 — Loop Tuning, Gate Independence & UI Delegation

> Amends [[projects/agent-os/adr-005-autonomous-build-loops|ADR-005]] (the two-agent
> loop) and [[projects/agent-os/adr-006-dogfooding-improvement-flywheel|ADR-006]] (the
> improvement flywheel) with four loop adjustments derived from the first 10 merged PRs,
> plus a UI-ownership change. Present-state record of WHAT the loop does now and WHY.

**Status:** Accepted · **Date:** 2026-05-31 · **Decider:** Tim

---

## Context — what the first 10 PRs taught us

Every one of the 10 merged PRs cleared all 3 gates + green-baseline + hpms1 deploy;
`main` never went red (0 reverts); zero file/migration collisions. The loop's *spine*
is sound and is KEPT unchanged. But four edges measurably cost rework or risked
corrupting the signal, and one ownership rule was relaxed:

- **Recurring defect classes re-emit despite prose guardrails.** tautological-test (~12×),
  silent-failure (~17×), reality-gap (5×), mock-only-proof (4×) kept recurring even after
  promotion into Roux's prompt. The gates *caught* them (good — that is severity-decay, not
  count-to-zero), but each catch is a review→fix→re-review cycle (WP-CI bounced ~4×).
- **The deterministic Gate-1 cron false-failed on its own infra.** Pattern-matched `*_test.go`
  on Python-only WPs; leaked `AOS_TEST_DSN` from sibling sessions caused dial/TRUNCATE
  false-fails; password literals display-masked as `***`.
- **Gate 3 silently collapsed into Gate 2's model family.** When the claude-api Opus wrapper
  was unavailable, Gate 3 fell back to a gpt-5.5 subagent — same family as Gate 2, so two
  "independent" gates became one, weakening the merge bar (observed on WP-D ticks).
- **Review cadence, not implementation, is now the bottleneck.** One PR/tick with 10 Wave-2
  WPs unfiled means the loop starves.

---

## Decision

### D1 — Pre-submit mutation self-check (shift the discrimination upstream)
Roux runs a **mutation self-check before every PR / re-push** (`roux-loop-prompt.md` STEP 5b):
for each AC and each "fixed" finding — break the production line, prove the test goes RED,
restore, prove GREEN. A regression test that does not fail on the reintroduced bug is not a
real test. This is the exact discrimination Lead's Gate 2/3 runs at review, moved to authoring
time, where it collapses the bounce cycle. tautological-test / silent-failure /
route-test-missing are unified as one meta-guardrail: *a test that does not fail when behavior
breaks*. Helper: `scripts/dev/mutation-selfcheck.sh <pkg> "<TestRegex>"` (in the repo). A
fix-rework PR with no "Mutation self-check" block in its body is incomplete.

### D2 — Type-aware deterministic Gate-1 cron (stop manufacturing false-fails)
`aos-reviewer-tick.sh` now:
- **Scrubs leaked DB env** (`unset AOS_TEST_DSN AOS_TEST_DATABASE_URL PG*`) at the top, so a
  sibling session's stray DSN can't make integration tests dial a dead host and false-fail.
  The pre-gate runs Go tests *without* a DB on purpose (skips are fine here); the agent gate +
  green-baseline run the real-PG suite authoritatively.
- **Classifies the diff** and gates by what actually changed: a Go WP needs `*_test.go`; a
  Python emitter WP needs a `test_*.py` under `emitters/`; a frontend WP needs `.test/.spec`.
  It no longer demands Go tests of a Python WP, and skips `go build`/`sqlc` on a Python-only diff.
- **Defers Python test EXECUTION to the agent gate.** Measured fact: the cron's base
  `/usr/bin/python3` (3.14) has no pytest, and the `uv run` path takes ~2 min AND false-fails the
  SIGINT signal tests on cpython-3.11 — running pytest in the zero-token pre-gate would
  *manufacture* a false-fail. Presence is checked here; execution is the agent gate's job (it has
  the right async env; runs 122/0 green).
- **Byte-probe, never trust display.** Hermes masks secret-shaped values as `***` in tool output;
  verify password/DSN literals with `od`/`grep` on disk before editing them (the WP-CI
  masked-secret-DSN lesson).

### D3 — Gate-3 model-independence is a hard, loud requirement
Gate independence is a load-bearing property, not a nicety. `lead-review-loop-prompt.md` now:
- **Verifies the Opus wrapper** (`/home/tim/.hermes/profiles/executor/bin/claude-api`) before Gate 3.
- If available: run Gate 3 on claude-opus-4-8; record `gate3_model=claude-opus-4-8`.
- If unavailable: **MUST NOT silently fall back** to a gpt-5.5 subagent. Run a degraded
  independent-context review, FLAG it loudly (`gate3_model=gpt-5.5 (DEGRADED)` + a ⚠️ line in the
  tick output + ledger), AND for any **high-risk PR** (WP-B/WP-E/correlation/tracker/security or
  any schema/migration change) a degraded Gate 3 is **not sufficient to merge** — hold at ESCALATE
  until a real-Opus Gate 3 runs. Low-risk may proceed flagged. Losing independence is a recorded
  event and a merge-bar change, never a hidden substitution.

### D4 — WP-MIG is the priority that unblocks the schema WPs
The repo still has **no migration runner** (deploy.sh carries an interim psql applier; hpms1
drift is hand-reconciled). [[#issue-11|WP-MIG #11]] is labeled into Roux's queue as priority and
**blocks the schema WPs** (WP-I #23, WP-J #24, WP-N #28, WP-A2 #10) — they each add a migration and
should land on the real runner, not the interim applier. Pure-read WPs (WP-K #25, WP-L #26,
WP-M #27) carry no migration and are eligible now. Migration-number ledger is collision-free:
**000017** WP-I · **000018** WP-J · **000019** WP-A2 · **000020** WP-N (next free: 000021).

### D5 — UI may be Roux-implemented against a Lead-authored mockup (taste stays gated)
**Supersedes the "Lead builds ALL UI" rule.** Tim's decision 2026-05-31: Roux MAY implement UI
when a precise Lead-authored/approved mockup + aurora tokens exist and Lead is confident the
design won't be botched. The split:
- **Lead always owns the DESIGN** — authors the mockup, gets Tim's sign-off. Roux never improvises
  design. A UI WP assigned to Roux MUST link an approved mockup; without one it stays Lead-owned.
- **Roux may own the LABOR** — building to that mockup in-place with `glass-card` + CSS-var tokens.
- **Design-fidelity gate (new Gate-3 axis):** for any UI PR, Lead loads the built UI in-browser
  after deploy and confirms it matches the approved mockup (tokens, spacing, glass-card, icons,
  happy + empty states, no new color system). A fidelity miss is a Gate-3 finding, same bar as a
  functional miss. This is what makes delegating UI labor safe — the typing is delegated, the taste
  is gated. Auto-merge still covers UI (no separate human visual-OK; the Rewst Gate-4 hold is NOT
  imported — agent-os is Tim's private tool).

---

### D6 — agy is NOT an autonomous implementer lane (trial verdict 2026-06-01)
Tim asked whether `agy` (Antigravity CLI, the WP-D-emitted harness) should take UI/UX polish
cycles as a third implementer. **Trial result: no.** On a deliberately bounded, mockup-anchored
2-line polish task dispatched headlessly via `agy --print --dangerously-skip-permissions`, agy:
got nerd-sniped by the `--dangerously-skip-permissions` flag name (web-searched it, grepped the
repo for "Permission", read the loop prompts and issues), ran the Go test suite unprompted, and
then **made zero edits** — ending by asking "what would you like to work on next?". Its `--print`
mode behaves like a single interactive turn, not a complete-the-task agent.
- **Decision:** no `agent:agy` UI lane. The proven path stays **Roux (implement) + Lead (design
  mockup + review) + the design-fidelity gate (D5)**. Adding agy would inject unreliability into
  the one lane (UI) where taste matters most.
- **Reusable lesson:** before standing up any new autonomous implementer, trial it on ONE bounded
  task and measure whether it *completes the edit* — not whether the model is capable in chat. A
  CLI that needs a human turn to act is not an autonomous lane.

## What is explicitly KEPT (the spine is not changing)
- 3 independent gates (deterministic build/test → correctness → adversarial-functional) before any merge.
- Lead-merge authority on 3-gate pass; post-merge green-baseline auto-revert; hpms1 deploy gate every merge.
- One PR per tick; halt sentinel; identity preflight; disjoint file ownership as an optimization.
- Findings-ledger every tick; class recurring ≥3× → guardrail promotion (ADR-006 D3).

## Consequences
**Positive:** fewer review bounces (discrimination moved upstream); the deterministic pre-gate
stops corrupting its own signal; the merge bar can't silently weaken; the schema dependency is
sequenced; UI labor can parallelize without surrendering design quality.
**Negative:** Roux's per-PR work is heavier (mutation self-check is real effort); a degraded Gate 3
can stall high-risk merges until the Opus wrapper is back (acceptable — that is the point).

## Implementation state (2026-05-31)
- In-repo (`main` @ commit `302660e`): `docs/roux-loop-prompt.md` STEP 5b + meta-guardrail;
  `docs/lead-review-loop-prompt.md` Gate-3 independence + UI-fidelity axis;
  `.github/ISSUE_TEMPLATE/work-package.md` UI-delegation policy; `scripts/dev/mutation-selfcheck.sh`.
- Cron script (`~/.hermes/profiles/executor/scripts/aos-reviewer-tick.sh`): D2 hardening; backed up
  in the `tfournet/hermes-config` private mirror.
- Issues filed: WP-I #23, WP-J #24, WP-K #25, WP-L #26, WP-M #27, WP-N #28 (backend-only, disjoint
  ownership); WP-MIG #11 + WP-A2 #10 prioritized/dep-noted.

See also: [[projects/agent-os/adr-005-autonomous-build-loops|ADR-005]] ·
[[projects/agent-os/adr-006-dogfooding-improvement-flywheel|ADR-006]] ·
[[projects/agent-os/build-orchestration-plan|Orchestration Plan]] ·
[[projects/agent-os/findings-ledger|Findings Ledger]] · [[projects/agent-os/README|Agent OS]]
