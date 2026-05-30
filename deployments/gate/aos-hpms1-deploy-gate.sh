#!/usr/bin/env bash
# aos-hpms1-deploy-gate.sh — Lead's post-merge deploy gate (runs on zbook, drives hpms1).
#
# THE real CI per Tim's decision: no GitHub Actions. After a merge to main, hpms1
# (the actual deploy target) pulls origin/main, rebuilds images, applies pending
# additive migrations, restarts the quadlet stack, and health-checks /api/health.
# Red -> deploy.sh auto-rolls images back. This proves the real container/deploy
# environment, not a dev box.
#
# Usage:
#   aos-hpms1-deploy-gate.sh            # full gate: deploy + verify (MUTATES hpms1)
#   aos-hpms1-deploy-gate.sh --build-only   # SAFE: build images from origin/main on
#                                           # hpms1, do NOT migrate/promote/restart
#
# Exit 0 = gate green. Non-zero = gate red (and deploy.sh already rolled back).
set -uo pipefail

# NOTE: this script may run under a Hermes profile where $HOME is the profile home
# (e.g. /home/tim/.hermes/profiles/executor/home), NOT the real user home. All LOCAL
# paths (Obsidian, repo) must therefore be anchored to an explicit real-home base.
# REMOTE_RECIPE is a path ON hpms1 — it must use hpms1's $HOME, so we let the remote
# shell expand it (single-quoted, expanded remotely), never the local $HOME.
REAL_HOME="${AOS_REAL_HOME:-/home/tim}"
HOST="${AOS_HPMS1_HOST:-hpms1}"
REMOTE_RECIPE="${AOS_REMOTE_RECIPE:-\$HOME/deploy/agent-os/deployments/deploy.sh}"  # \$HOME expands on hpms1
LEDGER="${AOS_LEDGER:-$REAL_HOME/Obsidian/projects/agent-os/findings-ledger.md}"
RUNLOG="${AOS_RUNLOG:-$REAL_HOME/Obsidian/projects/agent-os/autonomous-run-log.md}"
HALT="${AOS_HALT:-$REAL_HOME/code/agent-os/.autonomy-halt}"
MODE="${1:-}"

ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log() { echo "[deploy-gate $(ts)] $*"; }

# kill switch
if [ -f "$HALT" ]; then log "halt sentinel present — skipping deploy gate"; exit 0; fi

# ensure remote canonical checkout exists + recipe is current (pull first so the
# recipe we run is the merged one)
ssh -o BatchMode=yes -o ConnectTimeout=8 "$HOST" bash -lc '"
  set -e
  CHECKOUT_DIR=\"\$HOME/deploy/agent-os\"
  if [ ! -d \"\$CHECKOUT_DIR/.git\" ]; then
    git clone --branch main https://github.com/tim4net/agent-os.git \"\$CHECKOUT_DIR\"
  fi
  cd \"\$CHECKOUT_DIR\" && git fetch --prune origin && git reset --hard origin/main
"' || { log "could not sync remote checkout on $HOST"; exit 2; }

if [ "$MODE" = "--build-only" ]; then
  log "BUILD-ONLY: building images from origin/main on $HOST (no migrate/promote/restart)"
  ssh -o BatchMode=yes "$HOST" bash -lc '"
    set -e
    cd \"\$HOME/deploy/agent-os\"
    podman build -f deployments/Containerfile.api -t agent-os-api:candidate .
    podman build -f deployments/Containerfile.web -t agent-os-web:candidate .
    echo BUILD_ONLY_OK
  "'
  rc=$?
  if [ $rc -eq 0 ]; then log "build-only PASS — images compile from origin/main"; else log "build-only FAIL rc=$rc"; fi
  exit $rc
fi

# full gate. REMOTE_RECIPE holds a literal $HOME that must expand on hpms1, so we pass
# the command single-quoted from the local shell and let the remote login shell expand it.
log "running deploy gate on $HOST"
OUT="$(ssh -o BatchMode=yes "$HOST" "bash -lc 'bash $REMOTE_RECIPE'" 2>&1)"
rc=$?
echo "$OUT" | sed 's/^/  /'

VERDICT=$(echo "$OUT" | grep -oE 'DEPLOY_OK sha=[^ ]+ migrations=[0-9]+' | tail -1)
SHA=$(echo "$VERDICT" | grep -oE 'sha=[^ ]+' | cut -d= -f2)

if [ $rc -eq 0 ] && [ -n "$VERDICT" ]; then
  log "GATE GREEN — $VERDICT"
  printf -- "- %s · deploy-gate · hpms1 **GREEN** %s\n" "$(ts)" "$VERDICT" >> "$RUNLOG"
  printf -- "| %s | deploy | - | hpms1 | lead | n/a | pass | deploy-gate | infra | hpms1 pulled origin/main, built images, migrated, restarted, /api/health 200 (%s) |\n" "$(ts)" "$VERDICT" >> "$LEDGER"
  exit 0
else
  log "GATE RED rc=$rc — deploy.sh handled rollback; main image deploy did NOT go healthy"
  printf -- "- %s · deploy-gate · hpms1 **RED** (rc=%s) — rolled back; see output\n" "$(ts)" "$rc" >> "$RUNLOG"
  printf -- "| %s | deploy | - | hpms1 | lead | n/a | critical | deploy-gate-red | infra | hpms1 deploy of origin/main failed health check; images rolled back. rc=%s |\n" "$(ts)" "$rc" >> "$LEDGER"
  exit 1
fi
