#!/usr/bin/env bash
# Seed rich, realistic SPOG demo data for a local agent-os instance.
# Idempotent: ingest keys use ON CONFLICT DO NOTHING, work events use deterministic
# event_id values, and seeded ledger rows are replaced by marker before POSTing.

set -Eeuo pipefail

API_URL="${API_URL:-http://localhost:8420}"
PG_CONTAINER="${PG_CONTAINER:-aos-ui-pg}"
PG_USER="${PG_USER:-agentos}"
PG_DB="${PG_DB:-agentos}"
PSQL=(podman exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 -q)

PERSONAL_KEY="${PERSONAL_KEY:-seed-personal-key}"
DAYJOB_KEY="${DAYJOB_KEY:-seed-work-key}"

log() {
  printf '[seed-ui-demo] %s\n' "$*" >&2
}

sha256_hex() {
  printf '%s' "$1" | sha256sum | cut -d' ' -f1
}

psql_exec() {
  "${PSQL[@]}" "$@"
}

curl_json() {
  local method="$1" url="$2" data="$3"
  shift 3
  local response http body
  response="$(curl -sS -w $'\n%{http_code}' -X "$method" "$url" \
    -H 'Content-Type: application/json' "$@" --data "$data")"
  http="${response##*$'\n'}"
  body="${response%$'\n'*}"
  if [[ ! "$http" =~ ^2[0-9][0-9]$ ]]; then
    printf 'ERROR: %s %s returned HTTP %s\n%s\n' "$method" "$url" "$http" "$body" >&2
    exit 1
  fi
  printf '%s' "$body"
}

post_work_event() {
  local key="$1" event_id="$2" host="$3" harness="$4" kind="$5" session_id="$6"
  local status="$7" mode="$8" pid="$9" project_hint="${10}" external_ref="${11}"
  local branch="${12}" sha="${13}" cwd="${14}" title="${15}" cost_usd="${16}"
  local turns="${17}" agent_name="${18}" phase="${19}"
  local ts body response
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  body="$(EVENT_ID="$event_id" HOST="$host" HARNESS="$harness" KIND="$kind" SESSION_ID="$session_id" \
    STATUS="$status" MODE="$mode" PID_VALUE="$pid" PROJECT_HINT="$project_hint" EXTERNAL_REF="$external_ref" \
    BRANCH="$branch" SHA_VALUE="$sha" CWD_VALUE="$cwd" TITLE_VALUE="$title" COST_USD="$cost_usd" \
    TURNS_VALUE="$turns" AGENT_NAME="$agent_name" PHASE_VALUE="$phase" TS_VALUE="$ts" python3 - <<'PY'
import json
import os

_turns = int(os.environ["TURNS_VALUE"] or "0")
_harness = os.environ["HARNESS"]
# Per-harness model + context window (Option A: provider is inferred from these by
# the server's provider map). Realistic token density derived from turns so the
# usage panel has meaningful, non-fabricated-looking numbers.
_model_map = {
    "claude":      ("claude-opus-4-8", 200000, 5200),
    "hermes":      ("claude-sonnet-4-6", 200000, 4300),
    "antigravity": ("gemini-3.5-flash", 1000000, 3100),
    "codex":       ("gpt-5.5", 256000, 4800),
    "generic":     ("gpt-5.5", 256000, 4800),
}
_model, _ctx, _per_turn = _model_map.get(_harness, ("unknown", 128000, 3500))
_tokens_used = _turns * _per_turn if _turns > 0 else 0

_telemetry = {
    "model": _model,
    "context_window": _ctx,
    "turns": _turns,
}
if _tokens_used > 0:
    _telemetry["tokens_used"] = _tokens_used

body = {
    "schema": "agentos.work_event/v1",
    "event_id": os.environ["EVENT_ID"],
    "host": os.environ["HOST"],
    "harness": os.environ["HARNESS"],
    "kind": os.environ["KIND"],
    "session_id": os.environ["SESSION_ID"],
    "ts": os.environ["TS_VALUE"],
    "status": os.environ["STATUS"],
    "liveness_mode": os.environ["MODE"],
    "project_hint": os.environ["PROJECT_HINT"],
    "external_ref": os.environ["EXTERNAL_REF"],
    "branch": os.environ["BRANCH"],
    "sha": os.environ["SHA_VALUE"],
    "cwd": os.environ["CWD_VALUE"],
    "title": os.environ["TITLE_VALUE"],
    "payload": {
        "seed": "ui-demo",
        "agent": os.environ["AGENT_NAME"],
        "phase": os.environ["PHASE_VALUE"],
        "telemetry": _telemetry,
        "tags": ["demo", "spog", os.environ["PROJECT_HINT"]],
    },
}
# cost_usd is ONLY meaningful for metered providers. claude/hermes (anthropic) and
# antigravity (google) are subscription here, so we deliberately omit cost_usd for
# them — the server suppresses it anyway, and omitting keeps the seed honest.
_subscription_harnesses = {"claude", "hermes", "antigravity"}
if os.environ["PID_VALUE"]:
    body["pid"] = int(os.environ["PID_VALUE"])
if os.environ["COST_USD"] and _harness not in _subscription_harnesses:
    body["cost_usd"] = float(os.environ["COST_USD"])
# Remove empty optional string fields, but keep required status/liveness fields.
for key in ["project_hint", "external_ref", "branch", "sha", "cwd", "title"]:
    if body.get(key) == "":
        body.pop(key)
print(json.dumps(body, separators=(",", ":")))
PY
)"

  response="$(curl_json POST "$API_URL/api/events/work" "$body" \
    -H "X-AgentOS-Ingest-Key: $key" \
    -H "Idempotency-Key: $event_id")"
  log "work event ${event_id} ${kind} ${session_id}: ${response}"
}

