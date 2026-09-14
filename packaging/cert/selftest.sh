#!/usr/bin/env bash
# Self-test for the CE 1.0 certification harness library.
#
# Runs in any environment (no No-dal appliance required). It proves the parts of
# the harness that must be trustworthy regardless of hardware:
#   - the production-safety guard refuses non-disposable and denylisted names,
#   - the ledger records results,
#   - the verdict logic fails loudly (non-zero) on FAIL or BLOCKED-* and
#     only returns success when every gate genuinely PASSed.
#   - a KVM/QEMU/container Node B cannot PASS the physical two-node gate,
#   - an unarmed / un-rebooted Node A cannot PASS the reboot gate,
#   - same-version 1.0.6 -> 1.0.6 cannot PASS the upgrade gate.
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
( assert_disposable "nodal-restore-deadbeef" ) >/dev/null 2>&1
check "guard accepts restore-as-new leftover name" 0 "$?"

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
check "files content extracts JSON body" "canary" "$(printf '%s' '{"content":"canary","path":"/root/x"}' | cert_decode_files_content)"
cidr="$(cert_isolated_cidr)"
case "$cidr" in
  10.77.[0-9]*.0/24) check "isolated-nat CIDR is run-scoped" 0 0 ;;
  *) check "isolated-nat CIDR is run-scoped" "10.77.x.0/24" "$cidr" ;;
esac
check "files content keeps raw body" "plain" "$(printf '%s' 'plain' | cert_decode_files_content)"

arp_fix="$(mktemp)"
cat > "$arp_fix" <<'ARP'
IP address       HW type     Flags       HW address            Mask     Device
192.168.2.183    0x1         0x2         0e:68:ba:17:4b:1b     *        ndl685ff937
10.1.1.9         0x1         0x0         0e:68:ba:17:4b:1b     *        ndl685ff937
ARP
check "arp table prefers complete neighbor" "192.168.2.183" "$(cert_ipv4_from_arp_table "0E-68-BA-17-4B-1B" "$arp_fix")"
rm -f "$arp_fix"
if grep -q 'secrets/cluster-ca' "${HERE}/../lib/ndl/postinst-control.sh"; then
  printf 'ok    postinst creates cluster-ca dir for unprivileged Ensure\n'
else
  printf 'FAIL  postinst-control.sh must create /var/lib/ndl/secrets/cluster-ca\n'
  fails=$((fails + 1))
fi

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
if awk '/^gate20\(\)/,/^gate21\(\)/ { if ($0 ~ /CERT_REBOOT_NODE_A|cert_reboot_armed/) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh" && \
   grep -q -- '--resume-reboot' "${HERE}/ce-1.0-certify.sh" && \
   grep -q 'cert_node_a_reboot_verdict' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 20 requires an armed Node A reboot and --resume-reboot\n'
else
  printf 'FAIL  gate 20 must require CERT_REBOOT_NODE_A and prove a Node A reboot\n'
  fails=$((fails + 1))
fi
if awk '/^gate20\(\)/,/^gate21\(\)/ { if ($0 ~ /node B rebooted|reboot node B|never reboot production node A/) bad=1 } END { exit(bad?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'FAIL  gate 20 still treats a node B/guest reboot as the physical PASS\n'
  fails=$((fails + 1))
else
  printf 'ok    gate 20 no longer PASSes a node B/guest reboot\n'
fi
if grep -q 'workload_id' "${HERE}/ce-1.0-certify.sh" && grep -q 'restored_workload_id' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    backup/restore gates filter disposable artifacts\n'
else
  printf 'FAIL  backup/restore must not pick production artifacts[0]\n'
  fails=$((fails + 1))
fi
if grep -q 'github.com/minio/minio/releases/download' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    disposable MinIO download has a GitHub fallback\n'
else
  printf 'FAIL  MinIO download must not depend only on the 410 dl.min.io current URL\n'
  fails=$((fails + 1))
fi
if grep -q 'mode render --exclusive false' "${HERE}/ce-1.0-certify.sh" && ! grep -q 'mode vfio' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 27 uses render exclusive=false (never VFIO)\n'
else
  printf 'FAIL  gate 27 must assign render exclusive=false and never VFIO\n'
  fails=$((fails + 1))
fi
if grep -q 'CERT_NODE_B_ID' "${HERE}/lib.sh" && grep -q 'feature enable oci' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    two-node id and Store OCI feature enable are wired\n'
else
  printf 'FAIL  harness must prefer CERT_NODE_B_ID and enable the OCI feature\n'
  fails=$((fails + 1))
fi
if grep -q 'capture_mode' "${HERE}/ce-1.0-certify.sh" && grep -q 'dest agent listen' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    restore prefers full artifacts and dest-listen is checked per worker\n'
else
  printf 'FAIL  harness must prefer full artifacts and dest-listen Ready for node B\n'
  fails=$((fails + 1))
