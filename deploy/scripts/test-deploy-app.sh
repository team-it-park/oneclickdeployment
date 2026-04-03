#!/usr/bin/env bash
# Smoke-test POST /deploy-app against a running orchestrator.
# Usage:
#   ORCHESTRATOR_URL=http://127.0.0.1:8081 \
#   ORCHESTRATOR_SHARED_SECRET=... \
#   TEST_GIT_URL=https://github.com/traefik/whoami \
#   TEST_PROJECT_ID=abc12 \
#   ./deploy/scripts/test-deploy-app.sh
set -euo pipefail

ORCHESTRATOR_URL="${ORCHESTRATOR_URL:-http://127.0.0.1:8081}"
TEST_GIT_URL="${TEST_GIT_URL:-https://github.com/traefik/whoami}"
TEST_GIT_REF="${TEST_GIT_REF:-refs/heads/master}"
# whoami listens on 80 — override when testing APP_CONTAINER_PORT=8080 images
TEST_APP_CONTAINER_PORT="${TEST_APP_CONTAINER_PORT:-80}"

if [[ -z "${TEST_PROJECT_ID:-}" ]]; then
  TEST_PROJECT_ID="t$(openssl rand -hex 3)"
fi

BASE="${ORCHESTRATOR_URL%/}"
SECRET_HEADER=()
if [[ -n "${ORCHESTRATOR_SHARED_SECRET:-}" ]]; then
  SECRET_HEADER=(-H "X-Orchestrator-Secret: ${ORCHESTRATOR_SHARED_SECRET}")
fi

echo "POST ${BASE}/deploy-app projectID=${TEST_PROJECT_ID} repo=${TEST_GIT_URL}"
echo "Note: orchestrator must use APP_CONTAINER_PORT=${TEST_APP_CONTAINER_PORT} for this image."

curl -sS -N -X POST "${BASE}/deploy-app" \
  -H 'Content-Type: application/json' \
  "${SECRET_HEADER[@]}" \
  -d "{\"githubRepoEndpoint\":\"${TEST_GIT_URL}\",\"projectID\":\"${TEST_PROJECT_ID}\",\"gitRef\":\"${TEST_GIT_REF}\"}" \
  --max-time 7200
echo
