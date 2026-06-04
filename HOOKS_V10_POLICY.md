# react-hooks eslint v10 migration — decision policy

Branch: chore/react-hooks-v10-migration (off c9d3cf3). Baseline: tsc clean, vitest 50/50.
Goal: drive `eslint .` to ZERO errors without introducing render loops, stale closures, or
behavior changes. 54 errors + 10 warnings across 29 files.

## Per-rule decision policy (apply consistently)

### @typescript-eslint/no-unused-vars (6) — MECHANICAL
- Remove the unused binding. In tests (ControlView.test.tsx ×5), if it's an unused destructure,
  drop it or prefix `_`. Never delete a line that has a side effect.

### @typescript-eslint/no-explicit-any (2, FleetRadar.test.tsx) — MECHANICAL
- Replace `any` with the real type. In tests, use the actual fixture/response type or `unknown`
  + a narrow cast. Do NOT weaken a production type to satisfy a test.

### react-hooks/globals (1, Toast.tsx) + the Toast cluster — REAL REFACTOR
- Toast.tsx reassigns a module-level `addToastFn` during render (anti-pattern). Fix properly:
  register the handler in a `useEffect` (not render body), generate IDs inside the handler
  (already there), and resolve `only-export-components` by keeping `showToast` — it's a
  legitimate imperative API. If extraction is needed, move `showToast`/`addToastFn` wiring into
  a tiny non-component module (e.g. `toast-bus.ts`) and have ToastContainer subscribe in an
  effect. Preserve exact toast behavior (4s auto-dismiss, dedup by id).

### react-hooks/purity (2: StatusFooter, FleetRadar) — JUDGMENT
- A non-deterministic or impure call in render body. If it's `Date.now()` used to compute a
  render value (FleetRadar maxWindow/now), wrap the derivation in useMemo keyed on the tick
  state that already drives recompute (FleetRadar already has a `tick`). For StatusFooter's
  `.filter().length` on a prop-derived array, memoize the derived count with useMemo.
- NEVER introduce a new interval/timer to "fix" purity. Use existing tick/state.

### react-hooks/exhaustive-deps (10) — CASE BY CASE, NO BLIND DEP-ADD
- Adding a missing dep can cause infinite loops. For each: determine if the missing dep is
  stable (a const, a useCallback) → add it. If it's a value that changes every render (inline
  object/array/function) → first stabilize it (useMemo/useCallback) THEN add it. If the effect
  is intentionally run-once-on-mount, the correct fix is to make deps honest (extract the
  one-time logic) — only use a disable comment with a clear justification when the effect is a
  genuine mount-only init and adding deps would re-run it wrongly.

### react-hooks/set-state-in-effect (29) — THE BIG ONE, CLASSIFY EACH
Three sub-patterns — handle differently:
  (a) **Async fetch on mount** (`useEffect(() => { fetch().then(setState) }, [])`): this is the
      LEGITIMATE data-fetch pattern the rule over-flags. Fix: `// eslint-disable-next-line
      react-hooks/set-state-in-effect -- async data fetch on mount; setState lands after await,
      not synchronously in the effect body`. Do NOT restructure working fetch code.
  (b) **External-system sync** (`useEffect(() => { if (lastEvent) setState(...) }, [lastEvent])`):
      ALSO legitimate (subscribing to an external store/SSE). Same disable with justification:
      `-- syncing state from external SSE event, not a render-derived value`.
  (c) **Synchronous setState in effect body deriving state from props/state** (the ACTUAL
      anti-pattern the rule targets): e.g. `useEffect(() => setX(propY * 2), [propY])`. Fix
      PROPERLY: compute during render (useMemo or plain const) and delete the effect. This is the
      only sub-pattern that gets a real code change, not a disable.

Classification rule: if the setState is inside a `.then()`/`await`/event-callback/subscription →
disable+justify (a or b). If it's a bare synchronous `setX(derive(props))` → refactor (c).

## Batching (NON-OVERLAPPING — no two subagents touch the same file)
Group by directory to keep file ownership disjoint. Each batch = one subagent.
- Batch 1 (mechanical): ControlView.test.tsx, FleetRadar.test.tsx — unused-vars + any only
- Batch 2 (hooks dir + App): src/hooks/useAgents.ts, src/App.tsx
- Batch 3 (Toast refactor — isolated): src/components/Toast.tsx (+ toast-bus.ts if extracted)
- Batch 4 (MissionControl cluster — 17 issues, isolated): src/components/MissionControl.tsx
- Batch 5 (FleetRadar prod): src/components/observe/FleetRadar.tsx
- Batch 6 (simple set-state-in-effect, group A): ActivityFeed, CommandPalette, StatusFooter,
  ThemeProvider, agents/AgentConfig, chat/ChatPanel, chat/ConversationHistory
- Batch 7 (simple set-state-in-effect, group B): control/ModeControls, goals/GoalList,
  kanban/Board, kanban/Card, layout/Sidebar, memory/FileTree, memory/NoteViewer
- Batch 8 (simple set-state-in-effect, group C): pipeline/PipelineBoard, skills/SkillsList,
  studio/MediaPreview, timeline/TimelineView, workflows/WorkflowList, workspace/ArtifactGrid,
  workspace/ArtifactPreview, PulseTicker

## Hard rules for every subagent
- After edits: `npx tsc --noEmit` MUST be 0, `npx eslint <files>` MUST be 0 for owned files,
  `npx vitest run` MUST stay 50/50. Report exact counts.
- NEVER weaken a test to pass. NEVER add a dep that creates a loop. NEVER add a timer.
- Every eslint-disable MUST have an inline justification of WHY the flagged code is correct.
- Surgical diffs only — touch only the flagged lines + minimal surrounding context.
