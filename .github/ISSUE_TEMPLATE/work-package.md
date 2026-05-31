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
- [ ] Every AC above is covered by a case in the Test plan below (no AC ⇒ no test ⇒ not done).
- [ ] <PreLIVE real-run check if applicable, e.g. "a real `claude -p` run produces a row">
- [ ] Screenshot at self@1920 (UI WPs) / mobile ≤430px (chat WP).

## Test plan (REQUIRED — enumerate cases, don't just say "add a test")
<!--
  Fill this in BEFORE coding. One row per case. The reviewer checks the SHIPPED tests
  against THIS table — a missing row is a spec gap (Lead's fault); a row with no test is
  an incomplete WP (yours). This is what stops the 3-bounce test-quality cycle.
-->

| # | AC | Case (what scenario) | Level | Asserts | Would FAIL if… |
|---|----|----------------------|-------|---------|----------------|
| 1 | AC1 | happy path | route+db | 2xx + body shape + DB row present | handler stops persisting |
| 2 | AC1 | <each error status> e.g. missing key→403, bad input→400, not-found→404 | route | exact status + error body | validation removed |
| 3 | AC1 | **failure path surfaces** (dependency/DB error) | route+fake-err | returns non-2xx, NOT 200 | error silently swallowed |
| 4 | AC2 | tenant isolation (if tenant-scoped) | route+db | tenant A's key/scope cannot read tenant B | cross-tenant leak (ADR-002) |
| 5 | AC2 | idempotency / replay (if applicable) | route+db | second call → contracted code, no dup row | upsert regressed |
| … | …  | edge: empty/aged/absorbing-terminal/etc | … | … | … |

**Rules these cases MUST obey (non-negotiable — promoted guardrails):**
- **No tautological tests.** A test that mocks the exact thing it verifies proves nothing.
  Each case must execute the REAL code path and FAIL if behavior breaks. For any
  guarantee/contract/loop, exercise it for real (run the loop, hit a real/fake transport,
  assert the emitted body) — not "call it once, assert it returned."
- **Route handlers get a route-level test.** Any handler under `internal/api/` ships an
  `internal/api/<x>_test.go` that drives the REAL chi router via `httptest` against **real
  PG17** (`AOS_TEST_DATABASE_URL`), modeled on `internal/api/workevents_test.go`. Assert
  **both** the HTTP response (status + body = the contract) **and** the DB state (the row
  count/values = reality). One without the other is not enough.
- **Integration tests must RUN, not skip.** A suite that `t.Skip`s its real-PG tests is a
  Gate-1 FAIL — a skipped test protects nothing. Spin a throwaway PG17, migrate, run.
- **Failure path is mandatory.** Every bug fix ships a regression test that fails on the old
  code. Every handler has a case proving an internal error returns non-2xx (a 200 on error
  is a silent-failure bug).
- **Unique fixtures.** Tests sharing a real DB MUST use globally-unique keys (uuid-suffixed
  external_ref/branch/host) + clean up their own rows (`t.Cleanup`), or they pollute each
  other when the package runs together.
- **Don't substitute, and tell on yourself.** If you cannot write a required case, do NOT
  claim adjacent tests "cover it" — say so explicitly in the PR body. A buried skip costs
  more trust than the time it saves.

## Verification commands (run these, paste output in the PR)
```bash
# build + vet
PATH="$HOME/go/bin:$PATH" go build ./... && go vet ./...
# throwaway PG17, migrate, run the FULL suite serially with the real DB
# (integration tests MUST run — a skipped suite is a Gate-1 fail)
podman run -d --name wp-test-pg -e POSTGRES_USER=test -e POSTGRES_DB=test \
  -e POSTGRES_HOST_AUTH_METHOD=trust -p 55434:5432 postgres:17-alpine
for f in internal/migrations/*.up.sql; do podman exec -i wp-test-pg psql -U test -d test -q -f - < "$f"; done
AOS_TEST_DATABASE_URL='postgres://test@localhost:55434/test?sslmode=disable' \
  go test ./... -count=1 -p 1
podman rm -f wp-test-pg
# UI WPs: tsc + vite build must pass; new files lint-clean
cd web && npm run build && npm run lint
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
