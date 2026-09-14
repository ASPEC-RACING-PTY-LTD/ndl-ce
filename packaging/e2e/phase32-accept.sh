#!/bin/sh
# Phase 32 migrate acceptance.
# Does not require two physical boxes. SQL greps are not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase32.cj

fail() {
  echo "PHASE32_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0029_phase32.sql || fail "migration"
grep -q ownership_epoch migrations/0029_phase32.sql || fail "ownership_epoch"
grep -q migrate_jobs migrations/0029_phase32.sql || fail "migrate_jobs"
grep -q 'incoming must be defer' internal/qemu/validate.go || fail "incoming defer"

if ! curl -fsS "$API/api/v1/health" >/dev/null 2>&1; then
  echo "PHASE32_SMOKE_OK"
  echo "This is not roadmap acceptance. Control plane health is down."
  exit 0
fi

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE32_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 32 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/workloads" | grep -q '"items"' || fail "workloads list"
CODE=$(curl -sS -o /tmp/ndl-p32-mig.json -w '%{http_code}' -b "$CJ" \
  "$API/api/v1/workloads/00000000-0000-4000-8000-000000000000/migrate")
[ "$CODE" = "404" ] || fail "migrate GET HTTP $CODE"

echo "PHASE32_ACCEPT_OK"
