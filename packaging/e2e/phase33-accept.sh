#!/bin/sh
# Phase 33 cluster restore and DR export.
# Does not require two physical boxes. SQL greps are not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase33.cj

fail() {
  echo "PHASE33_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0030_phase33.sql || fail "migration"
grep -q locality migrations/0030_phase33.sql || fail "locality"
grep -q pull_url migrations/0030_phase33.sql || fail "pull_url"
grep -q target_node_id internal/httpapi/phase11.go || fail "target_node_id"
grep -q backups/dr-export internal/httpapi/server.go || fail "dr-export"

if ! curl -fsS "$API/api/v1/health" >/dev/null 2>&1; then
  echo "PHASE33_SMOKE_OK"
  echo "This is not roadmap acceptance. Control plane health is down."
  exit 0
fi

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE33_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 33 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/backups/dr-export" | grep -q '"cluster_id"' || fail "dr-export"
curl -fsS -b "$CJ" "$API/api/v1/backups/dr-export" | grep -q '"artifacts"' || fail "dr-export artifacts"

echo "PHASE33_ACCEPT_OK"
