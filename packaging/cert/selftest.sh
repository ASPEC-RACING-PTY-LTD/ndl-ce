#!/usr/bin/env bash
# Self-test for the CE 1.0 certification harness library.
#
# Runs in any environment (no No-dal appliance required). It proves the parts of
# the harness that must be trustworthy regardless of hardware:
#   - the production-safety guard refuses non-disposable and denylisted names,
#   - the ledger records results,
#   - the verdict logic fails loudly (non-zero) on FAIL or BLOCKED-PHYSICAL and
#     only returns success when every gate genuinely PASSed.
#
# This is what stops the harness from ever claiming CE 1.0 from mocks.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

CERT_RUN_ID="selftest-$$"
CERT_OUT="$(mktemp -d)"
export CERT_RUN_ID CERT_OUT
# shellcheck source=packaging/cert/lib.sh
. "${HERE}/lib.sh"

fails=0
check() {
  local desc="$1" want="$2" got="$3"
  if [ "$want" = "$got" ]; then
    printf 'ok    %s\n' "$desc"
  else
    printf 'FAIL  %s (want %s, got %s)\n' "$desc" "$want" "$got"
    fails=$((fails + 1))
  fi
}

cert_init >/dev/null 2>&1

# --- Safety guard -----------------------------------------------------------
# A name without the run prefix must be refused (cert_die exits 3).
( assert_disposable "some-production-vm" ) >/dev/null 2>&1
check "guard refuses non-disposable name" 3 "$?"

# The production denylist (Skila) must be refused even if prefixed.
( assert_disposable "${CERT_PREFIX}-Skila-clone" ) >/dev/null 2>&1
check "guard refuses denylisted production name" 3 "$?"

# A properly prefixed disposable name is accepted.
( assert_disposable "${CERT_PREFIX}-ct" ) >/dev/null 2>&1
check "guard accepts disposable name" 0 "$?"

# track_resource must also refuse non-disposable names.
( track_resource workload "prod-thing" ) >/dev/null 2>&1
check "track_resource refuses non-disposable" 3 "$?"

# --- Ledger -----------------------------------------------------------------
: > "$CERT_RESULTS"
gate_pass 01 "example pass" "evidence"
gate_blocked 02 "example blocked" "no hardware"
check "ledger has two rows" 2 "$(wc -l < "$CERT_RESULTS" | tr -d ' ')"
check "ledger records PASS" 1 "$(grep -c '^PASS' "$CERT_RESULTS")"
check "ledger records BLOCKED-PHYSICAL" 1 "$(grep -c '^BLOCKED-PHYSICAL' "$CERT_RESULTS")"

# --- Verdict logic ----------------------------------------------------------
# All PASS -> 0.
: > "$CERT_RESULTS"; gate_pass 01 a; gate_pass 02 b
cert_finalize >/dev/null 2>&1
check "verdict all-pass is success" 0 "$?"

# Any BLOCKED-PHYSICAL, no FAIL -> 2 (incomplete, needs hardware).
: > "$CERT_RESULTS"; gate_pass 01 a; gate_blocked 02 b "hw"
cert_finalize >/dev/null 2>&1
check "verdict blocked is incomplete" 2 "$?"

# Any FAIL -> 1 (real defect), even alongside passes/blocked.
: > "$CERT_RESULTS"; gate_pass 01 a; gate_fail 02 b "defect"; gate_blocked 03 c "hw"
cert_finalize >/dev/null 2>&1
check "verdict fail is failure" 1 "$?"

# --- Cleanup is a safe no-op with no registry -------------------------------
rm -f "$CERT_REGISTRY"
( cert_cleanup ) >/dev/null 2>&1
check "cleanup with empty registry is a no-op" 0 "$?"

# --- JSON helpers used by the physical gates --------------------------------
check "json get reads a field" "abc" "$(cert_json_get '{"id":"abc","name":"x"}' id)"
check "json find id by name" "u1" "$(cert_json_find_id '{"items":[{"id":"u1","name":"cert-x"}]}' cert-x)"

# The physical harness must call the real CLI surface, not the pre-1.0 stubs.
# shellcheck disable=SC2016
if grep -E 'nctl workload exec|nctl snapshot create --id |nctl pool create --kind|nctl feature status --id' "${HERE}/ce-1.0-certify.sh" >/dev/null; then
  printf 'FAIL  harness still calls obsolete CLI verbs\n'
  fails=$((fails + 1))
else
  printf 'ok    harness uses current nodalctl verbs\n'
fi
if grep -q 'workload create --kind system-container' "${HERE}/ce-1.0-certify.sh" && \
   grep -q 'snapshot create --workload' "${HERE}/ce-1.0-certify.sh" && \
   grep -q 'cluster nodes' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    harness create/snapshot/cluster match shipped CLI\n'
else
  printf 'FAIL  harness missing required current CLI calls\n'
  fails=$((fails + 1))
fi

rm -rf "$CERT_OUT"
if [ "$fails" -eq 0 ]; then
  echo "SELFTEST_OK"
  exit 0
fi
echo "SELFTEST_FAILED: ${fails} check(s) failed"
exit 1