post_ledger() {
  local path="$1" json="$2" response
  response="$(curl_json POST "$API_URL$path" "$json")"
  log "POST ${path}: ${response}"
}

verify_endpoint() {
  local label="$1" url="$2" array_key="$3"
  local json
  json="$(curl -sS "$url")"
  printf '%s\t%s\n' "$label" "$json"
  if ! JSON_INPUT="$json" python3 - "$array_key" <<'PY'
import json
import os
import sys
key = sys.argv[1]
try:
    data = json.loads(os.environ["JSON_INPUT"])
except Exception as exc:
    print(f"invalid JSON: {exc}", file=sys.stderr)
    sys.exit(1)
if key == "__array__":
    value = data
else:
    value = data.get(key)
if not isinstance(value, list) or len(value) == 0:
    print(f"expected non-empty {key}; got {value!r}", file=sys.stderr)
    sys.exit(1)
PY
  then
    printf 'ERROR: verification failed for %s (%s)\n' "$label" "$url" >&2
    exit 1
  fi
}

log "checking API health at ${API_URL}"
curl -fsS "$API_URL/api/health" >/dev/null

log "minting deterministic per-tenant ingest keys"
personal_hash="$(sha256_hex "$PERSONAL_KEY")"
dayjob_hash="$(sha256_hex "$DAYJOB_KEY")"
psql_exec <<SQL
INSERT INTO ingest_keys (key_hash, tenant, label)
VALUES ('${personal_hash}', 'personal', 'seed')
ON CONFLICT (key_hash) DO NOTHING;
INSERT INTO ingest_keys (key_hash, tenant, label)
VALUES ('${dayjob_hash}', 'dayjob', 'seed')
ON CONFLICT (key_hash) DO NOTHING;
SQL

