#!/bin/sh
# Phase 23 object-storage backup. Cloud proves API health.
# Live MinIO is a virt job, not every commit.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE23_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE23_ACCEPT_OK"
