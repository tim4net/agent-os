# Agent OS

**Agent OS is a single pane of glass (SPOG) that observes and correlates every
agent's real work** — across your dayjob (Rewst/Riftwing, tracked in Shortcut)
and personal projects (tracked in GitHub Issues or Obsidian) — without
re-implementing each agent's UX and without standing up a competing tracker.

It is an **observability plane, not a control plane** ([ADR-001](docs/adr/adr-001-observability-plane.md)):
agents keep running in their native surfaces (Claude Code in a worktree, `agy` in
a terminal, Hermes in Telegram/CLI). Their *work* — sessions, artifacts, commits,
PRs, state changes, cost — flows into Agent OS as a common **work-event** stream.
Agent OS is where you *see, search, and correlate* that work; it is not where you
operate the agents from.

---

## Architecture at a glance

```
  Agents (native surfaces)          Thin emitters            Agent OS
  ──────────────────────            ─────────────            ────────────────────────
  Claude Code  ──(Stop hook)──▶  emitters/claude    ─┐
  Antigravity  ──(history)────▶  emitters/antigravity ┼─▶ POST /api/events/work
  Hermes       ──(delegations)─▶  emitters/hermes     ┘        │
  Host procs   ──(liveness)────▶  emitters/host_reporter ──▶ /api/host/liveness
                                                              │
                                                              ▼
                                              ┌───────────────────────────────┐
                                              │  Go API (internal/api)         │
                                              │  • work-event ingestion        │
                                              │  • correlation engine          │
                                              │  • ledger / spend / incidents  │
                                              │  • control plane (orchestrator)│
                                              │  • read-only tracker mirror    │
                                              └──────────────┬────────────────┘
                                                             ▼
                                              PostgreSQL 17 (sqlc + migrations)
                                                             ▲
                                              React/Vite SPOG web UI (web/)
```

### Core design decisions (the *why*)

The architecture is governed by eight accepted ADRs in [`docs/adr/`](docs/adr/).
Read these first if you are extending or rebuilding the system:

- **[ADR-001](docs/adr/adr-001-observability-plane.md) — Observability plane, not control plane.** Agent OS observes; it does not proxy agent chat. The unit of record is the **work-event** (`{harness, session_id, project, external_ref?, branch?, sha?, artifacts[], status, cost?, ts}`), not the chat stream. Trackers are **read-only** mirrors — agents write to the canonical tracker themselves.
- **[ADR-002](docs/adr/adr-002-tenancy-knowledge-boundaries.md) — Tenancy & knowledge boundaries.** Separate by *kind*, not project: **payload** (secrets, client data, tickets) is hard-walled and never crosses; **pattern** (generic craft) flows via a deliberate generalize-and-scrub step. A *tenant* is one confidentiality boundary. This is why most read endpoints **require a `tenant` query param** and return `400` without it — that is correct behavior, not a bug.
- **[ADR-003](docs/adr/adr-003-app-instance-registry.md) — App instance registry.** Self-registering app instances + health prober.
- **[ADR-004](docs/adr/adr-004-chat-primary-multimodal.md) — Chat is the primary multimodal surface; TUI is fallback.**
- **[ADR-005](docs/adr/adr-005-autonomous-build-loops.md) — Autonomous two-agent build loops.** The Lead/Roux fleet, 3-gate review, and the orchestrator control plane (`/api/control`) that this repo was largely built by.
- **[ADR-006](docs/adr/adr-006-dogfooding-improvement-flywheel.md) — Dogfooding & the agent improvement flywheel.**
- **[ADR-007](docs/adr/adr-007-loop-tuning-and-ui-delegation.md) — Loop tuning, gate independence & UI delegation.**
- **[ADR-008](docs/adr/adr-008-relay-on-fleet-mailbox.md) — Agent relay on the fleet mailbox (`_mail`), not GitHub.**

---

## Repository layout

| Path | What lives here |
|------|-----------------|
| `cmd/server` | Main HTTP server — boots config, runs migrations, mounts the API at `/api`, serves the web UI |
| `cmd/orchestrator` | Standalone orchestrator driver (control-plane engine, [ADR-005](docs/adr/adr-005-autonomous-build-loops.md)) |
| `cmd/gen-openapi` | Generates [`docs/openapi.yaml`](docs/openapi.yaml) by walking the live chi router |
| `internal/api` | All HTTP handlers + the central router (`router.go`). ~99 endpoints |
| `internal/db` | `sqlc`-generated data layer + hand-written queries (`internal/db/queries/*.sql`) |
| `internal/migrations` | **Authoritative schema** — numbered `*.up.sql` / `*.down.sql` (currently 17 up-migrations) |
| `internal/service` | Event bus, activity feed, correlation engine, orchestrator driver |
| `internal/harness` | Harness registry (legacy chat adapters, demoted per ADR-001 D7) |
| `internal/config` | Env-var configuration loader |
| `emitters/` | Thin per-harness work-event emitters (Python): `claude`, `antigravity`, `hermes`, `host_reporter`, `supervisor` |
| `web/` | React + Vite + TypeScript SPOG frontend (~59 source files) |
| `deployments/` | Containerfiles, `deploy.sh` recipe, nginx config, Podman quadlets |
| `docs/` | ADRs, OpenAPI spec, work-event contract, loop prompts |

---

## API documentation

The full route surface is documented in **[`docs/openapi.yaml`](docs/openapi.yaml)**
(OpenAPI 3.1). It is a **generated file** — never hand-edit it. Regenerate it any
time the router changes:

```bash
go run ./cmd/gen-openapi > docs/openapi.yaml
```

