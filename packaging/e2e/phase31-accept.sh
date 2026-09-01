#!/bin/sh
# Phase 31 placement acceptance. Does not live-migrate.
set -eu

fail() {
  echo "PHASE31_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0028_phase31.sql || fail "migration"
grep -q node_groups migrations/0028_phase31.sql || fail "node_groups"
grep -q node_maintenance migrations/0028_phase31.sql || fail "maintenance"
echo "PHASE31_ACCEPT_OK"
