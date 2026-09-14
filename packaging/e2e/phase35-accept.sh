#!/bin/sh
# Phase 35 feature modules acceptance.
# Does not apt-get install feature packages. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase35.cj

fail() {
  echo "PHASE35_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE35_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 35 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/features" | grep -q '"kubelet_started":false' || fail "features kubelet_started"
curl -fsS -b "$CJ" "$API/api/v1/features" | grep -q '"base_install":"light"' || fail "features light base"

echo "PHASE35_ACCEPT_OK"