The generator (`cmd/gen-openapi`) walks the *actual* `internal/api.(*API).Router()`
via `chi.Walk`, so the spec can never silently drift from the code. The route
surface (method + path + handler) is authoritative; **payload shapes are defined
by the SQL migrations and the `sqlc` query layer**, which remain the source of
truth for request/response bodies.

View it interactively with any OpenAPI viewer, e.g.:

```bash
npx @redocly/cli preview-docs docs/openapi.yaml
```

---

## Running locally

### Prerequisites
- **Go 1.26+**
- **PostgreSQL 17** (the integration suite and runtime target PG17)
- **Node 20+** (for the `web/` frontend)
- **`sqlc` v1.31.1** — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1` (CI generates the data layer; it is never committed)
- **Podman 5+** (or Docker) for the containerized stack

### 1. Database
```bash
# Point the server at any PG17 instance:
export DATABASE_URL="postgres://agentos:agentos@localhost:5432/agentos?sslmode=disable"
```
Migrations run **automatically on server boot** (`db.MigrateUpWithLogger`) — no
separate migrate step needed for local dev.

### 2. Backend
```bash
go build ./...            # build everything
go vet ./...              # vet
go run ./cmd/server       # starts on :8080 (override with PORT)
curl localhost:8080/api/health   # → {"status":"ok","database":"ok"}
```

### 3. Frontend
```bash
cd web
npm install
npm run dev               # Vite dev server
npm run build             # production build (tsc -b && vite build)
npm run test              # vitest
```

### Configuration (environment variables)

All config is env-driven with sensible defaults (`internal/config/config.go`):

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://localhost:5432/agentos?sslmode=disable` | PostgreSQL DSN |
| `PORT` | `8080` | HTTP listen port |
| `LITELLM_URL` | `http://localhost:4000` | LiteLLM gateway for LLM calls |
| `LLM_MODEL` | `local-qwen` | Default model id |
| `OBSIDIAN_PATH` | `./obsidian` | Vault path for memory/notes integration |
| `ARTIFACTS_PATH` | `/data/artifacts` | Artifact storage root |
| `HERMES_SKILLS_PATH` | `/data/hermes-skills` | Hermes skills mount |
| `HERMES_API_KEY` | — | Hermes harness auth |
| `XAI_API_KEY` / `OPENROUTER_API_KEY` / `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) / `FAL_KEY` / `ZAI_API_KEY` | — | Studio image/LLM providers (optional; providers self-mark unavailable without a key) |

---

## Testing

```bash
# Unit tests (no DB):
go test ./...

# Integration tests need a real Postgres. Set AOS_TEST_DSN so the suite runs
# (a SKIPPED integration suite is treated as a failure in CI):
export AOS_TEST_DSN="postgres://agentos:agentos@localhost:55434/agentos_test?sslmode=disable"
go test ./internal/api/...
```

CI (`.github/workflows/ci.yml`) runs on a self-hosted runner: builds, vets,
generates `sqlc`, spins a throwaway PG17 on port 55434, applies all
up-migrations in order, and runs the full suite. See the **CI** section below.

---

## Deployment

The canonical deploy runs **on the host** (`hpms1`) via
[`deployments/deploy.sh`](deployments/deploy.sh) — a fail-fast, idempotent recipe:

1. Pull `origin/main` into the canonical checkout.
2. Build `agent-os-api` + `agent-os-web` images tagged `:candidate`.
3. Apply pending **additive** migrations (destructive `DROP`/`TRUNCATE`
   migrations abort the automated gate — that is a human decision).
4. Re-tag `:latest` → `:rollback`, promote `:candidate` → `:latest`.
5. Restart the Podman quadlet services; health-check `/api/health`.
6. On any post-promote failure: roll images back, restart, exit non-zero.

Services run as Podman quadlets (`deployments/quadlets/`): `agent-os-api`,
`agent-os-web`, `agent-os-db`, on the `agent-os` network.

---

## CI

The project uses a **self-hosted GitHub Actions runner** (not GitHub-hosted) to
avoid the 2,000 minute/month cap on the Free plan. The workflow
(`.github/workflows/ci.yml`) is **inert** until a runner is online.

### Registering the self-hosted runner
1. **Settings → Actions → Runners → New self-hosted runner** in the repo.
2. Choose **Linux** / **x64**.
3. On the runner host install: **Go 1.26+**, **sqlc v1.31.1**, **podman 5+** (or Docker — adjust the `podman` commands), **psql** client.
4. Run the provided `config.sh` and `run.sh` from GitHub's setup page.
5. The runner auto-picks up workflows; verify with a test push.

### What the workflow does
On every **pull request** and **push to `main`**: builds (`go build ./...`),
vets (`go vet ./...`), generates `sqlc` (never committed), starts a throwaway
**Postgres 17** via podman on port 55434, runs all up-migrations in numeric
order, then runs the full suite with `AOS_TEST_DSN` set so integration tests
actually run (a skipped integration suite is a failure). The Postgres container
is cleaned up even on failure.

Until the runner is registered, the local 3-gate loop and post-merge green
baseline ([ADR-005](docs/adr/adr-005-autonomous-build-loops.md) D6) continue to
provide regression defense.

---

## Could you rebuild Agent OS from this repo?

Yes. The reconstruction-grade material now travels *with* the code:
- **Schema** → `internal/migrations/*.up.sql` (authoritative, exact).
- **Data access** → `internal/db/queries/*.sql` (typed `sqlc` contract).
- **API surface** → `docs/openapi.yaml` (generated from the live router).
- **Design intent / the *why*** → the 8 ADRs in `docs/adr/`.
- **Work-event contract** → `docs/work-event-contract.md`.

Start with ADR-001 (what this is) and ADR-002 (tenancy — the rule that shapes
most endpoints), then the migrations, then the OpenAPI spec.
