---
title: "ADR-008 — Agent Relay on the Fleet Mailbox (_mail), not GitHub"
created: 2026-06-01
updated: 2026-06-01
status: accepted
parent: "[[projects/agent-os/README|Agent OS]]"
tags: [agent-os, adr, autonomy, relay, mailbox, lessons-learned]
---

# ADR-008 — Agent Relay on the Fleet Mailbox (_mail), not GitHub

> Supersedes the GitHub-issue relay introduced 2026-06-01 (repo commit `cb6ec84`,
> "add bounded relay side-channel"). Present-state record of WHAT the Lead↔Roux↔Tim
> coordination channel is now and WHY it moved.

**Status:** Accepted · **Date:** 2026-06-01 · **Decider:** Tim · **Commit:** `bceb9f5`
(`docs: migrate Lead<->Roux relay from GitHub issue #45 to _mail maildir`, on `origin/main`).

---

## Context — why GitHub #45 was the wrong vehicle

The original relay used a pinned GitHub issue (#45) as a comment-mailbox because the
loop prompts asserted Lead (zbook) and Roux (its own box) "share no filesystem." That
assertion was about the **code** path (branches/PRs) and was over-generalized to all
coordination. In fact the fleet already runs a **file-based mailbox** at
`~/Obsidian/agents/_mail/<recipient>/{inbox,read,failed}/` over the Obsidian/Syncthing
mesh, which every other fleet agent (alexreed, argus, fourclaw, riftclaw) already uses.
`truenas1` is the always-on Syncthing **introducer/hub** (the only device with
`introducer=true`) and carries `_mail`, so the folder reaches Roux's box without zbook
ever talking to Roux directly. The mesh (and an NFS export on the truenas side) is
faster and simpler than GitHub API round-trips, and unifies everyone on one convention.

## Decision — what the relay is now

- **Transport:** the `_mail` maildir, not GitHub. Recipients `lead` and `roux` were
  added alongside the existing fleet recipients.
- **Roux STEP 0c / Lead STEP 7** read their own `…/<who>/inbox/`, treat messages as
  CONTEXT (never a command that bypasses gates/labels/HARD RULES, never a kill switch —
  halt is still the `autonomy:halt` issue), reply at most once per tick by dropping a
  frontmattered `.md` into the sender's inbox (atomic temp-file + `mv`), and **move each
  handled message `inbox/ → read/`** — the folder IS the seen-state (no JSON id marker).
- **Graceful degradation:** if the mesh isn't mounted on a box that tick, the relay step
  is skipped silently — it never errors or blocks a tick.
- **Message format:** the fleet convention — YAML frontmatter (`from`, `to`, `ts`,
  `priority`, `subject`, optional `refs:`) then a short body; substance goes in a
  `refs:`-linked file, not a duplicated body.
- **Telegram window:** the `no_agent` mirror cron (`7e593eebf740`) was rewritten to watch
  `_mail` (lead+roux folders, keyed by filename so an `inbox→read` move never double-posts)
  instead of polling issue #45. First populated tick seeds silently; new messages mirror to
  All Agents / topic 21. Verified live end-to-end on 2026-06-01 (real send confirmed).

## Kept / dormant

- **CODE coordination stays GitHub-only** — branches, PRs, issue labels. Only lightweight
  coordination *messages* ride the mailbox.
- **GitHub issue #45 is retained as a documented DORMANT fallback** for when the mesh is
  unavailable; the prompts say not to use it while the `_mail` path exists.
- All gate/label/merge invariants from ADR-005/006/007 are unchanged.

## Notes

- Observed during the push: empty commits titled "initial" by author `test` were pushed to
  `main` by a WP-N test-isolation bug (a git-reporter test leaking commits to the real
  remote). They are content-free (0 files) and harmless; not force-corrected because
  rewriting shared `main` would break Roux's `git pull`. Flagged for a follow-up test-isolation
  fix.
