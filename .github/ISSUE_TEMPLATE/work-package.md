---
name: Work Package (SPOG build)
about: One issue per work package — the dispatch contract a single agent works from.
title: "WP-?: <short title>"
labels: ["spog", "wave:?", "agent:?", "status:todo"]
---

<!--
  This issue IS the dispatch contract (orchestration-plan §6). One WP = one agent =
  one branch = one PR. Fill every section before assigning. The implementer works
  ONLY from this issue + the frozen docs/work-event-contract.md.
-->

## Work package
**WP-id:** WP-?
**Wave:** ?
**Assigned agent:** `roux` | `lead`
**Depends on:** <WP ids that must merge first, or "none">

## Goal (1–2 sentences)
<what this WP delivers and why it exists; link the ADR/plan section it realizes>

## Frozen contract
Code to `docs/work-event-contract.md` @ commit `<sha>`. Do NOT deviate from the
event shape, enums, or correlation key. Contract questions → Lead, do not improvise.

## Owned files (the ONLY files this WP may create/modify)
- `path/one`
- `path/two`
<!-- per orchestration-plan §3: hand-write internal/db/queries/<wp>.sql; NEVER commit
     generated *.sql.go or edit querier.go/models.go/db.go — Lead runs sqlc generate. -->

## Off-limits (integrator-owned — propose as a PR-body diff, do NOT edit)
`internal/api/router.go` · `internal/config/config.go` · `cmd/server/*` ·
`web/src/App.tsx` · `web/src/api/client.ts` · `sqlc.yaml` · migration numbering ·
all `internal/db/*.sql.go` aggregates.

## Pre-assigned migration number(s)
<e.g. 000017, or "none">

## Acceptance criteria (must all pass before review request)
- [ ] <behavioral criterion 1 — concrete, testable>
- [ ] <criterion 2>
- [ ] Unit test included (no test ⇒ not done — plan §7).
- [ ] <PreLIVE real-run check if applicable, e.g. "a real `claude -p` run produces a row">
- [ ] Screenshot at self@1920 (UI WPs) / mobile ≤430px (chat WP).

## Verification commands (run these, paste output in the PR)
```bash
<exact build/test/curl commands the agent must run>
```

## Constraints (hard rules)
- Branch `wp-<x>/issue-<n>-<slug>`; commit trailer `Agent: <roux|lead>` + `Refs: #<n>`.
- gh identity preflight before any push (orchestration-plan §5) — abort on drift / dayjob email.
- No edits outside Owned files. Expose HTTP handlers via a `Routes()` `http.Handler`.
- **No external-tracker write code** (Shortcut/GitHub) — reviewer auto-rejects ([[ADR-001]] D4 / F5).
- No commit to `main`; push the feature branch and open a PR only. **Roux never merges.**
- Tenant-scoped where applicable; no employer-tenant co-mingling ([[ADR-002]]).

## Worktree
**Path:** <pre-created by Lead, e.g. /home/tim/work/agent-os/wp-g>
**Branch:** `wp-<x>/issue-<n>-<slug>`

## Review → merge (for reference)
Open PR with `Refs #<n>` → request review from Lead → set `status:in-review`.
Lead reviews (spec + quality + security/data where relevant). CHANGES_REQUESTED →
address each numbered finding, push, re-request. Lead approves → Lead runs
`sqlc generate`, wires routes/`client.ts`/`App.tsx`, verifies build, merges, closes this issue.
