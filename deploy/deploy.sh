#!/usr/bin/env bash
# Deploy task-api by digest, verify it is actually serving, and roll back if it is not.
#
# This is the single deployment implementation: the CI deploy job and a local operator run the
# same script against the same compose file. A deploy that only runs in CI is a deploy nobody can
# rehearse, and one that only runs locally is not a pipeline.
#
#   usage: TASK_API_IMAGE=ghcr.io/<owner>/task-api@sha256:... deploy/deploy.sh
#
# Exit codes: 0 deployed and verified, 1 failed and rolled back, 2 failed with no prior version.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker-compose.deploy.yml"
PROJECT="${TASK_API_PROJECT:-task-api-deploy}"
PORT="${TASK_API_PORT:-8080}"
HEALTH_TIMEOUT="${TASK_API_HEALTH_TIMEOUT:-60}"

: "${TASK_API_IMAGE:?TASK_API_IMAGE must be set}"

compose() { docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"; }
log() { printf '\n>>> %s\n' "$*"; }

# --- capture the currently deployed image so a failed rollout has somewhere to go back to -----
PREVIOUS_IMAGE="$(docker inspect --format '{{.Image}}' "${PROJECT}-app-1" 2>/dev/null || true)"
if [ -n "$PREVIOUS_IMAGE" ]; then
    log "currently deployed: $PREVIOUS_IMAGE"
else
    log "no previous deployment found; this is a first rollout"
fi

log "deploying $TASK_API_IMAGE"
TASK_API_IMAGE="$TASK_API_IMAGE" TASK_API_PORT="$PORT" compose pull --quiet app 2>/dev/null || true

# Not allowed to fail under `set -e`: if the container never reaches healthy, that is precisely
# when the rollback below has to run, so the failure is captured rather than aborting the script.
rollout_ok=1
if ! TASK_API_IMAGE="$TASK_API_IMAGE" TASK_API_PORT="$PORT" \
        compose up -d --wait --wait-timeout "$HEALTH_TIMEOUT" app; then
    log "container did not become healthy within ${HEALTH_TIMEOUT}s"
    rollout_ok=0
fi

# --- verification -----------------------------------------------------------------------------
# `compose up --wait` already gates on the container's HEALTHCHECK. The smoke test below is
# deliberately more than a health probe: /healthz answering says the process is up, not that the
# API works, so this exercises a real write/read/delete round trip through the published port.
BASE="http://localhost:${PORT}"

smoke() {
    local health created id fetched
    health="$(curl -fsS --max-time 5 "$BASE/healthz")"
    [ "$health" = '{"status":"ok"}' ] || { echo "unexpected /healthz: $health"; return 1; }

    created="$(curl -fsS --max-time 5 -X POST "$BASE/tasks" \
        -H 'Content-Type: application/json' -d '{"title":"deploy smoke test"}')"
    id="$(printf '%s' "$created" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')"
    [ -n "$id" ] || { echo "create returned no id: $created"; return 1; }

    fetched="$(curl -fsS --max-time 5 "$BASE/tasks/$id")"
    printf '%s' "$fetched" | grep -q 'deploy smoke test' || { echo "read back failed: $fetched"; return 1; }

    curl -fsS --max-time 5 -o /dev/null -X DELETE "$BASE/tasks/$id"
    curl -fsS --max-time 5 -o /dev/null "$BASE/metrics"
    echo "smoke test passed (created, read back and deleted task $id)"
}

log "running smoke test against $BASE"
if [ "$rollout_ok" = 1 ] && smoke; then
    log "deployed successfully"
    docker inspect --format 'running image: {{.Image}}' "${PROJECT}-app-1"
    exit 0
fi

# --- rollback -----------------------------------------------------------------------------------
log "DEPLOYMENT VERIFICATION FAILED"
compose logs --tail 50 app || true

if [ -z "$PREVIOUS_IMAGE" ]; then
    log "no previous version to roll back to; tearing down"
    compose down --remove-orphans || true
    exit 2
fi

log "rolling back to $PREVIOUS_IMAGE"
# The rollback unit is the image digest: one immutable artifact in, one out. Nothing else about
# the environment changes, so the rollback cannot drift from what was previously running.
TASK_API_IMAGE="$PREVIOUS_IMAGE" TASK_API_PORT="$PORT" compose up -d --wait --wait-timeout "$HEALTH_TIMEOUT" app
log "rolled back"
exit 1
