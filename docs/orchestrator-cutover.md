# Orchestrator Cutover — Staged Handoff & Rollback Runbook

## WP-O5 (#42): In-App Orchestrator Driver

This document is the **handoff checklist** (AC5) for transitioning from
external cron-driven orchestration to the in-app continuous driver.

---

## Overview

The in-app orchestrator driver (`cmd/orchestrator`) runs the continuous
dispatch loop from WP-O1 (#38) against real work, claiming units and
invoking the existing gate pipeline per unit.

**Key principle: SHADOW MODE FIRST.** The driver runs in parallel with the
external cron without disrupting the existing merge flow.

---

## Stage 1: Shadow Mode (Default)

### What happens
- Driver starts with `-shadow=true` (default)
- Claims and dispatches work units through gates
- Records findings to the ledger
- Records run_log entries marked `(shadow)`
- Does **NOT** merge — the existing Lead merge decision is the single merge
  point

### How to start
```bash
# Using the orchestrator binary
./orchestrator -dsn "$DATABASE_URL" -shadow=true

# Or via environment variable
DATABASE_URL=... ./orchestrator
```

### Shadow mode checklist
- [ ] Orchestrator binary built and deployed
- [ ] Control state set to `tick` or `continuous` (not `stopped`)
- [ ] Driver running in shadow mode alongside external cron
- [ ] run_log entries appearing with `(shadow)` summary
- [ ] Findings recorded in ledger
- [ ] No duplicate merges observed
- [ ] External cron continues to function normally

### How to verify shadow mode is working
```bash
# Check run_log for shadow entries
curl -s http://localhost:8080/api/control/state | jq .

# Check work units are being processed
curl -s http://localhost:8080/api/control/units?status=done | jq .
```

---

## Stage 2: Promote to Primary

Once shadow mode has been validated (typically 1-2 weeks):

### Pre-promotion checklist
- [ ] Shadow mode has run for at least one full sprint cycle
- [ ] All gate findings match expectations (compare shadow vs. cron results)
- [ ] No duplicate dispatches detected
- [ ] No double-merge incidents
- [ ] Halt sentinel mechanism tested and working
- [ ] Rollback procedure tested in staging

### Promotion steps
1. **Stop the external cron** — disable the Hermes cron jobs for
   `lead-review-loop-prompt` and `roux-loop-prompt`
2. **Restart the orchestrator with `-shadow=false`**
3. **Set mode to `continuous`**
4. **Monitor closely for 1 hour**
5. **Verify single merge point is preserved**

### Verify promotion
```bash
# Mode should be continuous
curl -s -X POST http://localhost:8080/api/control/mode \
  -H 'Content-Type: application/json' \
  -d '{"mode":"continuous"}'

# Units should be dispatched without (shadow) prefix in run_log
```

---

## Rollback Procedure

### Scenario: Need to revert to external cron

1. **Stop the orchestrator binary** (`kill <pid>` or `systemctl stop orchestrator`)
2. **Set control state to `tick`**
   ```bash
   curl -s -X POST http://localhost:8080/api/control/mode \
     -H 'Content-Type: application/json' \
     -d '{"mode":"tick"}'
   ```
3. **Re-enable the external Hermes cron jobs**
4. **Verify cron picks up work within one interval**

### Scenario: Driver is stuck or dispatching incorrectly

1. **Set mode to `stopped`** — this halts dispatch within one iteration (AC4)
   ```bash
   curl -s -X POST http://localhost:8080/api/control/mode \
     -H 'Content-Type: application/json' \
     -d '{"mode":"stopped"}'
   ```
2. **Or create the halt sentinel file**
   ```bash
   echo "autonomy:halt" > /path/to/halt-sentinel
   ```
3. **Investigate run_log and findings**
   ```bash
   curl -s http://localhost:8080/api/control/units?status=failed | jq .
   ```
4. **Requeue failed units if needed**
   ```bash
   # Via API
   curl -s -X POST http://localhost:8080/api/control/units/{id}/requeue
   ```

---

## Architecture Constraints

### Gate Independence (AC3)
- Gate 2 and Gate 3 MUST use different model families
- The driver validates this at startup and before each dispatch
- Violation fails the unit with a descriptive error

### Single Merge Point (AC2)
- In shadow mode, the driver NEVER merges
- The existing Lead merge decision is the single merge point
- This is enforced structurally: the dispatchFn in shadow mode skips merge

### Stop Flag (AC4)
- App-native: setting `mode=stopped` halts dispatch within one iteration
- External: `autonomy:halt` sentinel file also honored during shadow

### Work Unit Lifecycle
```
queued → in_flight → done
                   ↘ failed (with error message, never swallowed)
```

---

## Monitoring

### Key metrics to watch
- `work_units` count by status
- `run_log` entries per hour
- `findings` count by severity
- Time from `queued` to `done`

### API endpoints
- `GET /api/control/state` — current mode and queue counts
- `GET /api/control/units?status=failed` — failed units
- `GET /api/control/units?status=in_flight` — in-progress units

---

## File Reference

| File | Purpose |
|------|---------|
| `cmd/orchestrator/main.go` | Binary entry point |
| `internal/service/orchestrator_driver.go` | Live-dispatch wiring |
| `internal/service/orchestrator_driver_test.go` | Integration tests |
| `docs/orchestrator-cutover.md` | This document |

---

*Last updated: WP-O5 (#42) — Cutover wiring*
