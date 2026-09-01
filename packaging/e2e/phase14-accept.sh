#!/bin/sh
# Phase 14 GPU assignment acceptance. Does not bind a physical GPU or run DKMS.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE14_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE14_ACCEPT_OK"
