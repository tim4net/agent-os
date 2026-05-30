# Adversarial Functional Review — does the code DELIVER what it promised?

> Distinct from code review. Code review asks "is this code correct?" (bugs, security,
> logic). Adversarial functional review asks "does this actually deliver the promised
> function, and do the tests PROVE it — or pass trivially?" This is the standing defense
> against demo-ware (the agent-os original sin: a pretty shell over an empty DB).
> Run as Gate 3, AFTER deterministic gates + code review, by an independent Opus context.

Inputs the reviewer is given: the linked issue's **acceptance criteria**, the PR **diff**,
and the **test files**. It must reach the verdict from those alone.

The reviewer answers, per acceptance criterion and overall:

1. **AC fulfillment.** For EACH acceptance criterion in the issue: is it actually
   implemented by this diff? Point to the code that delivers it, or flag it as
   unfulfilled/partial. An AC with no corresponding code = FAIL.

2. **Test honesty (the WP-B trap).** For each test: does it exercise the REAL behavior, or
   does it pass trivially? Red flags: a fake/mock that just hands back what it was given
   (tautological); assertions that would pass even if the feature were broken; tests that
   mock the exact thing they claim to verify; "happy path only" where the AC implies edge
   cases. A test that claims to prove guarantee X but cannot fail when X is violated = FAIL.

3. **AC-vs-test coverage gap.** Which acceptance criteria have NO test that would catch a
   regression? List them. (Not every AC needs a test, but a load-bearing guarantee with
   zero real coverage is a FAIL.)

4. **Reality gap.** Does it work against reality (real DB/API/filesystem) or only against
   fakes? For data/contract code, is there a test against a real backend, or is the whole
   proof mocked? (WP-B needed a real Postgres test; a fake-only suite was insufficient.)

5. **Demo-ware check.** Could this appear to work in a demo while silently not doing the
   thing? Silent failures, swallowed errors, values that are fabricated/defaulted rather
   than computed, parser drops. (Silent failure is a P1 anti-pattern here.)

6. **Contract adherence (functional).** Does it actually honor the frozen contract's
   semantics — not just the field names, but the BEHAVIOR (validation that truly rejects,
   idempotency that truly dedupes, read-only that truly cannot write)?

Output: per-finding SEVERITY (critical/major/minor), the exact gap, and a concrete fix.
A `critical` or `major` = changes requested (do not merge). End with:
`FUNCTIONAL VERDICT: DELIVERS | DOES-NOT-DELIVER (n blocking)` then `=== END REVIEW ===`.

This pairs with `requesting-code-review` (Gate 2). Both must pass before merge. Use a real
Opus context (e.g. the claude-api wrapper) for genuine judgment, independent of the author —
mandatory even when the author is Lead.
