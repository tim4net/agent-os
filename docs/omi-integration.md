# Omi Wearable Integration (#135)

Ambient context from an [Omi](https://omi.me) (Based Hardware) wearable device,
ingested into the agent-os memory index.

## Status: opt-in / deferred

This integration is **low-priority and niche** (per the gap analysis). It ships
behind a feature flag: the background poller is **only started when
`OMI_API_TOKEN` is set**. With the token unset (the default deployment) the Omi
code is dead code — no goroutine is spawned, no network calls are made, and the
memory index is unaffected. This keeps it out of the way of higher-priority work.

## What it does

The Omi wearable captures ambient audio and, via the Omi cloud, produces
transcripts and structured summaries ("memories"). The Omi adapter:

1. Polls the Omi cloud REST API (`GET /v3/memories`) for new device memories.
2. Normalizes each memory into a source-agnostic `OmiMemory` (title, overview,
   transcript, action items, tags).
3. Upserts it into `memory_index` as ambient context, attributed to the system
   seed owner, tagged `omi` + `ambient`.

Re-syncing the same Omi memory updates the **same** `memory_index` row (the
`file_path` is derived deterministically as `omi://memories/<id>`, and
`UpsertMemory` does an `ON CONFLICT (file_path) DO UPDATE`), so there are no
duplicates across poll cycles.

A high-water mark (the newest `created_at` successfully ingested) is tracked so
each cycle fetches only incremental data.

## Configuration

| Env var         | Required | Default             | Description                          |
|-----------------|----------|---------------------|--------------------------------------|
| `OMI_API_TOKEN` | no       | _(unset = disabled)_| Omi cloud bearer token. **When unset, the integration is disabled.** |
| `OMI_BASE_URL`  | no       | `https://api.omi.dev` | Override the Omi cloud API root.   |

Set the token to enable:

```bash
export OMI_API_TOKEN=...
export OMI_BASE_URL=https://api.omi.dev   # optional
```

The poller syncs once on startup (initial backfill), then every 10 minutes.

## Architecture

```
cmd/server (main.go)
  └─ if OMI_API_TOKEN != "": start OmiIngester goroutine
        ├─ OmiClient  (OmiSource impl) ── GET /v3/memories ── Omi cloud
        └─ OmiIngester
             ├─ ListSince(highWaterMark) → []OmiMemory (ascending)
             ├─ omiMemoryToParams()      → db.UpsertMemoryParams
             └─ writer.UpsertMemory()    → memory_index row
```

- `internal/service/omi.go` — `OmiMemory`, the `OmiSource` interface, and
  `OmiClient` (the production REST client). Resilient to both wire shapes Omi
  emits (`{"memories":[...]}` envelope and bare array).
- `internal/service/omi_ingest.go` — `OmiIngester` background poller +
  high-water-mark tracking + the pure mapping to `db.UpsertMemoryParams`.

Both files are unit-tested with injected fakes (no live network, no Postgres).
A single failing upsert within a batch is logged and skipped rather than
aborting the whole cycle, and the high-water mark only advances to the newest
**successful** ingest.

## Testing

```bash
# Unit tests (no DB required):
go test ./internal/service/ -run 'Omi|DecodeMemories|NormalizeOmi|AssembleTranscript|ParseOmiTime|BuildOmi|EnsureTag'
```
