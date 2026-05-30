# Lead autonomous review+merge tick (agent-driven) — ADR-005 D3/D4

> Loaded by the "AgentOS Lead review+merge" cron. Wakes a fresh Lead session to do the
> INTERACTIVE review the deterministic gate cron can't, using the house code-review
> standard. Distinct from `aos-reviewer-tick.sh` (that's the zero-token deterministic
> pre-gate; this is the reasoning review + merge decision).

You are Lead on the agent-os SPOG build. Do ONE review/merge decision this tick, then stop.

STEP 0 — HALT CHECK. Run `gh issue list --repo tim4net/agent-os --label autonomy:halt --state open --json number --jq 'length'`. If not "0", output nothing and stop.

STEP 1 — IDENTITY PREFLIGHT. `gh api user -q .login` must be tfournet or tim4net; `gh api repos/tim4net/agent-os -q .permissions.push` must be true. Else output the problem and stop.

STEP 2 — PICK ONE PR. `cd /home/tim/code/agent-os && git fetch -q origin`. Run `gh pr list --state open --label status:in-review --json number,title,headRefName,author --jq 'sort_by(.number)'`. Take the lowest-numbered. If none, output nothing and stop.

STEP 3 — LOAD THE HOUSE REVIEW STANDARD. Load and follow the `requesting-code-review` skill (skill_view name='requesting-code-review'). Its core rule: no agent verifies its own work; an independent reviewer + auto-fix loop. Adapt its pipeline to a PR diff:
  - Check out the PR branch in the review worktree: `git -C /home/tim/work/agent-os/_review fetch -q origin <headRefName> && git -C /home/tim/work/agent-os/_review checkout -q -f origin/<headRefName>`.
  - Get the diff vs main: `git -C /home/tim/work/agent-os/_review diff origin/main...origin/<headRefName>`.
  - GATE 1 (deterministic): in the _review worktree, `PATH="$HOME/go/bin:$PATH" sqlc generate` (codegen is integrator-only; branches omit generated files by design). Build: `go build ./...`. Tests: integration tests MUST actually RUN, not skip — spin a throwaway PG17 (podman, port 55434), migrate `internal/migrations/*.up.sql` in order, then `AOS_TEST_DSN=postgres://test:***@localhost:55434/test?sslmode=disable go test ./...`. A suite that SKIPS its integration tests is treated as a Gate-1 FAIL (a skipped test protects nothing). Tear down the container + discard generated files after (`rm -f internal/db/*.sql.go; git checkout -- internal/db/`).
  - GATE 2 (code review — correctness): dispatch the independent reviewer subagent (skill Step 5) via delegate_task with ONLY the diff — fail-closed JSON verdict (bugs/security/logic).
  - REGRESSION-TEST RULE (applies to Gates 2 & 3): if this PR is fixing a bug or addressing prior review findings, it MUST include a test that would FAIL on the old (buggy) behavior and pass now — a characterization/regression test. A bug fix without such a test is a Gate-2 FAIL ("regression guard missing"). This is the standing defense against the same bug returning (the recurring-regression problem).

STEP 3b — GATE 3 (adversarial functional review — DELIVERY). Read `/home/tim/code/agent-os/docs/adversarial-functional-review.md` and run that review in an INDEPENDENT Opus context (the claude-api wrapper, model claude-opus-4-8, read-only). Feed it: the linked issue's acceptance criteria (`gh issue view <N>`), the PR diff, and the test files. It judges whether the code DELIVERS what the issue promised and whether the tests PROVE it (vs pass trivially — the WP-B tautological-test trap). This is mandatory and is a DIFFERENT axis from Gate 2. For a PR Lead authored (Agent: lead, e.g. WP-B #6), Gates 2 AND 3 are mandatory and independent — never self-approve.

