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

## UI work packages (REQUIRED if this WP creates/changes anything under `web/src/`)
<!-- Mockup-first, in-place, auto-merge. This is the project's UI contract. -->
- **Owner:** `lead` OR `roux` — but ONLY against a Lead-authored/approved mockup (see below).
  Tim's decision 2026-05-31: Roux MAY implement UI if a precise mockup + tokens exist and Lead
  is confident the design won't be botched. Lead ALWAYS owns the design (the mockup); Roux may
  own the labor. When in doubt, or for a net-new surface with no mockup, Lead builds it.
- **Mockup-first (do BEFORE coding) — this IS the design-ownership step:**
  1. Check for an existing mockup. The Observe/SPOG surfaces have one:
     `/home/tim/Obsidian/projects/agent-os/spog-ui-mockup.md` (+ `spog-ui-mockup.html`).
     If a directionally-correct mockup exists, build against it — don't re-derive pixels.
  2. If none exists, **Lead** builds a mockup (real aurora-theme tokens from
     `web/public/themes/aurora.css`; the exact data shape the API returns) and gets Tim's
     explicit sign-off BEFORE implementation is dispatched. A UI WP assigned to Roux MUST link
     an approved mockup in its body; without one, it stays Lead-owned. Design discussion stays
     in discussion mode until Tim approves a direction — Roux never improvises design.
- **In-place, no fork:** reuse `glass-card`, the CSS-var tokens (`--bg-card`, `--accent-*`,
  `--radius-*`, `--glass-blur`), Tailwind utilities, and the Material `Icon` component, so it
  auto-themes across aurora/daylight/noir. No new color system; match the existing app shell.
- **Honesty in the UI:** never render a stored flag as live truth — derive status from the
  data the server provides (F10). A card that lies about a dead agent being alive is worse
  than no card.
- **Design-fidelity gate (Lead, at review):** for ANY UI PR (Roux- or Lead-authored), Lead runs
  an in-browser design-fidelity check after the deploy gate — the built UI must match the
  approved mockup (tokens, spacing, glass-card, icons, both happy + empty states) and use no new
  color system. A fidelity miss is a Gate-3 finding sent back to the author, same as a functional
  miss. This is the safeguard that lets Roux build UI: the labor is delegated, the taste is gated.
- **Verify the BUILT result, not just the mockup:** after the deploy gate, load the deployed
  UI in the browser and confirm it renders real data correctly (both happy + empty states);
  fold any vision nits. Component/route tests per the Test plan still apply.
- **MERGE POLICY (Tim's decision 2026-05-31): auto-merge COVERS UI for agent-os.** Once the
  3 gates + green-baseline + hpms1 deploy gate + design-fidelity check pass, Lead merges without a
  separate human visual-OK-before-PR hold. NOTE: this intentionally DEVIATES from the
  Rewst/Riftwing `riftwing-ui-mockup-workflow` Gate 4 (which holds visual PRs for Tim's OK). That
  gate is a dayjob convention; this is Tim's private tool and he granted autonomous UI merge here.
  Do not import Gate 4 into this project.

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
