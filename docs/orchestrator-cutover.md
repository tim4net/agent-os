# Orchestrator cutover runbook (WP-O5)

WP-O5 adds the in-app orchestrator dispatch driver in **shadow mode**. It can claim queued `work_units`, dispatch the existing gate pipeline through the `GatePipeline` adapter seam, write run-log/finding ledger rows, and mark units done/failed. It does **not** merge PRs. The existing Lead path remains the only merge authority during this WP.

## Current implementation boundary

- Binary: `cmd/orchestrator`
- Driver: `internal/service/orchestrator_driver.go`
- Queue/control plane: existing `work_units` and `control_state`
- Gate adapter seam:
  - Go interface: `service.GatePipeline`
  - CLI wiring: set `AOS_GATE_PIPELINE_CMD` to a shell adapter for the existing Hermes gate flow.
  - The command receives a JSON request on stdin and environment variables:
    - `AOS_WORK_UNIT_ID`
    - `AOS_WP_REF`
    - `AOS_ORCHESTRATOR_SHADOW=true`
    - `AOS_MERGE_ALLOWED=false`
  - The command must print a JSON `service.GateResult` on stdout.
- Halt seam:
  - By default the binary checks the same global sentinel as the cron prompts:
    `gh issue list --repo tim4net/agent-os --label autonomy:halt --state open --json number --jq length`
  - Override with `AOS_HALT_CHECK_CMD` if the runtime environment needs a wrapper. Set it to `off` only for local tests, never for production cutover.

## Load-bearing guarantees

- **Shadow mode is default and forced by `cmd/orchestrator` for this WP.** The driver injects `merge_allowed=false` and `dispatch_only=true` into the payload it passes to the adapter.
- **No double merge:** in shadow, the in-app driver never calls merge code and fails closed if the adapter reports `merged=true` or `merge_attempted=true`. The existing Lead review loop remains the sole process allowed to merge.
- **Gate independence:** the driver refuses a Gate 3 model from the same normalized model family as Gate 2. It records the degraded path as a finding instead of silently collapsing the independent-review gates.
- **App-native stop:** setting `control_state.mode='stopped'` halts dispatch on the next RunLoop mode read. The driver also checks the external `autonomy:halt` sentinel and forces `mode=stopped` when it fires.

## Staged handoff plan

Live cron changes are **human-gated operational steps performed outside WP-O5**. This WP only ships the binary, driver, tests, and runbook.

1. **Preflight**
   - Confirm database migrations are current.
   - Confirm `cmd/orchestrator` is deployed with the same `DATABASE_URL` as the app.
   - Confirm `AOS_GATE_PIPELINE_CMD` invokes the existing Hermes gate pipeline adapter and honors `AOS_MERGE_ALLOWED=false`.
   - Confirm `gh` auth for the halt check or provide `AOS_HALT_CHECK_CMD`.
   - Confirm the external Lead cron is still active and remains the only merge authority.

2. **Shadow period: 45 minutes in tick mode**
   - Set `control_state.mode='tick'`.
   - Start the in-app driver in shadow mode while the external cron continues to run.
   - Invoke/allow the driver to process one unit per tick-mode run.
   - Run this parallel shadow period for **45 minutes**.
   - Verify:
     - in-app run-log entries match the external Lead gate outcomes;
     - no `shadow_merge_guard` findings appear;
     - no adapter output reports `merged=true` or `merge_attempted=true`;
     - no same-family Gate 2/Gate 3 degraded findings except expected test cases;
     - PR merge history shows only the Lead path performed merges.
   - No-double-merge reasoning: the in-app driver has `merge_allowed=false`, only records dispatch/finding/run-log results, and fails closed on reported merge attempts. The external Lead cron remains the sole merge authority during the parallel run.

3. **Cutover checklist**
   - Human operator confirms no open `autonomy:halt` issue.
   - Human operator pauses the external driver cron that performs dispatch work.
   - Start `cmd/orchestrator` in shadow with `AOS_GATE_PIPELINE_CMD` configured.
   - Keep `control_state.mode='tick'` for the 45-minute shadow verification window if not already completed.
   - Verify no double-merge and no divergence for 45 minutes.
   - Flip `control_state.mode='continuous'`.
   - Disable the external driver cron after the continuous in-app driver is healthy.
   - Leave the external Hermes cron as a **thin watchdog/bootstrap** only: it should check liveness, restart/bootstrap the in-app driver if needed, and surface alerts. It should not be the primary dispatcher after cutover.

4. **Post-cutover monitoring**
   - Watch `run_log` for `orchestrator.dispatch.completed` and `orchestrator.dispatch.failed`.
   - Watch `findings` for `shadow_merge_guard`, `gate_independence_degraded`, and repeated `dispatch_error` classes.
   - Watch work-unit counts by status. Queued should drain in continuous mode; failed should be investigated before requeue.

## Rollback

If the in-app driver misbehaves:

1. Set `control_state.mode='tick'` immediately. This returns the app to one-unit-per-run behavior and stops continuous drain.
2. If needed, set `control_state.mode='stopped'` or open an `autonomy:halt` issue to force a halt.
3. Re-enable the external Hermes cron driver.
4. Confirm the external Lead path is again handling dispatch/merge decisions.
5. Inspect `run_log` and `findings` for the failure class before retrying cutover.

Rollback does not require a code revert unless the binary itself is unsafe. The control-plane rollback is the mode flip to `tick` plus re-enabling the cron.