fi
if awk '/^gate14\(\)/,/^gate15\(\)/ { if ($0 ~ /dest-listen/) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 14 re-registers dest-listen immediately before migrate\n'
else
  printf 'FAIL  gate 14 must dest-listen immediately before migrate\n'
  fails=$((fails + 1))
fi
if awk '/^gate15\(\)/,/^gate16\(\)/ { if ($0 ~ /restored_workload_id/) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh" && \
   awk '/^gate15\(\)/,/^gate16\(\)/ { if ($0 ~ /unavailable/) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 15 requires dest-owned restore, not an unavailable placeholder\n'
else
  printf 'FAIL  gate 15 must require dest-owned restore-to-dest, not just HTTP accept\n'
  fails=$((fails + 1))
fi
if awk '/^cert_cleanup\(\)/,/^cert_install_cleanup_trap/' "${HERE}/lib.sh" | grep -q dest-listen && \
   awk '/^gate15\(\)/,/^gate16\(\)/ { if ($0 ~ /track_resource workload/) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    dest restore is tracked and cleanup re-registers dest-listen\n'
else
  printf 'FAIL  dest restore leftovers must be tracked and dest-listen before cleanup\n'
  fails=$((fails + 1))
fi
if grep -q 'CE 1.0 gate was not recorded' "${HERE}/lib.sh"; then
  printf 'ok    finalize fails when a required gate is missing from the ledger\n'
else
  printf 'FAIL  finalize must not silently omit required gates\n'
  fails=$((fails + 1))
fi
: > "$CERT_RESULTS"
gate_pass 01 a
cert_require_all_gates >/dev/null 2>&1
cert_finalize >/dev/null 2>&1
check "verdict missing-gates is failure" 1 "$?"
if grep -q '^FAIL	12	' "$CERT_RESULTS"; then
  printf 'ok    missing gate 12 is recorded as FAIL\n'
else
  printf 'FAIL  missing gate 12 was not recorded\n'
  fails=$((fails + 1))
fi

# --- Honesty: virtual Node B cannot PASS the physical two-node gate ----------
check "kvm guest virt kind" "kvm" "$(cert_virt_kind_from_facts kvm 0 QEMU 'Standard PC' hypervisor)"
check "qemu guest virt kind" "qemu" "$(cert_virt_kind_from_facts qemu 0 QEMU 'QEMU Virtual Machine' '')"
check "lxc guest virt kind" "lxc" "$(cert_virt_kind_from_facts lxc 0 '' '' '')"
check "docker guest virt kind" "docker" "$(cert_virt_kind_from_facts docker 0 '' '' '')"
check "physical none+dmi empty" "none" "$(cert_virt_kind_from_facts none 1 '' '' '')"
check "qemu DMI overrides none" "qemu" "$(cert_virt_kind_from_facts none 1 QEMU 'Standard PC (i440FX + PIIX, 1996)' '')"
check "hypervisor flag is not physical" "hypervisor" "$(cert_virt_kind_from_facts none 1 '' '' 'fpu vme hypervisor')"
check "missing detect-virt is unknown" "unknown" "$(cert_virt_kind_from_facts '' 1 '' '' '')"
check "physical join kvm is blocked" "BLOCKED-PHYSICAL" "$(cert_physical_join_verdict kvm 'worker-1')"
check "physical join qemu is blocked" "BLOCKED-PHYSICAL" "$(cert_physical_join_verdict qemu 'worker-1')"
check "physical join lxc is blocked" "BLOCKED-PHYSICAL" "$(cert_physical_join_verdict lxc 'worker-1')"
check "physical join unknown is blocked" "BLOCKED-PHYSICAL" "$(cert_physical_join_verdict unknown 'worker-1')"
check "physical join none+id is pass" "PASS" "$(cert_physical_join_verdict none 'worker-1')"
check "physical join none without id is fail" "FAIL" "$(cert_physical_join_verdict none '')"
if cert_virt_is_physical kvm || cert_virt_is_physical qemu || cert_virt_is_physical lxc; then
  printf 'FAIL  guest virt kinds must not count as physical\n'
  fails=$((fails + 1))
else
  printf 'ok    guest virt kinds are not physical\n'
fi
CERT_HONESTY_TEST=1 CERT_NODE_B_VIRT_FACTS="kvm|0|QEMU|Standard PC|hypervisor"
check "honesty test hook reports kvm" "kvm" "$(cert_node_b_virt_kind)"
unset CERT_NODE_B_VIRT_FACTS
if awk '/^gate12\(\)/,/^gate13\(\)/ { if ($0 ~ /cert_node_b_virt_kind/ ) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh" && \
   awk '/^gate12\(\)/,/^gate13\(\)/ { if ($0 ~ /cert_physical_join_verdict/ ) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 12 uses virt detection and cannot PASS a guest\n'
else
  printf 'FAIL  gate 12 must call cert_node_b_virt_kind and cert_physical_join_verdict\n'
  fails=$((fails + 1))
fi

# --- Honesty: Node A reboot is not PASS until boot_id changes ---------------
check "reboot verdict unchanged boot is blocked" "BLOCKED-PHYSICAL" "$(cert_node_a_reboot_verdict abc abc ok ok ok ok)"
check "reboot verdict empty pre-state is blocked" "BLOCKED-PHYSICAL" "$(cert_node_a_reboot_verdict '' def ok ok ok ok)"
check "reboot verdict success" "PASS" "$(cert_node_a_reboot_verdict abc def ok ok ok ok)"
check "reboot verdict mgmt fail" "FAIL" "$(cert_node_a_reboot_verdict abc def bad ok ok ok)"
check "reboot verdict autostart fail" "FAIL" "$(cert_node_a_reboot_verdict abc def ok bad ok ok)"
check "reboot verdict prod fail" "FAIL" "$(cert_node_a_reboot_verdict abc def ok ok bad ok)"
check "reboot verdict duplicate names fail" "FAIL" "$(cert_node_a_reboot_verdict abc def ok ok ok bad)"
if ! cert_reboot_armed; then
  printf 'ok    reboot gate is unarmed by default\n'
else
  printf 'FAIL  CERT_REBOOT_NODE_A must not be armed in selftest\n'
  fails=$((fails + 1))
fi

# --- Honesty: same-version reinstall is not a CE 1.0 upgrade PASS -----------
check "upgrade 1.0.5 -> 1.0.6 is real" "real" "$(cert_upgrade_kind 1.0.5 1.0.6)"
check "upgrade 1.0.6 -> 1.0.6 is same" "same" "$(cert_upgrade_kind 1.0.6 1.0.6)"
check "upgrade 1.0.6 -> 1.0.5 is downgrade" "downgrade" "$(cert_upgrade_kind 1.0.6 1.0.5)"
check "upgrade missing versions" "missing" "$(cert_upgrade_kind '' 1.0.6)"
check "upgrade gate real is pass" "PASS" "$(cert_upgrade_gate_status real)"
check "upgrade gate same is release-artifact" "BLOCKED-PHYSICAL/RELEASE-ARTIFACT" "$(cert_upgrade_gate_status same)"
check "upgrade gate missing is release-artifact" "BLOCKED-PHYSICAL/RELEASE-ARTIFACT" "$(cert_upgrade_gate_status missing)"
check "upgrade gate downgrade is release-artifact" "BLOCKED-PHYSICAL/RELEASE-ARTIFACT" "$(cert_upgrade_gate_status downgrade)"
: > "$CERT_RESULTS"
gate_pass 01 a
gate_blocked_release 21 "Package upgrade preserving workloads/config/auth" "1.0.6 -> 1.0.6"
cert_finalize >/dev/null 2>&1
check "verdict RELEASE-ARTIFACT is incomplete" 2 "$?"
if grep -c '^BLOCKED-PHYSICAL/RELEASE-ARTIFACT' "$CERT_RESULTS" | grep -qx 1; then
  printf 'ok    ledger records BLOCKED-PHYSICAL/RELEASE-ARTIFACT\n'
else
  printf 'FAIL  gate_blocked_release did not record RELEASE-ARTIFACT\n'
  fails=$((fails + 1))
fi
if awk '/^gate21\(\)/,/^gate22\(\)/ { if ($0 ~ /cert_upgrade_kind/ ) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh" && \
   awk '/^gate21\(\)/,/^gate22\(\)/ { if ($0 ~ /gate_blocked_release/ ) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh" && \
   awk '/^gate21\(\)/,/^gate22\(\)/ { if ($0 ~ /cert_upgrade_gate_status/ ) found=1 } END { exit(found?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'ok    gate 21 requires a real version transition\n'
else
  printf 'FAIL  gate 21 must use cert_upgrade_kind and gate_blocked_release\n'
  fails=$((fails + 1))
fi
if awk '/^gate21\(\)/,/^gate22\(\)/ { if ($0 ~ /upgraded nodal \$\{before\} -> \$\{after\}; health ok/) bad=1 } END { exit(bad?0:1) }' "${HERE}/ce-1.0-certify.sh"; then
  printf 'FAIL  gate 21 still PASSes any same-version dpkg reinstall\n'
  fails=$((fails + 1))
else
  printf 'ok    gate 21 no longer PASSes same-version reinstall as upgrade\n'
fi
if grep -q '\$1 ~ /^BLOCKED/' "${HERE}/lib.sh"; then
  printf 'ok    finalize counts every BLOCKED* status as incomplete\n'
else
  printf 'FAIL  finalize must count BLOCKED-PHYSICAL/RELEASE-ARTIFACT\n'
  fails=$((fails + 1))
fi

rm -rf "$CERT_OUT"
if [ "$fails" -eq 0 ]; then
  echo "SELFTEST_OK"
  exit 0
fi
echo "SELFTEST_FAILED: ${fails} check(s) failed"
exit 1
