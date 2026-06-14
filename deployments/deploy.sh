#!/usr/bin/env bash
# deployments/deploy.sh — canonical Agent OS deploy recipe (runs ON hpms1).
#
# Lead's deploy-gate cron SSHes to hpms1 and runs this. hpms1 owns its own
# deploy; this repo owns the recipe (version-controlled, reproducible).
#
# What it does, in order, fail-fast:
#   1. Pull origin/main into the canonical checkout (CHECKOUT_DIR).
#   2. Build api+web images tagged :candidate (multi-stage Go/web build in-container).
#   3. Apply pending additive migrations (version > current max in schema_migrations),
#      each in its own transaction, recording the version. (Interim runner until the
#      in-app migration runner story lands.)
#   4. Re-tag current :latest -> :rollback, promote :candidate -> :latest.
#   5. Restart the quadlet services; health-check /api/health.
#   6. On ANY failure after promote: roll images back to :rollback, restart, exit non-zero.
#
# This script is idempotent and safe to re-run. It NEVER drops/truncates data;
# it only applies additive .up.sql migrations. A migration containing DROP/TRUNCATE
# aborts the run (guard below) — destructive schema change is a human decision.
set -euo pipefail

CHECKOUT_DIR="${CHECKOUT_DIR:-$HOME/deploy/agent-os}"
REPO_URL="${REPO_URL:-https://github.com/tim4net/agent-os.git}"
BRANCH="${BRANCH:-main}"
DB_CONTAINER="${DB_CONTAINER:-agent-os-db}"
DB_USER="${DB_USER:-agentos}"
DB_NAME="${DB_NAME:-agentos}"
API_SVC="${API_SVC:-agent-os-api.service}"
WEB_SVC="${WEB_SVC:-agent-os-web.service}"
HEALTH_URL="${HEALTH_URL:-http://localhost:8080/api/health}"
API_IMG="agent-os-api"
WEB_IMG="agent-os-web"

log() { echo "[deploy $(date -u +%H:%M:%SZ)] $*"; }
fail() { echo "[deploy FAIL] $*" >&2; exit 1; }

# ---------- 1. sync canonical checkout ----------
if [ ! -d "$CHECKOUT_DIR/.git" ]; then
  log "cloning $REPO_URL -> $CHECKOUT_DIR"
  git clone --branch "$BRANCH" "$REPO_URL" "$CHECKOUT_DIR"
fi
cd "$CHECKOUT_DIR"
git fetch --prune origin
git checkout "$BRANCH"
git reset --hard "origin/$BRANCH"
DEPLOY_SHA="$(git rev-parse --short HEAD)"
log "checkout at $DEPLOY_SHA"

# ---------- 2. build candidate images ----------
log "building $API_IMG:candidate"
podman build -f deployments/Containerfile.api -t "$API_IMG:candidate" . \
  || fail "api image build failed"
log "building $WEB_IMG:candidate"
podman build -f deployments/Containerfile.web -t "$WEB_IMG:candidate" . \
  || fail "web image build failed"

# ---------- 3. apply pending additive migrations ----------
psql() { podman exec -i "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" "$@"; }

# ensure tracker table exists (golang-migrate-compatible shape)
psql -v ON_ERROR_STOP=1 -tAc \
  "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);" >/dev/null

CUR="$(psql -tAc "SELECT COALESCE(MAX(version),0) FROM schema_migrations;" | tr -d '[:space:]')"
log "current migration version: $CUR"

shopt -s nullglob
applied=0
for up in $(ls internal/migrations/*.up.sql | sort); do
  ver="$(basename "$up" | grep -oE '^[0-9]+' | sed 's/^0*//')"
  [ -z "$ver" ] && continue
  if [ "$ver" -le "$CUR" ]; then continue; fi
  # guard: refuse destructive migrations in the automated gate
  if grep -iE '\b(DROP|TRUNCATE)\b' "$up" | grep -vqiE 'ON DELETE|IF EXISTS .* ADD'; then
    if grep -iqE '\bDROP\s+(TABLE|COLUMN|SCHEMA|DATABASE)\b|\bTRUNCATE\b' "$up"; then
      fail "migration $ver ($up) contains destructive ops — aborting; apply by hand after review"
    fi
  fi
  log "applying migration $ver: $(basename "$up")"
  # each migration in its own transaction; record version atomically
  if ! psql -v ON_ERROR_STOP=1 --single-transaction \
        -c "$(cat "$up")" \
        -c "INSERT INTO schema_migrations(version,dirty) VALUES ($ver,false) ON CONFLICT (version) DO NOTHING;"; then
    fail "migration $ver failed — DB unchanged for this step (transaction rolled back)"
  fi
  applied=$((applied+1))
done
log "migrations applied this run: $applied"

# ---------- 4. promote candidate -> latest (save rollback) ----------
promote() {
  local img="$1"
  if podman image exists "$img:latest"; then
    podman tag "$img:latest" "$img:rollback"
  fi
  podman tag "$img:candidate" "$img:latest"
}
rollback_imgs() {
  for img in "$API_IMG" "$WEB_IMG"; do
    if podman image exists "$img:rollback"; then
      podman tag "$img:rollback" "$img:latest"
      log "rolled $img back to previous :latest"
    fi
  done
}
promote "$API_IMG"
promote "$WEB_IMG"

# ---------- 5. restart services + health check ----------
restart_stack() {
  systemctl --user restart "$WEB_SVC" "$API_SVC"
}
healthy() {
  local tries="${1:-20}"
  for _ in $(seq 1 "$tries"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 4 "$HEALTH_URL" || echo 000)"
    [ "$code" = "200" ] && return 0
    sleep 2
  done
  return 1
}

log "restarting stack"
restart_stack
if healthy 25; then
  log "HEALTHY at $DEPLOY_SHA (migrations +$applied) — deploy OK"
  # prune candidate tags (latest now points at the same image id)
  podman rmi "$API_IMG:candidate" "$WEB_IMG:candidate" 2>/dev/null || true
  # prune dangling images to prevent disk accumulation (22GB+ recurrence)
  podman image prune -f 2>/dev/null || true
  echo "DEPLOY_OK sha=$DEPLOY_SHA migrations=$applied"
  exit 0
fi

# ---------- 6. failure -> roll back ----------
log "health check FAILED — rolling back images"
rollback_imgs
restart_stack
if healthy 20; then
  fail "deploy of $DEPLOY_SHA failed health check; rolled back to previous images (stack healthy again)"
else
  fail "deploy of $DEPLOY_SHA failed AND rollback did not recover health — MANUAL INTERVENTION NEEDED"
fi
