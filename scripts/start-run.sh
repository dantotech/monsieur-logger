#!/usr/bin/env sh
# Start a monsieur-logger test run.
# Usage: scripts/start-run.sh <run_id> [logs_per_second] [log_length] [duration_seconds] [levels-json]
# Example: scripts/start-run.sh fb-test-001 1000 512 300 '{"info":8,"warn":1,"error":1}'
set -eu
URL="${MONSIEUR_LOGGER_URL:-http://localhost:8080}"
RUN_ID="${1:?run_id required}"
LPS="${2:-100}"
LEN="${3:-256}"
DUR="${4:-60}"
DEFAULT_LEVELS='{"debug":1,"info":1,"warn":1,"error":1}'
LEVELS="${5:-$DEFAULT_LEVELS}"

curl -sS -X POST "$URL/api/v1/runs" -H 'Content-Type: application/json' -d "{
  \"run_id\": \"$RUN_ID\",
  \"logs_per_second\": $LPS,
  \"log_length\": $LEN,
  \"duration_seconds\": $DUR,
  \"levels\": $LEVELS
}"
