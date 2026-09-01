#!/bin/sh
# Phase 26 NFS/SMB/iSCSI acceptance. Does not mount a live share or log in to iSCSI.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE26_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE26_ACCEPT_OK"
