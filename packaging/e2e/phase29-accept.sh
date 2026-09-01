#!/bin/sh
# Phase 29 host-platform and ISO acceptance. Does not boot an ISO.
set -eu

fail() {
  echo "PHASE29_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f packaging/iso/mkosi.conf || fail "mkosi.conf"
grep -q "Distribution=debian" packaging/iso/mkosi.conf || fail "debian"
grep -q "nodal" packaging/iso/mkosi.conf || fail "nodal metapackage"
echo "PHASE29_ACCEPT_OK"
