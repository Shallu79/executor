#!/usr/bin/env bash
# Smoke test the API: signup → execute → result, plus auth checks.
# Requires the stack up (docker compose up -d --build).
set -euo pipefail
cd "$(dirname "$0")/.."

BASE="${API_BASE:-http://127.0.0.1:8080}"

echo "== health =="
curl -s "$BASE/healthz"; echo
curl -s "$BASE/readyz"; echo

EMAIL="smoke+$(date +%s)@example.com"

echo "== signup =="
signup=$(curl -s -X POST "$BASE/api/v1/auth/signup" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"super-secret\"}")
echo "$signup"
KEY=$(echo "$signup" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
[ -n "$KEY" ] || { echo "no api_key in signup response"; exit 1; }

echo "== auth (expect 401 without key) =="
curl -s -o /dev/null -w "no key: %{http_code}\n" -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -d '{"language":"python","code":"print(1)"}'
curl -s -o /dev/null -w "bad key: %{http_code}\n" -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -H "X-API-Key: wrong" -d '{"language":"python","code":"print(1)"}'

echo "== execute (sync) =="
resp=$(curl -s -X POST "$BASE/api/v1/execute" \
  -H 'Content-Type: application/json' -H "X-API-Key: $KEY" \
  -d '{"language":"python","code":"print(42)"}')
echo "$resp"

# If the sync wait expired (504), poll until completed.
jid=$(echo "$resp" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
[ -n "$jid" ] || { echo "no job_id returned"; exit 1; }
if echo "$resp" | grep -q '"completed"'; then
  echo "sync result OK"
else
  echo "== sync wait timed out, polling =="
  for _ in $(seq 1 30); do
    out=$(curl -s "$BASE/api/v1/jobs/$jid" -H "X-API-Key: $KEY")
    if echo "$out" | grep -q '"completed"'; then
      echo "$out"
      break
    fi
    sleep 0.5
  done
fi

echo "== signin + history =="
token=$(curl -s -X POST "$BASE/api/v1/auth/signin" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"super-secret\"}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s "$BASE/api/v1/me" -H "Authorization: Bearer $token"; echo
curl -s "$BASE/api/v1/submissions" -H "Authorization: Bearer $token"; echo

echo "smoke test done"
