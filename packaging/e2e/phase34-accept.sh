#!/bin/sh
# Phase 34 HA foundations and rolling updates. Does not require a replica cluster.
set -eu

fail() {
  echo "PHASE34_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0031_phase34.sql || fail "migration"
grep -q ha_state migrations/0031_phase34.sql || fail "ha_state"
grep -q rolling_plans migrations/0031_phase34.sql || fail "rolling_plans"
grep -q cluster.promote internal/rbac/rbac.go || fail "cluster.promote"
grep -q cluster/ha internal/httpapi/server.go || fail "ha routes"
echo "PHASE34_ACCEPT_OK"