log "posting deterministic work events through /api/events/work"
# personal / agent-os: agy + Hermes + one failed session + one stale supervised session.
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000101" "zbook" "antigravity" "session.start" "personal-agy-vector-cache" "running" "bounded" "" "agent-os" "#57" "feature/vector-cache" "0f1ce001" "/home/tim/work/agent-os" "agy indexing vector cache for SPOG" "" "6" "agy" "start"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000102" "zbook" "antigravity" "session.end" "personal-agy-vector-cache" "done" "bounded" "" "agent-os" "#57" "feature/vector-cache" "0f1ce9a1" "/home/tim/work/agent-os" "agy shipped vector cache for SPOG" "8.90" "34" "agy" "terminal"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000103" "zbook" "hermes" "session.start" "personal-hermes-spog-polish" "running" "bounded" "" "agent-os" "#58" "ui/spog-polish" "0f1ce002" "/home/tim/work/agent-os/ui-spog" "Hermes polishing SPOG panels" "" "4" "Hermes" "start"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000104" "zbook" "hermes" "session.end" "personal-hermes-spog-polish" "done" "bounded" "" "agent-os" "#58" "ui/spog-polish" "0f1ce9b2" "/home/tim/work/agent-os/ui-spog" "Hermes completed SPOG polish" "3.80" "22" "Hermes" "terminal"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000105" "zbook" "claude" "session.start" "personal-claude-docs-pass" "running" "bounded" "" "agent-os" "#61" "docs/spog-demo" "0f1ce003" "/home/tim/work/agent-os/docs" "Claude drafting operator runbook" "" "3" "Claude" "start"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000106" "zbook" "claude" "session.end" "personal-claude-docs-pass" "done" "bounded" "" "agent-os" "#61" "docs/spog-demo" "0f1ce9c3" "/home/tim/work/agent-os/docs" "Claude finished operator runbook" "2.15" "15" "Claude" "terminal"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000107" "zbook" "hermes" "session.start" "personal-hermes-migration-flake" "running" "bounded" "" "agent-os" "#59" "bugfix/migration-seed" "0f1ce004" "/home/tim/work/agent-os/ui-spog" "Hermes investigating migration flake" "" "2" "Hermes" "start"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000108" "zbook" "hermes" "session.end" "personal-hermes-migration-flake" "failed" "bounded" "" "agent-os" "#59" "bugfix/migration-seed" "0f1ce9d4" "/home/tim/work/agent-os/ui-spog" "Hermes migration seed flaked on CI" "1.25" "9" "Hermes" "terminal"
post_work_event "$PERSONAL_KEY" "11111111-1111-4111-8111-000000000109" "zbook" "antigravity" "session.start" "personal-agy-stale-refactor" "running" "supervised" "42420" "agent-os" "#62" "refactor/session-liveness" "0f1ce005" "/home/tim/work/agent-os/ui-spog" "agy long-running refactor with missing heartbeat" "0.75" "5" "agy" "start"

# dayjob / atlas: Lead and Roux work-packages, plus Roux Gate-1 build failure on wp-o5.
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000201" "build-ci-01" "generic" "session.start" "work-lead-wp-o1-plan" "running" "bounded" "" "atlas" "#44" "wp-o1" "d1a90001" "/srv/atlas" "Lead decomposing wp-o1 orchestration plan" "" "9" "Lead" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000202" "build-ci-01" "generic" "session.end" "work-lead-wp-o1-plan" "done" "bounded" "" "atlas" "#44" "wp-o1" "d1a9c0de" "/srv/atlas" "Lead merged wp-o1 orchestration plan" "14.10" "41" "Lead" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000203" "build-ci-02" "hermes" "session.start" "work-roux-wp-o5-build" "running" "bounded" "" "atlas" "#57" "wp-o5" "d1a90002" "/srv/atlas" "Roux hardening wp-o5 build gate" "" "11" "Roux" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000204" "build-ci-02" "hermes" "session.end" "work-roux-wp-o5-build" "done" "bounded" "" "atlas" "#57" "wp-o5" "d1a9beef" "/srv/atlas" "Roux passed wp-o5 build gate" "21.40" "63" "Roux" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000205" "build-ci-03" "hermes" "session.start" "work-roux-gate1-error" "running" "bounded" "" "atlas" "#60" "wp-o5" "d1a90003" "/srv/atlas" "Roux Gate-1 build on branch wp-o5" "" "7" "Roux" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000206" "build-ci-03" "hermes" "session.end" "work-roux-gate1-error" "failed" "bounded" "" "atlas" "#60" "wp-o5" "d1a9f00d" "/srv/atlas" "Roux Gate-1 build error on wp-o5" "6.70" "24" "Roux" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000207" "build-dev-01" "generic" "session.start" "work-lead-live-review" "running" "supervised" "52525" "atlas" "#63" "wp-o6" "d1a90004" "/srv/atlas" "Lead live review of wp-o6 recurrence query" "2.40" "12" "Lead" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000208" "build-dev-01" "generic" "session.heartbeat" "work-lead-live-review" "running" "supervised" "52525" "atlas" "#63" "wp-o6" "d1a90005" "/srv/atlas" "Lead heartbeat while reviewing wp-o6" "" "13" "Lead" "heartbeat"
# Codex on the OpenAI API = METERED. This is the one agent whose dollar cost is real,
# so the usage panel can demonstrate the $-overlay path alongside subscription agents.
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000209" "build-ci-04" "codex" "session.start" "work-codex-api-batch" "running" "bounded" "" "atlas" "#64" "wp-o7" "d1a90006" "/srv/atlas" "Codex (metered API) batch-fixing wp-o7 lint" "" "9" "Codex" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000210" "build-ci-04" "codex" "session.end" "work-codex-api-batch" "done" "bounded" "" "atlas" "#64" "wp-o7" "d1a9c12e" "/srv/atlas" "Codex (metered API) finished wp-o7 lint batch" "4.80" "18" "Codex" "terminal"

