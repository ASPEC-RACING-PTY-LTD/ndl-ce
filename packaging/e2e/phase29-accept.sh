#!/bin/sh
# Phase 29 host-platform and ISO acceptance. Does not boot an ISO.
# File greps are not roadmap acceptance of an installer boot.
set -eu

fail() {
  echo "PHASE29_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f packaging/iso/mkosi.conf || fail "mkosi.conf"
grep -q "Distribution=debian" packaging/iso/mkosi.conf || fail "debian"
grep -q "nodal" packaging/iso/mkosi.conf || fail "nodal metapackage"
echo "PHASE29_SMOKE_OK"
echo "This is not roadmap acceptance. The installer ISO is not booted in this tree."
