#!/usr/bin/env bash
# Smoke-test POST /deploy-app against a running orchestrator.
# Usage:
#   ORCHESTRATOR_URL=http://127.0.0.1:8081 \
#   ORCHESTRATOR_SHARED_SECRET=... \
#   TEST_GIT_URL=https://github.com/traefik/whoami \
#   TEST_PROJECT_ID=abc12 \
#   TEST_DOCKERFILE=path/in/repo/Dockerfile \
#   TEST_CONTAINER_PORT=3000 \
#   TEST_SERVICE_PORT=80 \
#   ./deploy/scripts/test-deploy-app.sh
set -euo pipefail

ORCHESTRATOR_URL="${ORCHESTRATOR_URL:-http://127.0.0.1:8081}"
TEST_GIT_URL="${TEST_GIT_URL:-https://github.com/traefik/whoami}"
TEST_GIT_REF="${TEST_GIT_REF:-refs/heads/master}"
TEST_DOCKERFILE="${TEST_DOCKERFILE:-}"
TEST_CONTAINER_PORT="${TEST_CONTAINER_PORT:-}"
TEST_SERVICE_PORT="${TEST_SERVICE_PORT:-}"

if [[ -z "${TEST_PROJECT_ID:-}" ]]; then
  TEST_PROJECT_ID="t$(openssl rand -hex 3)"
fi

BASE="${ORCHESTRATOR_URL%/}"
SECRET_HEADER=()
if [[ -n "${ORCHESTRATOR_SHARED_SECRET:-}" ]]; then
  SECRET_HEADER=(-H "X-Orchestrator-Secret: ${ORCHESTRATOR_SHARED_SECRET}")
fi

echo "POST ${BASE}/deploy-app projectID=${TEST_PROJECT_ID} repo=${TEST_GIT_URL}"

JSON="{\"githubRepoEndpoint\":\"${TEST_GIT_URL}\",\"projectID\":\"${TEST_PROJECT_ID}\",\"gitRef\":\"${TEST_GIT_REF}\""
if [[ -n "$TEST_DOCKERFILE" ]]; then
  JSON+=",\"dockerfile\":\"${TEST_DOCKERFILE}\""
fi
if [[ -n "$TEST_CONTAINER_PORT" ]]; then
  JSON+=",\"containerPort\":${TEST_CONTAINER_PORT}"
fi
if [[ -n "$TEST_SERVICE_PORT" ]]; then
  JSON+=",\"servicePort\":${TEST_SERVICE_PORT}"
fi
JSON+="}"

curl -sS -N -X POST "${BASE}/deploy-app" \
  -H 'Content-Type: application/json' \
  "${SECRET_HEADER[@]}" \
  -d "${JSON}" \
  --max-time 7200
echo