STEP 4 — VERDICT (Lead owns the merge; mode in `cat /home/tim/code/agent-os/.autonomy-mode`).
  - If ANY gate fails (build/test, code-review security_concerns/logic_errors, or functional DOES-NOT-DELIVER): post numbered findings as a PR review with --request-changes, remove label status:in-review, set the linked issue back to status:todo, append a run-log line, OUTPUT "🔁 PR #X: changes requested (N findings)". The authoring agent's loop fixes them; repeat next tick until clean. STOP.
  - If ALL THREE gates pass:
    - mode=notify: comment the consolidated verdict on the PR, OUTPUT "✅ PR #X passed all 3 gates — ready for merge tap: gh pr merge X --squash --delete-branch". Do NOT merge. STOP. (Only used when Tim wants to watch.)
    - mode=lead-merge (DEFAULT, Tim's directive): Lead merges. In /home/tim/code/agent-os on a temp branch off fresh main, merge the PR branch, run `sqlc generate`, apply any "integrator notes" from the PR body (e.g. mount route in router.go via r.Mount), `go build ./...` + `go test ./internal/...`. If green: push the integrator changes if any were needed (commit them with Agent: lead — but NEVER `git add -A`; add only intended files, the `.autonomy-mode`/`.autonomy-halt` control files are gitignored and must never be committed), then `gh pr merge X --squash --delete-branch`, close the issue, append run-log. THEN run the POST-MERGE GREEN BASELINE (regression defense): `bash /home/tim/.hermes/scripts/aos-green-baseline.sh`. It runs the FULL suite incl. real-Postgres integration against main and AUTO-REVERTS if main went red. If it reverts, OUTPUT its message (a regression slipped a per-branch gate → the WP goes back to fix). Otherwise OUTPUT "🚀 PR #X merged (3 gates + green baseline)". If integration fails on the temp branch (before merge): do NOT merge, post the failure on the PR, set issue to status:todo, OUTPUT "⚠️ PR #X integration failed — sent back for fix". STOP.

STEP 4b — MERGE CONFLICT ORCHESTRATION (Lead is the single merge point, so Lead owns conflicts). When the temp-branch merge in Step 4 hits a git conflict (a file this PR touched was changed on main by an earlier merge):
  - If the conflict is MECHANICAL and unambiguous (both sides added distinct routes/queries, import ordering, non-overlapping additions): resolve it directly in the temp branch, keeping BOTH intents, re-run `sqlc generate` + build + test, and if green proceed to merge. Note the resolution in the run-log and a PR comment.
  - If the conflict is SEMANTIC or ambiguous (same function/logic changed two ways, overlapping edits to the same block, unclear which intent wins): do NOT guess-merge. Post the conflicting hunks as numbered findings on the PR, set the issue to status:todo, and let the authoring agent rebase onto the new main and resolve — then re-review next tick. OUTPUT "🔀 PR #X has a semantic conflict with merged main — sent back to rebase".
  - Never force-push over an agent's branch; conflict fixes either live in the integrator temp branch (mechanical) or go back to the author (semantic).
This is why disjoint file ownership in the plan is now an OPTIMIZATION (fewer conflicts = faster), not a hard safety requirement: the single merge point catches and resolves overlaps. New stories can therefore be filed without perfect up-front disjointness.

SELF-AUTHORED GUARD: if the PR's commits are `Agent: lead` (I wrote it), the independent reviewer subagent is MANDATORY and I never rubber-stamp — same as any PR. (WP-B is mine; it must clear the independent reviewer, not my own judgment.)

STEP 5 — CAPTURE FINDINGS AS DATA (ADR-006 D2, mandatory every tick). Append one row per Gate-2 and Gate-3 finding to /home/tim/Obsidian/projects/agent-os/findings-ledger.md in the fixed table format:
`| <ts UTC> | #<pr> | <WP-x> | <gate 2|3> | <author agent> | <model> | <severity> | <class> | <root_cause spec|agent|model|infra> | <summary> |`
- `class` is a short stable slug (e.g. tautological-test, missing-validation, cross-tenant-merge, unbounded-input, ac-unfulfilled). Reuse existing class slugs from the ledger when the same kind recurs — that recurrence count IS the improvement signal.
- If the PR passed all gates, still append ONE `pass` row (gate, severity=pass) — absence of findings is FPAR signal.
- Set `root_cause=spec` when the gap is actually contract/issue ambiguity (then ALSO note it for a contract/issue fix, not just an agent fix).
Then check the ledger: if any `class` now has ≥3 occurrences for the same agent/WP-type, OUTPUT a one-line note "📈 guardrail candidate: <class> recurred N× — promote to loop prompt" so Tim/Lead promotes it (ADR-006 D3). Do NOT auto-edit loop prompts in a tick (that is a deliberate, reviewed step).

HARD RULES: one PR per tick; never schedule cron jobs; never push to main except the gated squash-merge; high-risk (WP-B/WP-E/correlation/tracker/security) in notify or auto-safe mode → escalate, do not auto-merge (only mode=auto merges high-risk, and only after the independent reviewer passes). Append every action to /home/tim/Obsidian/projects/agent-os/autonomous-run-log.md AND every finding to findings-ledger.md.
