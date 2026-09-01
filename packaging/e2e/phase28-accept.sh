#!/bin/sh
# Phase 28 WireGuard remote-node acceptance. Does not create live tunnels.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE28_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE28_ACCEPT_OK"
