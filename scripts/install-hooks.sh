#!/usr/bin/env bash
# scripts/install-hooks.sh — point git at the version-controlled hooks.
# Run once per clone. Idempotent.
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
chmod +x scripts/hooks/* 2>/dev/null || true
git config core.hooksPath scripts/hooks
echo "installed: core.hooksPath -> scripts/hooks"
echo "hooks active: $(ls scripts/hooks | tr '\n' ' ')"
echo "note: hpms1 deploy gate (deployments/deploy.sh) is the authoritative CI; these hooks are a local pre-filter only."
