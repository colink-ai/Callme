#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${CALLME_E2E_TMP:-/tmp/callme-e2e}"
PORT="${CALLME_E2E_PORT:-18090}"

rm -rf "${TMP_DIR}"
mkdir -p "${TMP_DIR}/data" "${TMP_DIR}/hermes-home" "${TMP_DIR}/workdir" "${TMP_DIR}/logs"

cat > "${TMP_DIR}/config.yaml" <<YAML
server:
  host: 127.0.0.1
  port: ${PORT}
database:
  driver: sqlite
  dsn: ${TMP_DIR}/data/callme.db
agent:
  type: hermes
  cli_path: ${ROOT_DIR}/auto-test/mock-acp-agent.py
  default_model: e2e-mock
  api_url: http://127.0.0.1:9/v1
  api_token: e2e-token
  hermes_home: ${TMP_DIR}/hermes-home
  work_dir: ${TMP_DIR}/workdir
  prompt_timeout: 30s
auth:
  token_ttl: 2h
session:
  max_active: 2
  max_queue: 5
  idle_warn_after: 5m
  idle_close_after: 10m
  max_duration: 1h
  max_per_client: 2
  queue_poll_seconds: 1
feedback:
  distill_interval: 1h
  audit_interval: 1h
  notes_max_entries: 100
handoff:
  webhook_url: ""
log:
  path: ${TMP_DIR}/logs/callme.log
YAML

cd "${ROOT_DIR}"
exec go run ./cmd/server -config "${TMP_DIR}/config.yaml" -web web/dist
