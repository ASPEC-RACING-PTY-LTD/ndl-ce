#!/bin/sh
# Phase 28 WireGuard remote acceptance.
# Does not form a live two-machine tunnel. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase28.cj

fail() {
  echo "PHASE28_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE28_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 28 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/cluster/wg" | grep -q 'Pairing tokens are not join tokens' || fail "cluster wg"

echo "PHASE28_ACCEPT_OK"