log "adjusting seeded event clocks for realistic recency and stale-session coverage"
psql_exec <<'SQL'
WITH seed(event_id, age) AS (
  VALUES
    ('11111111-1111-4111-8111-000000000101'::uuid, interval '9 minutes 30 seconds'),
    ('11111111-1111-4111-8111-000000000102'::uuid, interval '8 minutes 45 seconds'),
    ('11111111-1111-4111-8111-000000000103'::uuid, interval '8 minutes 20 seconds'),
    ('11111111-1111-4111-8111-000000000104'::uuid, interval '7 minutes 40 seconds'),
    ('11111111-1111-4111-8111-000000000105'::uuid, interval '7 minutes 10 seconds'),
    ('11111111-1111-4111-8111-000000000106'::uuid, interval '6 minutes 25 seconds'),
    ('11111111-1111-4111-8111-000000000107'::uuid, interval '5 minutes 55 seconds'),
    ('11111111-1111-4111-8111-000000000108'::uuid, interval '5 minutes 15 seconds'),
    ('11111111-1111-4111-8111-000000000109'::uuid, interval '8 minutes 10 seconds'),
    ('22222222-2222-4222-8222-000000000201'::uuid, interval '9 minutes 40 seconds'),
    ('22222222-2222-4222-8222-000000000202'::uuid, interval '8 minutes 55 seconds'),
    ('22222222-2222-4222-8222-000000000203'::uuid, interval '8 minutes 30 seconds'),
    ('22222222-2222-4222-8222-000000000204'::uuid, interval '7 minutes 30 seconds'),
    ('22222222-2222-4222-8222-000000000205'::uuid, interval '6 minutes 50 seconds'),
    ('22222222-2222-4222-8222-000000000206'::uuid, interval '6 minutes 5 seconds'),
    ('22222222-2222-4222-8222-000000000207'::uuid, interval '2 minutes 0 seconds'),
    ('22222222-2222-4222-8222-000000000208'::uuid, interval '45 seconds'),
    ('22222222-2222-4222-8222-000000000209'::uuid, interval '4 minutes 30 seconds'),
    ('22222222-2222-4222-8222-000000000210'::uuid, interval '3 minutes 15 seconds')
)
UPDATE work_events AS we
SET received_at = NOW() - seed.age,
    ts = NOW() - seed.age
FROM seed
WHERE we.event_id = seed.event_id;
SQL

log "replacing seeded ledger rows and POSTing run-log/finding demo records"
psql_exec <<'SQL'
DELETE FROM run_log
WHERE summary LIKE '[seed-ui-demo]%'
   OR payload @> '{"seed":"ui-demo"}'::jsonb;
DELETE FROM findings
WHERE summary LIKE '[seed-ui-demo]%'
   OR root_cause LIKE 'seed-ui-demo:%';
SQL

