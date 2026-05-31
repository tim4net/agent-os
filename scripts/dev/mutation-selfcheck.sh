#!/usr/bin/env bash
# mutation-selfcheck.sh — prove a Go test is NOT tautological (agent-os, ADR-006 D3).
#
# A test only protects a behavior if it FAILS when that behavior breaks. This helper
# drives the loop: baseline-green -> you apply the bug -> MUST go red -> restore -> green.
# It is the exact discrimination Lead's Gate 2/3 runs at review; running it before you
# push collapses the tautological-test / silent-failure / regression-guard-missing bounce
# cycle (this repo's top 3 recurring review findings).
#
# Usage:
#   scripts/dev/mutation-selfcheck.sh <pkg> "<TestNameRegex>"
# Examples:
#   scripts/dev/mutation-selfcheck.sh ./internal/api "TestSyncTracker_DispatchByTrackerType"
#   scripts/dev/mutation-selfcheck.sh ./internal/service "TestGitHubSync_SkipsPullRequests"
#
# Env:
#   AOS_TEST_DSN / AOS_TEST_DATABASE_URL — set BOTH to a real PG17 DSN so integration
#   tests actually RUN (a skipped test proves nothing). The script warns if neither is set.
#
# Flow is interactive by design: it pauses so YOU hand-edit the ONE production line that
# implements the behavior (revert it to the bug), because only you know which line that is.
set -uo pipefail

PKG="${1:-}"; RE="${2:-}"
if [ -z "$PKG" ] || [ -z "$RE" ]; then
  echo "usage: $0 <pkg> \"<TestNameRegex>\"" >&2; exit 2
fi
export PATH="/home/linuxbrew/.linuxbrew/bin:$HOME/go/bin:$PATH"
command -v go >/dev/null || { echo "go not on PATH" >&2; exit 2; }

if [ -z "${AOS_TEST_DSN:-}" ] && [ -z "${AOS_TEST_DATABASE_URL:-}" ]; then
  echo "⚠️  Neither AOS_TEST_DSN nor AOS_TEST_DATABASE_URL is set — integration tests will SKIP."
  echo "    A skipped test cannot be mutation-proven. Set both to a real PG17 DSN and re-run."
fi

run() { go test "$PKG" -run "$RE" -count=1 -p 1; }

echo "=== STEP 1/4: baseline — the test must PASS on current (correct) code ==="
if ! run; then
  echo "❌ Test does not pass on clean code. Fix it before mutation-proving." >&2; exit 1
fi
echo "✅ baseline green."
echo
echo "=== STEP 2/4: APPLY THE MUTATION ==="
echo "Hand-edit the ONE production line that implements the behavior \"$RE\" proves,"
echo "and revert it to the BUG (flip the comparison, drop the tenant=\$2 clause, return the"
echo "old constant, skip the continue, swallow the error, etc.). Do NOT edit the test."
echo "Save the file, then press ENTER to continue (Ctrl-C to abort)."
read -r _

echo "=== STEP 3/4: with the bug reintroduced, the test MUST now FAIL ==="
if run; then
  echo
  echo "❌❌ TAUTOLOGICAL TEST: it still PASSED with the bug reintroduced."
  echo "   The test does not exercise the real code path (mocks the thing it verifies,"
  echo "   asserts only != 500, re-implements the logic inline, or checks a fake's map"
  echo "   instead of prod SQL). FIX THE TEST so it fails here. Then restore your prod line."
  echo "   (Your production file is still mutated — restore it now: git checkout -- <file>)"
  exit 1
fi
echo "✅ test correctly FAILED on the reintroduced bug — it is load-bearing."
echo
echo "=== STEP 4/4: RESTORE the production code, then it must PASS again ==="
echo "Restore your prod line exactly (e.g. \`git checkout -- <file>\`), then press ENTER."
read -r _
if ! run; then
  echo "❌ Test fails after restore — your restore was not exact. Check \`git diff\`." >&2; exit 1
fi
# guard: ensure no stray mutation left in tracked files touched by this pkg
if ! git diff --quiet -- "$PKG" 2>/dev/null; then
  echo "⚠️  git diff is non-empty under $PKG after restore — verify the mutation left NO trace:"
  git --no-pager diff --stat -- "$PKG"
fi
echo "✅✅ MUTATION SELF-CHECK PASSED: green → (bug) red → (restore) green."
echo "    Add a 'Mutation self-check' block to your PR body recording this."
