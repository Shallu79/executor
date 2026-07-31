#!/usr/bin/env bash
# Smoke test the API end-to-end: submit a job, poll until it completes.
# Requires the stack up (docker compose up -d --build) and, for a real
# result, at least one worker running.
set -euo pipefail
cd "$(dirname "$0")/.."

BASE="${API_BASE:-http://127.0.0.1:8080}"
KEY="${API_KEY:-dev-secret-key}"

echo "== health =="
curl -s "$BASE/healthz"; echo
curl -s "$BASE/readyz"; echo

echo "== auth (expect 401) =="
curl -s -o /dev/null -w "no key: %{http_code}\n" -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -d '{"language":"python","code":"print(1)"}'
curl -s -o /dev/null -w "wrong key: %{http_code}\n" -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -H "X-API-Key: wrong" -d '{"language":"python","code":"print(1)"}'

echo "== submit (sync — expect result in the same response) =="
resp=$(curl -s -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -H "X-API-Key: $KEY" \
  -d '{"language":"python","code":"print(42)"}')
echo "$resp"

# If the wait window expired (504 / processing), poll the job until it lands.
jid=$(echo "$resp" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
[ -n "$jid" ] || { echo "no job_id returned"; exit 1; }

if echo "$resp" | grep -q '"completed"'; then
  echo "sync result OK"
  exit 0
fi

echo "== sync wait timed out, polling =="
for _ in $(seq 1 30); do
  out=$(curl -s "$BASE/api/v1/jobs/$jid" -H "X-API-Key: $KEY")
  if echo "$out" | grep -q '"completed"'; then
    echo "$out"
    exit 0
  fi
  sleep 0.5
done

echo "timed out waiting for job $jid"
exit 1