post_ledger "/api/ledger/runs" '{"event_type":"merge","pr_ref":"#57","wp_ref":"wp-o5","summary":"[seed-ui-demo] Roux merged wp-o5 build gate hardening after green CI","payload":{"seed":"ui-demo","tenant":"dayjob","project":"atlas","agent":"Roux","branch":"wp-o5"}}'
post_ledger "/api/ledger/runs" '{"event_type":"gate","pr_ref":"#44","wp_ref":"wp-o1","summary":"[seed-ui-demo] Lead passed Gate-2 orchestration design review","payload":{"seed":"ui-demo","tenant":"dayjob","project":"atlas","agent":"Lead","gate":2}}'
post_ledger "/api/ledger/runs" '{"event_type":"build","pr_ref":"#60","wp_ref":"wp-o5","summary":"[seed-ui-demo] Roux hit Gate-1 build error before retrying wp-o5","payload":{"seed":"ui-demo","tenant":"dayjob","project":"atlas","agent":"Roux","status":"failed"}}'
post_ledger "/api/ledger/runs" '{"event_type":"merge","pr_ref":"#58","wp_ref":"spog-polish","summary":"[seed-ui-demo] Hermes merged SPOG polish for agent-os personal dashboard","payload":{"seed":"ui-demo","tenant":"personal","project":"agent-os","agent":"Hermes"}}'
post_ledger "/api/ledger/runs" '{"event_type":"deploy","pr_ref":"#61","wp_ref":"demo-seed","summary":"[seed-ui-demo] agy refreshed local demo data for fleet and spend panels","payload":{"seed":"ui-demo","tenant":"personal","project":"agent-os","agent":"agy"}}'

post_ledger "/api/ledger/findings" '{"pr_ref":"#60","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"medium","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux pushed wp-o5 before running the local build target","summary":"[seed-ui-demo] wp-o5 push missed local build preflight"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#57","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"medium","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux retried wp-o5 without verifying the generated asset bundle","summary":"[seed-ui-demo] wp-o5 retry still skipped build preflight"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#62","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"high","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux opened another wp-o5 update before CI artifacts were present","summary":"[seed-ui-demo] recurring wp-o5 build preflight gap"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#44","wp_ref":"wp-o1","gate":2,"author_agent":"Lead","model":"gpt-5.5","severity":"high","class":"missing-test","root_cause":"seed-ui-demo: acceptance coverage omitted the tenant isolation case","summary":"[seed-ui-demo] Lead needs tenant isolation test for wp-o1"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#58","wp_ref":"spog-polish","gate":3,"author_agent":"Hermes","model":"gpt-5.5","severity":"low","class":"copy-drift","root_cause":"seed-ui-demo: panel empty-state copy diverged from incident semantics","summary":"[seed-ui-demo] Hermes should align SPOG empty-state copy"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#57","wp_ref":"demo-seed","gate":2,"author_agent":"agy","model":"antigravity-coder","severity":"medium","class":"missing-observability","root_cause":"seed-ui-demo: seed run did not previously prove recurrence endpoint was populated","summary":"[seed-ui-demo] agy added recurrence verification coverage"}'

log "seeding productivity planes (goals, tasks, workflows, skills, pipeline) through the real API"
# These planes are populated via the same public REST endpoints the UI uses, so a
# successful seed doubles as an end-to-end proof that each create/list path works.
# Idempotent: every record carries a deterministic title/name; on re-run we delete
# any existing record whose title/name matches our known seed set, then recreate.
# Titles are kept clean (no marker text) so the demo UI reads naturally.
API_URL="$API_URL" python3 - <<'PY'
import json
import os
import urllib.request
import urllib.error

API = os.environ["API_URL"].rstrip("/")


