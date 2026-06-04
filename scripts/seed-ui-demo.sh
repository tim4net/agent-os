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
DAYJOB_KEY="${DAYJOB_KEY:-seed-dayjob-key}"

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
        "telemetry": {
            "turns": int(os.environ["TURNS_VALUE"] or "0"),
            "model_calls": max(1, int(os.environ["TURNS_VALUE"] or "0") // 3),
        },
        "tags": ["demo", "spog", os.environ["PROJECT_HINT"]],
    },
}
if os.environ["PID_VALUE"]:
    body["pid"] = int(os.environ["PID_VALUE"])
if os.environ["COST_USD"]:
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

# dayjob / riftwing: Lead and Roux work-packages, plus Roux Gate-1 build failure on wp-o5.
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000201" "rift-ci-01" "generic" "session.start" "dayjob-lead-wp-o1-plan" "running" "bounded" "" "riftwing" "#44" "wp-o1" "d1a90001" "/srv/riftwing" "Lead decomposing wp-o1 orchestration plan" "" "9" "Lead" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000202" "rift-ci-01" "generic" "session.end" "dayjob-lead-wp-o1-plan" "done" "bounded" "" "riftwing" "#44" "wp-o1" "d1a9c0de" "/srv/riftwing" "Lead merged wp-o1 orchestration plan" "14.10" "41" "Lead" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000203" "rift-ci-02" "hermes" "session.start" "dayjob-roux-wp-o5-build" "running" "bounded" "" "riftwing" "#57" "wp-o5" "d1a90002" "/srv/riftwing" "Roux hardening wp-o5 build gate" "" "11" "Roux" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000204" "rift-ci-02" "hermes" "session.end" "dayjob-roux-wp-o5-build" "done" "bounded" "" "riftwing" "#57" "wp-o5" "d1a9beef" "/srv/riftwing" "Roux passed wp-o5 build gate" "21.40" "63" "Roux" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000205" "rift-ci-03" "hermes" "session.start" "dayjob-roux-gate1-error" "running" "bounded" "" "riftwing" "#60" "wp-o5" "d1a90003" "/srv/riftwing" "Roux Gate-1 build on branch wp-o5" "" "7" "Roux" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000206" "rift-ci-03" "hermes" "session.end" "dayjob-roux-gate1-error" "failed" "bounded" "" "riftwing" "#60" "wp-o5" "d1a9f00d" "/srv/riftwing" "Roux Gate-1 build error on wp-o5" "6.70" "24" "Roux" "terminal"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000207" "rift-dev-01" "generic" "session.start" "dayjob-lead-live-review" "running" "supervised" "52525" "riftwing" "#63" "wp-o6" "d1a90004" "/srv/riftwing" "Lead live review of wp-o6 recurrence query" "2.40" "12" "Lead" "start"
post_work_event "$DAYJOB_KEY" "22222222-2222-4222-8222-000000000208" "rift-dev-01" "generic" "session.heartbeat" "dayjob-lead-live-review" "running" "supervised" "52525" "riftwing" "#63" "wp-o6" "d1a90005" "/srv/riftwing" "Lead heartbeat while reviewing wp-o6" "" "13" "Lead" "heartbeat"

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
    ('22222222-2222-4222-8222-000000000208'::uuid, interval '45 seconds')
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

post_ledger "/api/ledger/runs" '{"event_type":"merge","pr_ref":"#57","wp_ref":"wp-o5","summary":"[seed-ui-demo] Roux merged wp-o5 build gate hardening after green CI","payload":{"seed":"ui-demo","tenant":"dayjob","project":"riftwing","agent":"Roux","branch":"wp-o5"}}'
post_ledger "/api/ledger/runs" '{"event_type":"gate","pr_ref":"#44","wp_ref":"wp-o1","summary":"[seed-ui-demo] Lead passed Gate-2 orchestration design review","payload":{"seed":"ui-demo","tenant":"dayjob","project":"riftwing","agent":"Lead","gate":2}}'
post_ledger "/api/ledger/runs" '{"event_type":"build","pr_ref":"#60","wp_ref":"wp-o5","summary":"[seed-ui-demo] Roux hit Gate-1 build error before retrying wp-o5","payload":{"seed":"ui-demo","tenant":"dayjob","project":"riftwing","agent":"Roux","status":"failed"}}'
post_ledger "/api/ledger/runs" '{"event_type":"merge","pr_ref":"#58","wp_ref":"spog-polish","summary":"[seed-ui-demo] Hermes merged SPOG polish for agent-os personal dashboard","payload":{"seed":"ui-demo","tenant":"personal","project":"agent-os","agent":"Hermes"}}'
post_ledger "/api/ledger/runs" '{"event_type":"deploy","pr_ref":"#61","wp_ref":"demo-seed","summary":"[seed-ui-demo] agy refreshed local demo data for fleet and spend panels","payload":{"seed":"ui-demo","tenant":"personal","project":"agent-os","agent":"agy"}}'

post_ledger "/api/ledger/findings" '{"pr_ref":"#60","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"medium","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux pushed wp-o5 before running the local build target","summary":"[seed-ui-demo] wp-o5 push missed local build preflight"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#57","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"medium","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux retried wp-o5 without verifying the generated asset bundle","summary":"[seed-ui-demo] wp-o5 retry still skipped build preflight"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#62","wp_ref":"wp-o5","gate":1,"author_agent":"Roux","model":"gpt-5.5","severity":"high","class":"pushed-before-build","root_cause":"seed-ui-demo: Roux opened another wp-o5 update before CI artifacts were present","summary":"[seed-ui-demo] recurring wp-o5 build preflight gap"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#44","wp_ref":"wp-o1","gate":2,"author_agent":"Lead","model":"gpt-5.5","severity":"high","class":"missing-test","root_cause":"seed-ui-demo: acceptance coverage omitted the tenant isolation case","summary":"[seed-ui-demo] Lead needs tenant isolation test for wp-o1"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#58","wp_ref":"spog-polish","gate":3,"author_agent":"Hermes","model":"gpt-5.5","severity":"low","class":"copy-drift","root_cause":"seed-ui-demo: panel empty-state copy diverged from incident semantics","summary":"[seed-ui-demo] Hermes should align SPOG empty-state copy"}'
post_ledger "/api/ledger/findings" '{"pr_ref":"#57","wp_ref":"demo-seed","gate":2,"author_agent":"agy","model":"antigravity-coder","severity":"medium","class":"missing-observability","root_cause":"seed-ui-demo: seed run did not previously prove recurrence endpoint was populated","summary":"[seed-ui-demo] agy added recurrence verification coverage"}'

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

log "done"
