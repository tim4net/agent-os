#!/usr/bin/env bash
# =============================================================================
# Agent OS — Docker Deploy Script (tf-docker)
# =============================================================================
# Deploys Agent OS as a self-contained Docker Compose stack.
#
# This replaces deploy.sh (which was podman/quadlet-specific for hpms1).
# The compose file IS the deterministic control — this script just wraps it.
#
# Usage (on the deploy host, e.g. tf-docker):
#   cd /srv/agent-os && bash deployments/deploy-docker.sh
#
# What it does:
#   1. Pull latest code from origin/main
#   2. docker compose build (multi-stage Go + web build)
#   3. docker compose up -d (recreates containers if images changed)
#   4. Health-check /api/health
#   5. Fail-fast on any error
# =============================================================================
set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deployments/docker-compose.yml}"
HEALTH_URL="${HEALTH_URL:-http://localhost:${WEB_PORT:-8080}/api/health}"

log() { echo "[deploy-docker $(date -u +%H:%M:%SZ)] $*"; }
fail() { echo "[deploy-docker FAIL] $*" >&2; exit 1; }

# ---------- 1. Sync code ----------
log "pulling latest code"
git fetch --prune origin
git checkout "${BRANCH:-main}"
git reset --hard "origin/${BRANCH:-main}"
DEPLOY_SHA="$(git rev-parse --short HEAD)"
log "checkout at $DEPLOY_SHA"

# ---------- 2. Build images ----------
log "building images via docker compose"
docker compose -f "$COMPOSE_FILE" build --pull || fail "image build failed"

# ---------- 3. Deploy ----------
log "starting stack"
# --remove-orphans cleans up any stale containers from previous config
docker compose -f "$COMPOSE_FILE" up -d --remove-orphans || fail "compose up failed"

# ---------- 4. Health check ----------
healthy() {
  local tries="${1:-30}"
  for _ in $(seq 1 "$tries"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 4 "$HEALTH_URL" || echo 000)"
    [ "$code" = "200" ] && return 0
    sleep 3
  done
  return 1
}

log "waiting for health check at $HEALTH_URL"
if healthy 30; then
  log "HEALTHY at $DEPLOY_SHA — deploy complete"
  echo "DEPLOY_OK sha=$DEPLOY_SHA"
  exit 0
else
  fail "deploy of $DEPLOY_SHA failed health check — check logs: docker compose -f $COMPOSE_FILE logs"
fi
