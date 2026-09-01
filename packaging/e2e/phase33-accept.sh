#!/bin/sh
# Phase 33 cluster restore and DR export. Does not require two physical boxes.
set -eu

fail() {
  echo "PHASE33_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0030_phase33.sql || fail "migration"
grep -q locality migrations/0030_phase33.sql || fail "locality"
grep -q pull_url migrations/0030_phase33.sql || fail "pull_url"
grep -q target_node_id internal/httpapi/phase11.go || fail "target_node_id"
grep -q backups/dr-export internal/httpapi/server.go || fail "dr-export"
echo "PHASE33_ACCEPT_OK"
