#!/bin/sh
# Phase 38 optional Kubernetes acceptance.
# Does not start kubelet. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase38.cj

fail() {
  echo "PHASE38_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE38_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 38 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/kubernetes" | grep -q '"kubelet_started":false' || fail "kubelet_started"
curl -fsS -b "$CJ" "$API/api/v1/features" | grep -q '"kubelet_started":false' || fail "features kubelet_started"

echo "PHASE38_ACCEPT_OK"
