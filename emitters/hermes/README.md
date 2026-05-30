# Hermes Work-Event Emitter

Thin out-of-process emitter for [Agent OS](https://github.com/tim4net/agent-os).
Emits work-events from Hermes delegate/session lifecycle to the Agent OS ingestion endpoint.

Realizes [ADR-001 D3](https://github.com/tim4net/agent-os/blob/main/docs/adr-001.md)
(thin emitters, never proxies). Codes to the
[frozen work-event contract v1.1](https://github.com/tim4net/agent-os/blob/main/docs/work-event-contract.md).

## Architecture

```
Hermes session → emitter.py → POST /api/events/work
                     ↓
              heartbeat loop (every ~60s)
                     ↓
              session.end on completion/failure/cancellation
```

The emitter is **supervised**: a background heartbeat loop outlives the Hermes
work, ensuring the Agent OS dashboard can detect crashed sessions via liveness timeout.

## Installation

```bash
# From the agent-os repo root
pip install -e emitters/hermes/
# Runtime dependency
pip install httpx
```

## Quick Start

### Library usage (async)

```python
import asyncio
from emitters.hermes import HermesEmitter

async def main():
    emitter = HermesEmitter(
        endpoint="http://localhost:8080/api/events/work",
        ingest_key="your-tenant-ingest-key",
    )
    try:
        # Start the session
        await emitter.start(title="fix auth bug", project_hint="my-project")

        # Do work... (heartbeats run automatically if using supervised())
        await do_work()

        # End the session
        await emitter.end("done", cost_usd=0.05)
    finally:
        await emitter.close()

asyncio.run(main())
```

### Supervised mode (recommended)

The supervised context manager handles start, heartbeats, and end automatically:

```python
async with emitter.supervised(title="fix auth bug"):
    await do_work()  # heartbeats sent every 60s
# session.end emitted automatically with status "done"
# (or "failed" if an exception was raised)
```

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `AGENTOS_ENDPOINT` | No | `http://localhost:8080/api/events/work` | Agent OS ingestion URL |
| `AGENTOS_INGEST_KEY` | **Yes** | — | Per-tenant ingest key |
| `AGENTOS_HEARTBEAT_S` | No | `60` | Heartbeat interval in seconds |

## Events Emitted

| Event | Kind | Status | When |
|---|---|---|---|
| Session start | `session.start` | `running` | When the session begins |
| Heartbeat | `session.heartbeat` | `running` | Every ~60s while running |
| Session end | `session.end` | `done`/`failed`/`cancelled` | On completion or failure |

All events include:
- Fresh `event_id` (UUID) per event for idempotent retry
- Stable `session_id` across all events in a session
- `host` (hostname), `pid` (process ID)
- `liveness_mode: supervised`
- `branch` and `sha` (auto-detected from git when available)
- `cwd`, `project_hint`, `external_ref`, `tenant` when known

## Contract Compliance

This emitter codes to the frozen [work-event contract v1.1](https://github.com/tim4net/agent-os/blob/main/docs/work-event-contract.md):
- Exact JSON shape per §2
- Required headers: `X-AgentOS-Ingest-Key` + `Idempotency-Key`
- Supervised liveness with periodic heartbeats
- Terminal-only `session.end` statuses (`done`, `failed`, `cancelled`)
- No fabricated fields — omits what it can't determine

## Running Tests

```bash
pip install pytest pytest-asyncio
python -m pytest emitters/hermes/test_emitter.py -v
```

## Files

- `emitter.py` — the emitter implementation
- `test_emitter.py` — unit tests (37 tests)
- `README.md` — this file