def req(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(
        API + path, data=data, method=method,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(r) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw.strip() else None)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def as_list(payload):
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        for key in ("items", "goals", "tasks", "workflows", "skills", "records"):
            if isinstance(payload.get(key), list):
                return payload[key]
    return []


def resolve_agent(name):
    _, agents = req("GET", "/api/agents")
    for a in as_list(agents):
        if a.get("name") == name:
            return a["id"]
    return None


def reseed(plane, path, key_field, records, match_field="description"):
    """Delete prior seed records, then (re)create them.

    Idempotency is scoped tightly: a record is deleted ONLY when both its
    ``key_field`` (title/name) AND its ``match_field`` (a content field we
    author, e.g. description or type) are byte-identical to one of our seed
    records. This mirrors the marker convention the rest of this script uses
    for work_events/ledger rows, so a user-authored record that merely shares
    a seed title is never destroyed — only our own previous seed output is
    replaced. Keeps demo titles clean (no marker text) without the footgun.

    A record may carry an optional ``_put`` dict of fields to apply via a
    follow-up PUT — used for state the create endpoint forces to a default
    (pipeline status, goal progress), so the demo board spreads across stages.
    """
    # Build the set of (key, match) tuples that identify OUR seed records.
    owned = {(r[key_field], r.get(match_field)) for r in records}
    status, existing = req("GET", path)
    if status != 200:
        raise SystemExit(f"FAIL seeding {plane}: GET {path} -> {status} {existing}")
    deleted = 0
    for item in as_list(existing):
        ident = (item.get(key_field), item.get(match_field))
        if ident in owned and item.get("id"):
            code, body = req("DELETE", f"{path}/{item['id']}")
            if code in (200, 204):
                deleted += 1
            elif code != 404:  # 404 = already gone (race); anything else is fatal
                raise SystemExit(f"FAIL seeding {plane}: DELETE {item['id']} -> {code} {body}")
    created = 0
    for r in records:
        put = r.pop("_put", None)
        code, resp = req("POST", path, r)
        if code in (200, 201):
            created += 1
            if put and isinstance(resp, dict) and resp.get("id"):
                pcode, presp = req("PUT", f"{path}/{resp['id']}", put)
                if pcode not in (200, 201):
                    raise SystemExit(
                        f"FAIL finalizing {plane} {resp['id']}: PUT -> {pcode} {presp}")
        else:
            raise SystemExit(f"FAIL seeding {plane}: POST {path} -> {code} {resp}")
    print(f"[seed-ui-demo]   {plane}: deleted {deleted}, created {created}", flush=True)


roux = resolve_agent("roux")
crawbot = resolve_agent("crawbot")

# --- Goals (Build > Goals): high-level objectives across personal + work ----
reseed("goals", "/api/goals", "title", [
    {"title": "Ship Agent OS SPOG v1", "status": "active",
     "description": "Single pane of glass over every agent, project, and tenant — fleet, spend, control, and observability planes live.",
     "target_date": "2026-06-30", "_put": {"progress": 72}},
    {"title": "Cut fleet over to the work-event plane", "status": "active",
     "description": "Retire the delegation-proxy timeline; all agent activity flows through agentos.work_event/v1 ingestion.",
     "target_date": "2026-07-15", "_put": {"progress": 40}},
    {"title": "Automate home-lab backup + restore drills", "status": "paused",
     "description": "Personal: nightly restic snapshots to the NAS with a monthly automated restore verification.",
     "target_date": "2026-08-01", "_put": {"progress": 15}},
    {"title": "Three-gate review loop on every merge", "status": "completed",
     "description": "Build + tests, independent model spec-compliance, and live render — authoritative locally when CI runners are inert.",
     "target_date": "2026-05-31", "_put": {"progress": 100}},
])

# --- Tasks (Build > Board): kanban across all four columns, linked to agents --
reseed("tasks", "/api/tasks", "title", [
    {"title": "Wire productivity tabs to live data", "status": "in_progress",
     "priority": 1, "agent_id": roux,
     "description": "Seed goals/tasks/workflows/skills/pipeline through the public API so every tab renders real records."},
    {"title": "Add tenant isolation test for wp-o1", "status": "backlog",
     "priority": 2, "agent_id": roux,
     "description": "Gate-2 flagged missing acceptance coverage for the tenant-scoped orchestration path."},
    {"title": "Fix Sidebar nested-button a11y warning", "status": "backlog",
     "priority": 3, "agent_id": crawbot,
     "description": "Configure-agent button is nested inside the agent-select button; split into sibling controls."},
    {"title": "Review wp-o5 build-gate hardening", "status": "review",
     "priority": 1, "agent_id": roux,
     "description": "Confirm the local build preflight now blocks pushes that skip artifact generation."},
    {"title": "Migrate react-hooks eslint to v10", "status": "done",
     "priority": 2, "agent_id": crawbot,
     "description": "54 errors to zero with no behavior change; merged in #59."},
    {"title": "Rebuild spend panel usage-first", "status": "done",
     "priority": 2, "agent_id": roux,
     "description": "Subscription sessions show token usage always; dollars only for metered providers."},
])

# --- Workflows (Automate): realistic multi-step agent workflows ---------------
reseed("workflows", "/api/workflows", "name", [
    {"name": "Nightly PR digest", "agent_id": roux,
     "description": "Summarize the day's merged PRs and post a digest to Telegram each evening.",
     "steps": [
         {"name": "Collect merged PRs", "prompt": "List all PRs merged to main in the last 24h with author and summary."},
         {"name": "Summarize", "prompt": "Write a concise digest grouping PRs by work package."},
         {"name": "Deliver", "prompt": "Send the digest to the team Telegram channel."},
     ]},
    {"name": "Gate-2 review dispatch", "agent_id": roux,
     "description": "On a new in-review PR, dispatch an independent model for spec-compliance review.",
     "steps": [
         {"name": "Detect in-review PR", "prompt": "Find the single PR currently awaiting Gate-2 review."},
         {"name": "Run independent review", "prompt": "Review the diff against acceptance criteria; report PASS/FAIL with evidence."},
         {"name": "Record finding", "prompt": "Post the verdict and any findings to the ledger."},
     ]},
    {"name": "Skill bloat scan", "agent_id": crawbot,
     "description": "Weekly scan for oversized or stale skills and memory entries.",
     "steps": [
         {"name": "Scan skills + memory", "prompt": "Measure skill sizes and memory utilization against thresholds."},
         {"name": "Report overruns", "prompt": "Flag anything over threshold and suggest consolidation."},
     ]},
])

# --- Skills (Knowledge > Skills): realistic procedural skills -----------------
reseed("skills", "/api/skills", "name", [
    {"name": "three-gate-merge", "category": "devops", "agent_id": roux,
     "description": "Authoritative local merge gate: build+tests, independent review, live render.",
     "triggers": ["before merge", "pr ready", "gate check"],
     "content": "# Three-Gate Merge\n\n1. Gate 1 — real build, vet, and full test suite green on a fresh DB.\n2. Gate 2 — independent model reviews the diff for spec compliance; re-verify every PASS claim.\n3. Gate 3 — live render in Chrome; zero new console errors.\n\nWhen CI runners are inert, the local loop is authoritative (ADR-005 D6)."},
    {"name": "idempotent-api-seed", "category": "data", "agent_id": roux,
     "description": "Seed demo data through the real REST API so the seed doubles as an e2e proof.",
     "triggers": ["seed demo", "populate planes", "empty tabs"],
     "content": "# Idempotent API Seed\n\nFor each plane: list existing, delete records whose title/name match the known seed set, then recreate. Keeps titles clean (no markers) while staying re-runnable. A green seed proves every create/list path end-to-end."},
    {"name": "clean-worktree-grounding", "category": "github", "agent_id": crawbot,
     "description": "Ground every session: verify repo root, remote, branch, worktree before substantive work.",
     "triggers": ["new session", "before build", "worktree"],
     "content": "# Clean Worktree Grounding\n\nBefore touching code: confirm repo root, remote URL, current branch, and that the target task belongs in THIS worktree. Cut a fresh worktree off origin/main rather than reusing a contested one."},
])

# --- Pipeline (Build > Pipeline): content across all four stages --------------
# match_field="type" because pipeline records have no description field; type is
# returned plainly by the API. Advanced items carry real `content` via _put so a
# "published" article isn't an empty shell (outline-only).
reseed("pipeline", "/api/pipeline", "title", [
    {"title": "Agent OS launch announcement", "type": "blog",
     "outline": "What a single pane of glass over every agent and project unlocks for solo builders.",
     "_put": {"status": "published",
              "content": "# Agent OS\n\nOne operating system for every project — personal and professional. Agent OS gives you a single pane of glass over every agent, work package, and tenant: live fleet status, token spend, the control plane, and full observability. No more tab-hopping between a dozen dashboards; the whole org of agents is one screen."}},
    {"title": "How the three-gate review loop works", "type": "blog",
     "outline": "Build+tests, independent spec review, live render — and why local gates can be authoritative.",
     "_put": {"status": "human_review",
              "content": "# The Three-Gate Review Loop\n\nEvery merge passes three gates: (1) a real build plus the full test suite on a fresh database, (2) an independent model reviewing the diff for spec compliance — and every PASS is re-verified, (3) a live render in the browser with zero new console errors. When CI runners are inert, this local loop is authoritative."}},
    {"title": "Fleet radar: see every agent at once", "type": "social",
     "outline": "Short thread on the work-event plane and live fleet observability.",
     "_put": {"status": "ai_review",
              "content": "Thread: every agent across every box reports into one work-event plane. Fleet radar shows who's running, who's stale, and who failed — in real time, across tenants. 1/4"}},
    {"title": "Weekly build digest — work-package roundup", "type": "newsletter",
     "outline": "Template for the automated nightly/weekly digest of merged work packages."},
], match_field="type")

print("[seed-ui-demo]   productivity planes seeded", flush=True)
PY

log "seeding Knowledge vault notes through /api/memory/file"
# Notes are written via the same POST endpoint the UI uses. WriteFile overwrites
# by path and MkdirAll's parent dirs, so this is naturally idempotent — no delete
# pass needed. This also auto-creates the vault directory on a fresh deploy.
API_URL="$API_URL" python3 - <<'PY'
import json
import os
import urllib.request
import urllib.error

API = os.environ["API_URL"].rstrip("/")

NOTES = {
    "Welcome.md": (
        "# Welcome to Agent OS Knowledge\n\n"
        "This is your vault — every note, doc, and reference across all your "
        "projects in one place. Files here are full-text indexed and searchable "
        "from the Knowledge tab.\n\n"
        "- **Files** — browse and read your Markdown vault\n"
        "- **Skills** — procedural know-how your agents can load\n"
    ),
    "Projects/Agent-OS.md": (
        "# Agent OS\n\n"
        "An operating system for all your projects, personal and professional. "
        "A single pane of glass over every agent, work package, and tenant.\n\n"
        "## Planes\n"
        "- Fleet — live agent sessions\n"
        "- Spend — token usage and metered cost\n"
        "- Control — orchestrator mode and queue\n"
        "- Observe — the work-event stream\n"
    ),
    "Projects/Three-Gate-Review.md": (
        "# The Three-Gate Review Loop\n\n"
        "1. Build plus the full test suite on a fresh database\n"
        "2. Independent model spec-compliance review — re-verify every PASS\n"
        "3. Live render in the browser with zero new console errors\n\n"
        "When CI runners are inert, this local loop is authoritative.\n"
    ),
    "Notes/Daily-Standup.md": (
        "# Daily Standup\n\n"
        "- Wired the productivity tabs (Build, Knowledge, Automate) to live data\n"
        "- Fixed the Knowledge > Files 500 on a missing vault (graceful empty)\n"
        "- Next: navigation polish\n"
    ),
}


def write_note(path, content):
    body = json.dumps({"path": path, "content": content}).encode()
    r = urllib.request.Request(
        API + "/api/memory/file", data=body, method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(r) as resp:
            return resp.status
    except urllib.error.HTTPError as e:
        raise SystemExit(f"FAIL seeding note {path}: {e.code} {e.read().decode()}")


written = 0
for path, content in NOTES.items():
    if write_note(path, content) in (200, 201):
        written += 1
if written != len(NOTES):
    raise SystemExit(f"FAIL seeding notes: wrote {written} of {len(NOTES)}")
print(f"[seed-ui-demo]   memory notes: wrote {written}", flush=True)
PY

log "verification JSON follows; each listed endpoint must have a non-empty array"
verify_endpoint "spend_personal" "$API_URL/api/spend?group_by=agent&tenant=personal" "rows"
verify_endpoint "spend_dayjob" "$API_URL/api/spend?group_by=agent&tenant=dayjob" "rows"
verify_endpoint "incidents_dayjob" "$API_URL/api/incidents?tenant=dayjob" "incidents"
verify_endpoint "incidents_personal" "$API_URL/api/incidents?tenant=personal" "incidents"
verify_endpoint "fleet_personal" "$API_URL/api/fleet?tenant=personal" "sessions"
verify_endpoint "fleet_dayjob" "$API_URL/api/fleet?tenant=dayjob" "sessions"
verify_endpoint "ledger_runs" "$API_URL/api/ledger/runs" "records"
verify_endpoint "ledger_findings" "$API_URL/api/ledger/findings" "records"
verify_endpoint "ledger_recurrence" "$API_URL/api/ledger/recurrence?min_count=2" "records"
verify_endpoint "goals" "$API_URL/api/goals" "__array__"
verify_endpoint "tasks" "$API_URL/api/tasks" "__array__"
verify_endpoint "workflows" "$API_URL/api/workflows" "__array__"
verify_endpoint "skills" "$API_URL/api/skills" "__array__"
verify_endpoint "pipeline" "$API_URL/api/pipeline" "__array__"
verify_endpoint "memory_tree" "$API_URL/api/memory/tree?depth=2" "__array__"

log "done"
