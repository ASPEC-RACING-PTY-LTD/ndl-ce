#!/bin/sh
# Phase 32 migrate acceptance. Does not require two physical boxes.
set -eu

fail() {
  echo "PHASE32_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0029_phase32.sql || fail "migration"
grep -q ownership_epoch migrations/0029_phase32.sql || fail "ownership_epoch"
grep -q migrate_jobs migrations/0029_phase32.sql || fail "migrate_jobs"
grep -q 'incoming must be defer' internal/qemu/validate.go || fail "incoming defer"
echo "PHASE32_ACCEPT_OK"
