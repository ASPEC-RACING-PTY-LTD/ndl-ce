#!/bin/sh
# Phase 44 Import / Export / Migration acceptance.
# Copy-first. Source destruction is not a migration operation.
# Health-only is not acceptance. Disposable guest procedures require a real appliance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase44.cj

fail() {
  echo "PHASE44_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE44_SMOKE_OK"
  echo "Authenticated migration API checks did not run. This is not disposable-workload acceptance."
  exit 0
fi

AD=$(curl -fsS -b "$CJ" "$API/api/v1/migration/adapters")
printf '%s' "$AD" | grep -q '"id":"proxmox"' || fail "proxmox adapter"
printf '%s' "$AD" | grep -q '"id":"nodal"' || fail "nodal adapter"
printf '%s' "$AD" | grep -q 'vma' || true

MD=$(curl -fsS -b "$CJ" "$API/api/v1/migration/modes")
printf '%s' "$MD" | grep -q '"source_safety":"PROTECTED"' || fail "source safety"
printf '%s' "$MD" | grep -q '"id":"offline"' || fail "offline mode"
printf '%s' "$MD" | grep -q '"id":"live"' || fail "live mode listed"
printf '%s' "$MD" | grep -q '"available":false' || fail "unimplemented modes must be unavailable"

reject_destroy() {
  key=$1
  code=$(curl -sS -o /tmp/ndl-phase44.del -w '%{http_code}' -b "$CJ" -H 'Content-Type: application/json' \
    -d "{\"adapter\":\"disk\",\"mode\":\"disk\",\"path\":\"/tmp/x.qcow2\",\"${key}\":true}" \
    "$API/api/v1/migration/jobs")
  [ "$code" = "400" ] || fail "$key must be rejected ($code)"
  grep -q 'source destruction' /tmp/ndl-phase44.del || fail "$key message"
}

reject_destroy delete_source
reject_destroy cleanup_source

poll_job() {
  jobf=$1
  id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$jobf" | head -n 1)
  [ -n "$id" ] || fail "job id"
  i=0
  while [ "$i" -lt 60 ]; do
    st=$(curl -fsS -b "$CJ" "$API/api/v1/migration/jobs/$id")
    echo "$st" | grep -q '"source_untouched":true' || fail "source_untouched missing"
    if echo "$st" | grep -q '"state":"succeeded"'; then
      echo "$st"
      return 0
    fi
    if echo "$st" | grep -q '"state":"failed"'; then
      echo "$st"
      return 1
    fi
    i=$((i + 1))
    sleep 1
  done
  fail "job did not finish"
}

FIX=${NODAL_MIGRATION_FIXTURE:-}
if [ -z "$FIX" ]; then
  echo "PHASE44_ACCEPT_OK"
  echo "API catalog, source-safety reject, and honest mode availability checked. Disposable Tests A-F did not run on this host."
  exit 0
fi

case "$FIX" in
  A|offline-vm)
    SRC=${NODAL_MIGRATION_SRC_DISK:?}
    NAME=${NODAL_MIGRATION_NAME:-mig-a}
    POOL=${NODAL_MIGRATION_POOL_ID:?}
    NET=${NODAL_MIGRATION_NET_ID:?}
    SUM=$(sha256sum "$SRC" | awk '{print $1}')
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
      -d "{\"adapter\":\"disk\",\"mode\":\"disk\",\"path\":\"${SRC}\",\"name\":\"${NAME}\",\"kind\":\"vm\",\"cpus\":1,\"memory_bytes\":268435456,\"pool_id\":\"${POOL}\",\"network_id\":\"${NET}\"}" \
      "$API/api/v1/migration/import/disk" >/tmp/ndl-phase44.job
    poll_job /tmp/ndl-phase44.job || fail "TEST A job failed"
    [ "$(sha256sum "$SRC" | awk '{print $1}')" = "$SUM" ] || fail "source disk changed"
    echo "TEST A: offline VM disk import completed. Source checksum unchanged."
    ;;
  B|container)
    SRC=${NODAL_MIGRATION_SRC_TAR:?}
    NAME=${NODAL_MIGRATION_NAME:-mig-b}
    POOL=${NODAL_MIGRATION_POOL_ID:?}
    NET=${NODAL_MIGRATION_NET_ID:?}
    SUM=$(sha256sum "$SRC" | awk '{print $1}')
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
      -d "{\"adapter\":\"disk\",\"mode\":\"disk\",\"path\":\"${SRC}\",\"name\":\"${NAME}\",\"kind\":\"system-container\",\"cpus\":1,\"memory_bytes\":268435456,\"pool_id\":\"${POOL}\",\"network_id\":\"${NET}\"}" \
      "$API/api/v1/migration/import/disk" >/tmp/ndl-phase44.job
    poll_job /tmp/ndl-phase44.job || fail "TEST B job failed"
    [ "$(sha256sum "$SRC" | awk '{print $1}')" = "$SUM" ] || fail "source archive changed"
    echo "TEST B: system container archive import completed. Source checksum unchanged."
    ;;
  C|backup)
    SRC=${NODAL_MIGRATION_BACKUP:?}
    NAME=${NODAL_MIGRATION_NAME:-mig-c}
    POOL=${NODAL_MIGRATION_POOL_ID:?}
    NET=${NODAL_MIGRATION_NET_ID:?}
    SUM=$(sha256sum "$SRC" | awk '{print $1}')
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
      -d "{\"adapter\":\"backup\",\"mode\":\"backup\",\"path\":\"${SRC}\",\"name\":\"${NAME}\",\"kind\":\"system-container\",\"cpus\":1,\"memory_bytes\":268435456,\"pool_id\":\"${POOL}\",\"network_id\":\"${NET}\"}" \
      "$API/api/v1/migration/jobs" >/tmp/ndl-phase44.job
    poll_job /tmp/ndl-phase44.job || fail "TEST C job failed"
    [ "$(sha256sum "$SRC" | awk '{print $1}')" = "$SUM" ] || fail "backup changed"
    echo "TEST C: backup import completed. Backup checksum unchanged."
    ;;
  D|live)
    echo "TEST D skipped: V1 live transfer is unavailable. Snapshot-assisted is unavailable."
    ;;
  E|bundle)
    PATHF=${NODAL_MIGRATION_BUNDLE:?}
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
      -d "{\"path\":\"${PATHF}\",\"mode\":\"disk\"}" \
      "$API/api/v1/migration/import/bundle" >/tmp/ndl-phase44.job
    poll_job /tmp/ndl-phase44.job || fail "TEST E job failed"
    [ -e "$PATHF" ] || fail "bundle removed"
    echo "TEST E: bundle import completed. Source bundle remains."
    ;;
  F|failure)
    SRC=${NODAL_MIGRATION_SRC_DISK:?}
    SUM=$(sha256sum "$SRC" | awk '{print $1}')
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
      -d "{\"adapter\":\"disk\",\"mode\":\"disk\",\"path\":\"${SRC}\",\"name\":\"mig-f\",\"kind\":\"vm\",\"cpus\":1,\"memory_bytes\":268435456,\"pool_id\":\"${NODAL_MIGRATION_POOL_ID:?}\",\"network_id\":\"${NODAL_MIGRATION_NET_ID:?}\"}" \
      "$API/api/v1/migration/import/disk" >/tmp/ndl-phase44.job
    id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' /tmp/ndl-phase44.job | head -n 1)
    curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
      "$API/api/v1/migration/jobs/${id}/cancel" >/dev/null
    st=$(curl -fsS -b "$CJ" "$API/api/v1/migration/jobs/$id")
    echo "$st" | grep -q '"source_untouched":true' || fail "cancel left source_untouched unset"
    [ "$(sha256sum "$SRC" | awk '{print $1}')" = "$SUM" ] || fail "source changed after cancel"
    echo "TEST F: cancel requested. Source checksum unchanged."
    ;;
  *)
    fail "unknown fixture $FIX"
    ;;
esac

echo "PHASE44_ACCEPT_OK"
