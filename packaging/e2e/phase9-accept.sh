#!/bin/sh
# Phase 9 TLS settings acceptance. Cloud proves API and cookies, not public ACME.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase9.cj

fail() {
  echo "PHASE9_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null

curl -fsS -b "$CJ" "$API/api/v1/certs" | grep -q '"fingerprint"' || fail "certs status"

CODE=$(curl -sS -o /tmp/ndl-p9-noconfirm.json -w '%{http_code}' -b "$CJ" \
  -H 'Content-Type: application/json' \
  -d '{"common_name":"phase9","sans":["localhost"]}' \
  "$API/api/v1/certs/generate")
[ "$CODE" = "409" ] || fail "generate without confirm HTTP $CODE"

curl -fsS -b "$CJ" -H 'Content-Type: application/json' -H 'X-Nodal-Confirm: enable-tls' \
  -d '{"common_name":"phase9","sans":["localhost"]}' \
  "$API/api/v1/certs/generate" | grep -q '"enabled":true' || fail "generate"

echo "PHASE9_ACCEPT_OK"
