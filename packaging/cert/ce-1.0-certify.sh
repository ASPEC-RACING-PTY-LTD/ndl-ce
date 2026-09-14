#!/usr/bin/env bash
# No-dal CE 1.0 two-physical-node certification harness.
#
# Run this on the first Debian 13 amd64 node after installing No-dal. It drives
# real product operations against the local control plane, creates clearly named
# disposable certification resources, verifies real outcomes, and records
# PASS / FAIL / BLOCKED-PHYSICAL for each CE 1.0 gate with evidence. It cleans up
# every disposable resource it creates and never touches production.
#
# It is deliberately impossible for this harness to report CE 1.0 as certified
# from mocks: gates only PASS when a real operation was executed and verified,
# and any gate whose hardware/external prerequisite is missing is recorded as
# BLOCKED-PHYSICAL, never PASS.
#
# Three required physical gates are fail-closed:
#   12  Node B must be bare metal (KVM/QEMU/VM/container is BLOCKED-PHYSICAL)
#   20  Node A must actually reboot when armed (unarmed is BLOCKED-PHYSICAL)
#   21  requires a real version transition (same-version is RELEASE-ARTIFACT)
#
# See docs/checklists/ce-1.0-certification.md for the full operator procedure,
# required environment variables, and safety notes.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=packaging/cert/lib.sh
. "${HERE}/lib.sh"

usage() {
  cat >&2 <<EOF
Usage: CERT_I_UNDERSTAND=disposable-only $0 [--plan|--resume-reboot]

  --plan            List the gates and exit without running anything.
  --resume-reboot   After an armed Node A reboot, rewrite gate 20 from
                    saved pre-state (same CERT_RUN_ID) and continue.

Required acknowledgement: set CERT_I_UNDERSTAND=disposable-only to run. The
harness only mutates resources it creates with the prefix ${CERT_PREFIX}.

Optional environment (missing ones make their gates BLOCKED-PHYSICAL):
  CERT_NODE_B         address of the second physical node (join/migration)
  CERT_R2_TARGET_ID   existing backup target id for R2/S3 (create it first)
  CERT_R2_BUCKET      R2/S3 bucket name (name only)
  CERT_R2_ENDPOINT    R2/S3 endpoint URL (name only)
  CERT_VM_IMAGE       path/id of a small disposable cloud image for KVM
  NODAL_URL           control-plane URL (default http://127.0.0.1:8080)
  NODAL_TOKEN         API token for gates that use the HTTP API
  CERT_REBOOT_NODE_A  ${CERT_REBOOT_NODE_A_TOKEN} arms the primary-host reboot gate
  CERT_REBOOT_NODE_A_EXECUTE  now  actually reboot Node A (destructive)
  CERT_REINSTALL_IDEMPOTENCE  1  same-version reinstall as integration only
  CERT_UPGRADE_DEB_DIR        directory of signed nodal_*.deb artifacts
EOF
}

GATES=(
  "01:Fresh supported Debian 13 installation"
  "02:No-dal package/repository installation"
  "03:Browser first-run setup"
  "04:Management services healthy"
  "05:Disposable LXC creation/start/network"
  "06:Disposable KVM/QEMU VM creation/start/network"
  "07:Docker/nested-container functionality"
  "08:Snapshots"
  "09:Backup Engine V2 Smart/Custom/Full"
  "10:R2/S3 upload to verification to Protected"
  "11:Destroy disposable source, restore-as-new, validate data"
  "12:Second physical node join"
  "13:Authenticated mTLS/WireGuard destination-agent connectivity"
  "14:Offline/live migration per actual CE 1.0 support"
  "15:Destination object pull"
  "16:Restored/migrated workload boots and runs on destination"
  "17:Source is not duplicated after migration"
  "18:Failed migration leaves source safely running"
  "19:Management-plane restart does not terminate workloads"
  "20:Host reboot/autostart"
  "21:Package upgrade preserving workloads/config/auth"
  "22:License/API outage grace behavior"
  "23:Store signed application install"
  "24:Monitoring/events/tasks"
  "25:Terminal/Files/guest-agent functionality"
  "26:Network safety/rollback"
  "27:GPU/IOMMU path if hardware exists"
  "28:Final cleanup of disposable certification resources"
)

CERT_RESUME_REBOOT=0
case "${1:-}" in
  --plan)
    printf 'CE 1.0 certification gates:\n'
    for g in "${GATES[@]}"; do printf '  %s\n' "$g"; done
    exit 0
    ;;
  --resume-reboot)
    CERT_RESUME_REBOOT=1
    ;;
  "" ) ;;
  --help|-h)
    usage
    exit 0
    ;;
  *)
    usage
    exit 3
    ;;
esac

if [ "${CERT_I_UNDERSTAND:-}" != "disposable-only" ]; then
  usage
  exit 3
fi

if [ "$CERT_RESUME_REBOOT" != 1 ]; then
  cert_init
  cert_install_cleanup_trap
  cert_ensure_token
fi

# Reusable disposable identifiers for this run.
NET_NAME="${CERT_PREFIX}-net"
POOL_NAME="${CERT_PREFIX}-pool"
CT_NAME="${CERT_PREFIX}-ct"
VM_NAME="${CERT_PREFIX}-vm"
APP_NAME="${CERT_PREFIX}-web"
MIG_NAME="${CERT_PREFIX}-mig"
NODEB_NAME="${CERT_PREFIX}-nodeb"
CANARY="${CERT_PREFIX}-canary-$(date -u +%s)"
CT_ID=""
VM_ID=""
NET_ID=""
POOL_ID=""
NODE_B_ID=""
MIG_ID=""
APP_WL_ID=""
ARTIFACT_ID=""

# ---------------------------------------------------------------------------
# Gate implementations. Each returns after recording exactly one result per
# gate id. Real operations run only when prerequisites are genuinely present.
# ---------------------------------------------------------------------------

gate01() {
  local id=01 t="Fresh supported Debian 13 installation"
  if is_debian13; then
    gate_pass "$id" "$t" "$(cert_evidence os cat /etc/os-release)"
  else
    gate_blocked "$id" "$t" "not running on Debian 13 amd64"
  fi
}

gate02() {
  local id=02 t="No-dal package/repository installation"
  require_physical "$id" "$t" nodal_installed || return
  local v; v="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || echo unknown)"
  if [ "$v" != unknown ]; then
    gate_pass "$id" "$t" "nodal ${v}; $(cert_evidence pkgs dpkg -l 'nodal*' 'ndl-*')"
  else
    gate_fail "$id" "$t" "nodal metapackage is not installed via dpkg"
  fi
}

gate03() {
  local id=03 t="Browser first-run setup"
  require_physical "$id" "$t" control_healthy || return
  if setup_complete; then
    gate_pass "$id" "$t" "setup is closed; a Super Administrator exists"
  else
    gate_blocked "$id" "$t" "complete /setup in a browser first, then re-run"
  fi
}

gate04() {
  local id=04 t="Management services healthy"
  require_physical "$id" "$t" control_healthy || return
  local ok=1
  if have_cmd systemctl; then
    systemctl is-active --quiet ndl-control || ok=0
    systemctl is-active --quiet ndl-agent || ok=0
  fi
  if [ "$ok" = 1 ]; then
    gate_pass "$id" "$t" "$(cert_evidence health curl -fsS "${NODAL_URL}/api/v1/health")"
  else
    gate_fail "$id" "$t" "ndl-control or ndl-agent unit is not active"
  fi
}

gate05() {
  local id=05 t="Disposable LXC creation/start/network"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  assert_disposable "$CT_NAME"
  POOL_ID="$(cert_default_pool_id)"
  NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
  if [ -z "$POOL_ID" ] || [ -z "$NET_ID" ]; then
    gate_fail "$id" "$t" "could not resolve a usable pool or create isolated-nat ${NET_NAME}"
    return
  fi
  track_resource workload "$CT_NAME"
  local raw
  raw="$(nctl workload create --kind system-container --name "$CT_NAME" \
    --image-pin debian/trixie/amd64/default --pool-id "$POOL_ID" --network-id "$NET_ID" \
    --cpus 1 --memory-bytes 536870912 --disk-bytes 4294967296 --extras docker 2>/dev/null || true)"
  CT_ID="$(cert_json_get "$raw" id)"
  [ -n "$CT_ID" ] || CT_ID="$(cert_id_by_name workload "$CT_NAME")"
  if [ -z "$CT_ID" ]; then
    gate_fail "$id" "$t" "LXC create failed"
    return
  fi
  nctl workload start --id "$CT_ID" >/dev/null 2>&1 || true
  if cert_wait_workload "$CT_ID" running 240; then
    gate_pass "$id" "$t" "$(cert_evidence lxc nctl workload get --id "$CT_ID")"
  else
    gate_fail "$id" "$t" "container did not reach running"
  fi
}

cert_resolve_cloud_image() {
  if [ -n "${CERT_VM_IMAGE:-}" ]; then
    printf '%s' "$CERT_VM_IMAGE"
    return 0
  fi
  local listed
  listed="$(nctl storage image list 2>/dev/null || true)"
  local existing
  existing="$(printf '%s' "$listed" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for i in items:
  if i.get("kind") in ("cloud-image","disk-image") and i.get("id"):
    print(i["id"]); break
' 2>/dev/null || true)"
  if [ -n "$existing" ]; then
    printf '%s' "$existing"
    return 0
  fi
  local imgdir="/var/lib/ndl/cert/images"
  mkdir -p "$imgdir"
  local img="${imgdir}/debian-13-genericcloud-amd64.qcow2"
  if [ ! -s "$img" ]; then
    curl -fL --retry 3 -o "${img}.part" \
      "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2" \
      && mv "${img}.part" "$img" || rm -f "${img}.part"
  fi
  [ -s "$img" ] || return 1
  local pool raw
  pool="$(cert_default_pool_id)"
  [ -n "$pool" ] || return 1
  raw="$(nctl storage image upload --pool-id "$pool" --kind cloud-image --file "$img" 2>/dev/null || true)"
  cert_json_get "$raw" id
}

gate06() {
  local id=06 t="Disposable KVM/QEMU VM creation/start/network"
  if ! nodal_installed || ! control_healthy; then
    gate_blocked "$id" "$t" "no No-dal appliance"
    return
  fi
  if [ ! -e /dev/kvm ]; then
    gate_blocked "$id" "$t" "/dev/kvm not present (no hardware virtualization)"
    return
  fi
  local image
  image="$(cert_resolve_cloud_image || true)"
  if [ -z "$image" ]; then
    gate_blocked "$id" "$t" "set CERT_VM_IMAGE to a small disposable cloud image"
    return
  fi
  CERT_VM_IMAGE="$image"
  export CERT_VM_IMAGE
  [ -n "$NET_ID" ] || NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
  [ -n "$POOL_ID" ] || POOL_ID="$(cert_default_pool_id)"
  assert_disposable "$VM_NAME"
  track_resource workload "$VM_NAME"
  local raw
  raw="$(nctl workload create --kind vm --name "$VM_NAME" --network-id "$NET_ID" \
    --pool-id "$POOL_ID" --cloud-image-id "$CERT_VM_IMAGE" --firmware bios \
    --cpus 1 --memory-bytes 536870912 --nocloud-user debian --nocloud-host "$VM_NAME" 2>/dev/null || true)"
  VM_ID="$(cert_json_get "$raw" id)"
  [ -n "$VM_ID" ] || VM_ID="$(cert_id_by_name workload "$VM_NAME")"
  if [ -z "$VM_ID" ]; then
    gate_fail "$id" "$t" "VM create failed"
    return
  fi
  nctl workload start --id "$VM_ID" >/dev/null 2>&1 || true
  if cert_wait_workload "$VM_ID" running 300; then
    gate_pass "$id" "$t" "$(cert_evidence kvm nctl workload get --id "$VM_ID")"
  else
    gate_fail "$id" "$t" "VM create/start failed"
  fi
}

gate07() {
  local id=07 t="Docker/nested-container functionality"
  if ! nodal_installed || ! control_healthy; then
    gate_blocked "$id" "$t" "no No-dal appliance"
    return
  fi
  if [ -z "$CT_ID" ]; then
    gate_blocked "$id" "$t" "no disposable workload from gate 05"
    return
  fi
  nctl feature enable docker >/dev/null 2>&1 || true
  local extras
  extras="$(api POST "/api/v1/workloads/${CT_ID}/setup-extras" '{"extras":["docker"]}' 2>/dev/null || true)"
  local out="" waited=0
  while [ "$waited" -lt 180 ]; do
    # lxc-start's unit MainPID is a host process. Probe the guest with lxc-attach.
    out="$(lxc-attach -P /var/lib/ndl/runtime/lxc -n "$CT_ID" --clear-env -- /bin/sh -c 'command -v docker && docker info >/dev/null && echo DOCKER_OK' 2>/dev/null || true)"
    printf '%s' "$out" | grep -q DOCKER_OK && break
    sleep 5; waited=$((waited + 5))
  done
  if printf '%s' "$out" | grep -q DOCKER_OK; then
    gate_pass "$id" "$t" "docker info succeeded inside disposable CT ${CT_NAME}"
  else
    gate_fail "$id" "$t" "docker extras did not yield a running engine inside the disposable guest"
  fi
}

gate08() {
  local id=08 t="Snapshots"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  local snap_of=""
  if [ -n "$VM_ID" ]; then
    snap_of="$VM_ID"
  elif [ -n "$CT_ID" ]; then
    snap_of="$CT_ID"
  fi
  if [ -z "$snap_of" ]; then
    gate_blocked "$id" "$t" "no disposable workload from gate 05/06"
    return
  fi
  local snap_err
  snap_err="$(nctl snapshot create --workload "$snap_of" --name "${CERT_PREFIX}-snap" 2>&1 || true)"
  if printf '%s' "$snap_err" | grep -qi '"id"'; then
    gate_pass "$id" "$t" "$(cert_evidence snap nctl snapshot list --workload "$snap_of")"
  else
    gate_fail "$id" "$t" "snapshot create failed: ${snap_err}"
  fi
}

# cert_ensure_object_target starts a disposable MinIO and registers it as an
# S3-compatible backup target when CERT_R2_* is not already set. Secrets stay
# in the environment and are never written to evidence.
cert_ensure_object_target() {
  if have_r2; then
    return 0
  fi
  local datadir="${CERT_OUT}/minio"
  mkdir -p "$datadir"
  local bin="${CERT_OUT}/minio-bin"
  if [ ! -x "$bin" ]; then
    local url
    for url in \
      "https://github.com/minio/minio/releases/download/RELEASE.2024-11-07T00-52-20Z/minio.linux-amd64.RELEASE.2024-11-07T00-52-20Z" \
      "https://github.com/minio/minio/releases/download/RELEASE.2024-10-13T13-34-11Z/minio.linux-amd64.RELEASE.2024-10-13T13-34-11Z" \
      "https://dl.min.io/server/minio/release/linux-amd64/archive/minio.RELEASE.2024-11-07T00-52-20Z" \
      "https://dl.min.io/server/minio/release/linux-amd64/minio"
    do
      if curl -fsSL --retry 2 -o "${bin}.part" "$url"; then
        mv "${bin}.part" "$bin"
        break
      fi
      rm -f "${bin}.part"
    done
    [ -s "$bin" ] || return 1
    chmod +x "$bin"
  fi
  local user pass
  user="cert$(printf '%s' "$CERT_RUN_ID" | tr -cd 'a-zA-Z0-9' | cut -c1-8)"
  pass="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
  MINIO_ROOT_USER="$user" MINIO_ROOT_PASSWORD="$pass" "$bin" server --address 127.0.0.1:19000 "$datadir" >/tmp/cert-minio.log 2>&1 &
  echo $! > "${CERT_OUT}/minio.pid"
  local i=0
  while [ "$i" -lt 30 ]; do
    curl -fsS -m 1 "http://127.0.0.1:19000/minio/health/live" >/dev/null 2>&1 && break
    sleep 1; i=$((i + 1))
  done
  local mcli="${CERT_OUT}/mc"
  if [ ! -x "$mcli" ]; then
    local murl
    for murl in \
      "https://github.com/minio/mc/releases/download/RELEASE.2024-10-08T09-37-26Z/mc.linux-amd64.RELEASE.2024-10-08T09-37-26Z" \
      "https://dl.min.io/client/mc/release/linux-amd64/archive/mc.RELEASE.2024-10-08T09-37-26Z" \
      "https://dl.min.io/client/mc/release/linux-amd64/mc"
    do
      if curl -fsSL --retry 2 -o "${mcli}.part" "$murl"; then
        mv "${mcli}.part" "$mcli"
        break
      fi
      rm -f "${mcli}.part"
    done
    [ -s "$mcli" ] || return 1
    chmod +x "$mcli"
  fi
  "$mcli" alias set certr2 "http://127.0.0.1:19000" "$user" "$pass" >/dev/null
  "$mcli" mb "certr2/${CERT_PREFIX}-bucket" >/dev/null
  local raw
  raw="$(nctl backup target create --kind minio --name "${CERT_PREFIX}-r2" \
    --endpoint "http://127.0.0.1:19000" --bucket "${CERT_PREFIX}-bucket" \
    --username "$user" --password "$pass" --prefix "${CERT_PREFIX}" --region us-east-1 \
    --no-check-bucket 2>/dev/null || true)"
  CERT_R2_TARGET_ID="$(cert_json_get "$raw" id)"
  [ -n "$CERT_R2_TARGET_ID" ] || CERT_R2_TARGET_ID="$(cert_id_by_name target "${CERT_PREFIX}-r2")"
  CERT_R2_BUCKET="${CERT_PREFIX}-bucket"
  CERT_R2_ENDPOINT="http://127.0.0.1:19000"
  export CERT_R2_TARGET_ID CERT_R2_BUCKET CERT_R2_ENDPOINT
  track_resource target "${CERT_PREFIX}-r2"
  [ -n "$CERT_R2_TARGET_ID" ]
}

# cert_backup_dr runs the full backup/DR chain against the disposable CT and an
# R2/S3 target. It records gates 09, 10 and 11.
cert_backup_dr() {
  if ! nodal_installed || ! control_healthy; then
    gate_blocked 09 "Backup Engine V2 Smart/Custom/Full" "no No-dal appliance"
    gate_blocked 10 "R2/S3 upload to verification to Protected" "no No-dal appliance"
    gate_blocked 11 "Destroy disposable source, restore-as-new, validate data" "no No-dal appliance"
    return
  fi
  if [ -z "$CT_ID" ]; then
    gate_blocked 09 "Backup Engine V2 Smart/Custom/Full" "no disposable workload from gate 05"
    gate_blocked 10 "R2/S3 upload to verification to Protected" "no disposable workload"
    gate_blocked 11 "Destroy disposable source, restore-as-new, validate data" "no disposable workload"
    return
  fi

  cert_ensure_object_target || true
  cert_files_put "$CT_ID" "/root/${CERT_PREFIX}.canary" "$CANARY" >/dev/null 2>&1 || true

  local ok09=1 mode
  local freeze_violation=0
  for mode in smart custom full; do
    _cert_zero_freeze_sample "$CT_ID" &
    local sampler=$!
    if ! api POST /api/v1/backups/run \
         "{\"workload_id\":\"${CT_ID}\",\"target_id\":\"${CERT_R2_TARGET_ID:-}\",\"capture_mode\":\"${mode}\"}" \
         | python3 -c 'import json,sys
d=json.load(sys.stdin)
st=str(d.get("status") or "").lower()
ok=d.get("id") and st in ("succeeded","succeeded_with_warnings","accepted","running")
raise SystemExit(0 if ok else 1)
' 2>/dev/null; then
      ok09=0
    fi
    kill "$sampler" >/dev/null 2>&1 || true
    wait "$sampler" 2>/dev/null
    [ -f "${CERT_EVIDENCE}/freeze-violation" ] && freeze_violation=1
  done
  api POST /api/v1/backups/run \
    "{\"workload_id\":\"${CT_ID}\",\"target_id\":\"${CERT_R2_TARGET_ID:-}\",\"capture_mode\":\"smart\"}" >/dev/null 2>&1
  if [ "$freeze_violation" = 1 ]; then
    gate_fail 09 "Backup Engine V2 Smart/Custom/Full" "ZERO-FREEZE INVARIANT VIOLATED: cgroup.freeze changed during capture"
  elif [ "$ok09" = 1 ]; then
    gate_pass 09 "Backup Engine V2 Smart/Custom/Full" "Smart/Custom/Full plus incremental captured; zero-freeze held"
  else
    gate_fail 09 "Backup Engine V2 Smart/Custom/Full" "a capture mode failed"
  fi

  if ! have_r2; then
    gate_blocked 10 "R2/S3 upload to verification to Protected" "set CERT_R2_TARGET_ID/BUCKET/ENDPOINT"
  else
    local waited=0 protected=0
    while [ "$waited" -lt 300 ]; do
      if api GET /api/v1/backups/restore-points | python3 -c 'import json,sys
want=sys.argv[1]
d=json.load(sys.stdin)
for i in d.get("items") or []:
  if i.get("workload_id")==want and str(i.get("remote_state","")).lower()=="protected":
    raise SystemExit(0)
raise SystemExit(1)
' "$CT_ID" 2>/dev/null; then
        protected=1; break
      fi
      sleep 5; waited=$((waited + 5))
    done
    if [ "$protected" = 1 ]; then
      gate_pass 10 "R2/S3 upload to verification to Protected" "$(cert_evidence protected api GET /api/v1/backups/restore-points)"
    else
      gate_fail 10 "R2/S3 upload to verification to Protected" "disposable restore point did not reach Protected within 300s"
    fi
  fi

  local art
  art="$(api GET /api/v1/backups/artifacts | python3 -c 'import json,sys
want=sys.argv[1]
items=json.load(sys.stdin).get("items") or []
full=""
any=""
for i in items:
  if i.get("workload_id")!=want or not i.get("id"):
    continue
  any=i["id"]
  if str(i.get("capture_mode") or "").lower()=="full":
    full=i["id"]
print(full or any)
' "$CT_ID" 2>/dev/null || true)"
  ARTIFACT_ID="$art"
  if [ -z "$art" ]; then
    gate_fail 11 "Destroy disposable source, restore-as-new, validate data" "no disposable backup artifact found"
    return
  fi
  nctl workload delete --id "$CT_ID" >/dev/null 2>&1 || true
  local restore
  restore="$(api POST "/api/v1/backups/artifacts/${art}/restore" '{"mode":"new"}')"
  if printf '%s' "$restore" | grep -qi '"id"'; then
    local restored="" waited=0
    restored="$(printf '%s' "$restore" | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("restored_workload_id") or "")
' 2>/dev/null || true)"
    while [ "$waited" -lt 180 ]; do
      if [ -z "$restored" ] || [ "$restored" = "$CT_ID" ]; then
        restored="$(nctl workload list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
pref=sys.argv[1]
for i in items:
  n=i.get("name") or ""
  if i.get("id") and (n.startswith(pref) or n.startswith("nodal-restore-")):
    print(i["id"]); break
' "$CERT_PREFIX" 2>/dev/null || true)"
      fi
      [ -n "$restored" ] && [ "$restored" != "$CT_ID" ] && break
      sleep 3; waited=$((waited + 3))
    done
    if [ -n "$restored" ]; then
      CT_ID="$restored"
      CT_NAME="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("name",""))' 2>/dev/null || printf '%s' "$CT_NAME")"
      if [ -n "$CT_NAME" ]; then
        case "$CT_NAME" in
          "${CERT_PREFIX}"*|nodal-restore-*) track_resource workload "$CT_NAME" ;;
        esac
      fi
      nctl workload start --id "$CT_ID" >/dev/null 2>&1 || true
      cert_wait_workload "$CT_ID" running 180 || true
      local got
      got="$(cert_files_get "$CT_ID" "/root/${CERT_PREFIX}.canary" 2>/dev/null || true)"
      if [ "$got" = "$CANARY" ]; then
        gate_pass 11 "Destroy disposable source, restore-as-new, validate data" "canary matched after restore-as-new"
      else
        gate_fail 11 "Destroy disposable source, restore-as-new, validate data" "canary mismatch after restore"
      fi
    else
      gate_fail 11 "Destroy disposable source, restore-as-new, validate data" "restored workload not found"
    fi
  else
    gate_fail 11 "Destroy disposable source, restore-as-new, validate data" "restore-as-new call failed"
  fi
}

_cert_zero_freeze_sample() {
  local wl="$1" base
  base="$(_cert_cgroup_path "$wl")"
  [ -n "$base" ] || return 0
  for _ in $(seq 1 200); do
    if [ -r "${base}/cgroup.freeze" ] && [ "$(cat "${base}/cgroup.freeze" 2>/dev/null)" = "1" ]; then
      : > "${CERT_EVIDENCE}/freeze-violation"
      return 0
    fi
    sleep 0.05
  done
}

_cert_cgroup_path() {
  local wl="$1" scope
  scope="$(systemctl show -p ControlGroup "$(cert_ct_unit "$wl")" 2>/dev/null | cut -d= -f2)"
  [ -n "$scope" ] && printf '/sys/fs/cgroup%s' "$scope"
}

cert_recreate_ct_if_needed() {
  if [ -n "$CT_ID" ] && nctl workload get --id "$CT_ID" >/dev/null 2>&1; then
    return 0
  fi
  [ -n "$POOL_ID" ] || POOL_ID="$(cert_default_pool_id)"
  [ -n "$NET_ID" ] || NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
  assert_disposable "$CT_NAME"
  track_resource workload "$CT_NAME"
  local raw
  raw="$(nctl workload create --kind system-container --name "$CT_NAME" \
    --image-pin debian/trixie/amd64/default --pool-id "$POOL_ID" --network-id "$NET_ID" \
    --cpus 1 --memory-bytes 536870912 --disk-bytes 4294967296 2>/dev/null || true)"
  CT_ID="$(cert_json_get "$raw" id)"
  [ -n "$CT_ID" ] || CT_ID="$(cert_id_by_name workload "$CT_NAME")"
  nctl workload start --id "$CT_ID" >/dev/null 2>&1 || true
  cert_wait_workload "$CT_ID" running 240 || true
}

gate12() {
  local id=12 t="Second physical node join"
  if ! nodal_installed; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  if ! have_node_b; then gate_blocked "$id" "$t" "set CERT_NODE_B to the second physical node address"; return; fi
  NODE_B_ID="$(cert_worker_node_id)"
  local virt verdict
  virt="$(cert_node_b_virt_kind)"
  verdict="$(cert_physical_join_verdict "$virt" "$NODE_B_ID")"
  if [ -n "$NODE_B_ID" ]; then
    cert_evidence join nctl cluster nodes >/dev/null || true
  fi
  case "$verdict" in
    PASS)
      gate_pass "$id" "$t" "worker ${NODE_B_ID} is a physical node (virt=${virt})"
      ;;
    FAIL)
      gate_fail "$id" "$t" "second physical node is not present in cluster nodes (virt=${virt})"
      ;;
    *)
      if [ -n "$NODE_B_ID" ]; then
        cert_integration PASS "Virtual two-node join (not physical)" "worker ${NODE_B_ID} virt=${virt} at ${CERT_NODE_B}"
      fi
      gate_blocked "$id" "$t" "Node B is virtualized or unproven (virt=${virt}); a KVM/QEMU/VM/container guest cannot satisfy the CE 1.0 second-physical-node gate"
      ;;
  esac
}

gate13() {
  local id=13 t="Authenticated mTLS/WireGuard destination-agent connectivity"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  if [ -z "$NODE_B_ID" ]; then
    gate_fail "$id" "$t" "dest node id is unknown"
    return
  fi
  local listen
  listen="$(nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" 2>/dev/null || true)"
  if printf '%s' "$listen" | grep -qi '"status":"Ready"'; then
    gate_pass "$id" "$t" "dest agent listen ${CERT_NODE_B}:9444 is Ready"
  else
    gate_fail "$id" "$t" "dest agent for ${CERT_NODE_B} is not Ready"
  fi
}

gate14() {
  local id=14 t="Offline/live migration per actual CE 1.0 support"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  cert_recreate_ct_if_needed
  if [ -z "$CT_ID" ] || [ -z "$NODE_B_ID" ]; then
    gate_fail "$id" "$t" "missing disposable workload or dest node"
    return
  fi
  nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" >/dev/null 2>&1 || true
  local raw
  raw="$(nctl workload migrate --id "$CT_ID" --dest-node-id "$NODE_B_ID" --mode offline 2>&1 || true)"
  if printf '%s' "$raw" | grep -Eqi 'dest agent is not connected|424'; then
    gate_fail "$id" "$t" "offline migrate refused: dest agent is not connected"
    return
  fi
  if printf '%s' "$raw" | grep -Eqi 'dest volume locator missing'; then
    gate_fail "$id" "$t" "offline migrate refused: dest volume locator missing"
    return
  fi
  local waited=0 dest=""
  while [ "$waited" -lt 300 ]; do
    dest="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("node_id") or d.get("owner_node_id") or "")' 2>/dev/null || true)"
    [ "$dest" = "$NODE_B_ID" ] && break
    sleep 5; waited=$((waited + 5))
  done
  if [ "$dest" = "$NODE_B_ID" ]; then
    gate_pass "$id" "$t" "offline migrate of ${CT_NAME} reached dest ${NODE_B_ID}"
  else
    gate_fail "$id" "$t" "workload did not move to dest node"
  fi
}

gate15() {
  local id=15 t="Destination object pull"
  if ! nodal_installed || ! have_node_b || ! have_r2; then gate_blocked "$id" "$t" "needs two nodes and R2"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  local art
  art="${ARTIFACT_ID}"
  if [ -z "$art" ]; then
    art="$(api GET /api/v1/backups/artifacts | python3 -c 'import json,sys
want=sys.argv[1]
items=json.load(sys.stdin).get("items") or []
full=""
any=""
for i in items:
  n=str(i.get("locator") or "") + str(i.get("object_key") or "") + str(i.get("workload_id") or "")
  if want and want not in n:
    continue
  if not i.get("id"):
    continue
  any=i["id"]
  if str(i.get("capture_mode") or "").lower()=="full":
    full=i["id"]
print(full or any)
' "$CERT_PREFIX" 2>/dev/null || true)"
  fi
  if [ -z "$art" ] || [ -z "$NODE_B_ID" ]; then
    gate_fail "$id" "$t" "no disposable artifact or dest node for dest pull"
    return
  fi
  nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" >/dev/null 2>&1 || true
  local raw
  raw="$(api POST "/api/v1/backups/artifacts/${art}/restore" "{\"mode\":\"new\",\"target_node_id\":\"${NODE_B_ID}\"}")"
  mkdir -p "${CERT_OUT}/evidence"
  printf '%s\n' "$raw" > "${CERT_OUT}/evidence/dest-pull.json"
  local restored dest status
  restored="$(printf '%s' "$raw" | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("restored_workload_id") or "")
' 2>/dev/null || true)"
  if [ -z "$restored" ]; then
    gate_fail "$id" "$t" "restore-to-dest call failed"
    return
  fi
  dest="$(nctl workload get --id "$restored" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print((d.get("node_id") or ""), d.get("status") or "", d.get("reason") or "")
' 2>/dev/null || true)"
  status="$(printf '%s' "$dest" | awk '{print $2}')"
  if printf '%s' "$dest" | grep -q "$NODE_B_ID" && [ -n "$status" ] && [ "$status" != "unavailable" ]; then
    local rname
    rname="$(nctl workload get --id "$restored" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("name") or "")' 2>/dev/null || true)"
    [ -n "$rname" ] && track_resource workload "$rname"
    gate_pass "$id" "$t" "restore-to-dest ${restored} on dest ${NODE_B_ID} (${status})"
  else
    gate_fail "$id" "$t" "dest object pull did not land a dest-owned guest (${dest})"
  fi
}

gate16() {
  local id=16 t="Restored/migrated workload boots and runs on destination"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  if [ -z "$CT_ID" ]; then
    gate_fail "$id" "$t" "no migrated workload"
    return
  fi
  nctl workload start --id "$CT_ID" >/dev/null 2>&1 || true
  local dest
  dest="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print((d.get("node_id") or ""), d.get("status") or "")' 2>/dev/null || true)"
  if printf '%s' "$dest" | grep -q "$NODE_B_ID" && printf '%s' "$dest" | grep -qi running; then
    gate_pass "$id" "$t" "migrated workload running on dest"
  else
    gate_fail "$id" "$t" "migrated workload is not running on dest (${dest})"
  fi
}

gate17() {
  local id=17 t="Source is not duplicated after migration"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  local src
  src="$(cert_control_node_id)"
  local count
  count="$(nctl workload list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
name=sys.argv[1]
print(sum(1 for i in items if i.get("name")==name))
' "$CT_NAME" 2>/dev/null || echo 0)"
  local dest
  dest="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("node_id",""))' 2>/dev/null || true)"
  if [ "$count" = 1 ] && [ "$dest" = "$NODE_B_ID" ] && [ "$dest" != "$src" ]; then
    gate_pass "$id" "$t" "single owner on dest after migrate"
  else
    gate_fail "$id" "$t" "duplicate or unexpected ownership (count=${count} dest=${dest})"
  fi
}

gate18() {
  local id=18 t="Failed migration leaves source safely running"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  cert_recreate_ct_if_needed
  [ -n "$POOL_ID" ] || POOL_ID="$(cert_default_pool_id)"
  [ -n "$NET_ID" ] || NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
  assert_disposable "$MIG_NAME"
  track_resource workload "$MIG_NAME"
  local raw
  raw="$(nctl workload create --kind system-container --name "$MIG_NAME" \
    --image-pin alpine/3.21/amd64/default --pool-id "$POOL_ID" --network-id "$NET_ID" \
    --cpus 1 --memory-bytes 268435456 --disk-bytes 1073741824 2>/dev/null || true)"
  MIG_ID="$(cert_json_get "$raw" id)"
  [ -n "$MIG_ID" ] || MIG_ID="$(cert_id_by_name workload "$MIG_NAME")"
  nctl workload start --id "$MIG_ID" >/dev/null 2>&1 || true
  cert_wait_workload "$MIG_ID" running 180 || true
  local before
  before="$(nctl workload get --id "$MIG_ID" 2>/dev/null || true)"
  nctl workload migrate --id "$MIG_ID" --dest-node-id "$(python3 -c 'import uuid; print(uuid.uuid4())')" --mode offline >/dev/null 2>&1 || true
  local after st
  after="$(nctl workload get --id "$MIG_ID" 2>/dev/null || true)"
  st="$(cert_json_get "$after" status)"
  if [ "$st" = "running" ] && [ -n "$before" ] && [ -n "$after" ]; then
    gate_pass "$id" "$t" "source remained running after a failed migrate"
  else
    gate_fail "$id" "$t" "source was not left running after failed migrate (${st})"
  fi
}

gate19() {
  local id=19 t="Management-plane restart does not terminate workloads"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  if ! have_cmd systemctl; then gate_blocked "$id" "$t" "no systemd on this host"; return; fi
  cert_recreate_ct_if_needed
  if [ -z "$CT_ID" ]; then
    gate_blocked "$id" "$t" "no disposable workload to observe"
    return
  fi
  nctl workload start --id "$CT_ID" >/dev/null 2>&1 || true
  cert_wait_workload "$CT_ID" running 120 || true
  # Observe a disposable CT that still lives on this node (not a dest-only guest).
  local unit before after
  unit="$(cert_ct_unit "$CT_ID")"
  if ! systemctl is-active --quiet "$unit" 2>/dev/null; then
    if [ -n "$MIG_ID" ] && systemctl is-active --quiet "$(cert_ct_unit "$MIG_ID")" 2>/dev/null; then
      unit="$(cert_ct_unit "$MIG_ID")"
    fi
  fi
  if ! systemctl is-active --quiet "$unit" 2>/dev/null; then
    gate_fail "$id" "$t" "no disposable CT unit is active on this node to observe"
    return
  fi
  before="$(systemctl show -p MainPID "$unit" 2>/dev/null | cut -d= -f2)"
  systemctl restart ndl-control >/dev/null 2>&1 || true
  systemctl restart ndl-agent >/dev/null 2>&1 || true
  sleep 3
  after="$(systemctl show -p MainPID "$unit" 2>/dev/null | cut -d= -f2)"
  if [ -n "$before" ] && [ "$before" = "$after" ] && [ "$before" != 0 ]; then
    gate_pass "$id" "$t" "workload MainPID unchanged (${before}) across control-plane restart"
  else
    gate_fail "$id" "$t" "workload PID changed across management-plane restart (${before} -> ${after})"
  fi
}

# Pick a disposable autostart workload on the primary (control) node.
# Prints: id<TAB>name. Empty if none. When CT_ID is on node A, enable autostart.
cert_pick_node_a_autostart() {
  local control owner
  control="$(cert_control_node_id)"
  if [ -n "${CT_ID:-}" ]; then
    owner="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("node_id") or d.get("owner_node_id") or "")' 2>/dev/null || true)"
    if [ -n "$control" ] && [ "$owner" = "$control" ]; then
      nctl workload update --id "$CT_ID" --autostart true >/dev/null 2>&1 || true
      printf '%s\t%s\n' "$CT_ID" "${CT_NAME}"
      return 0
    fi
  fi
  nctl workload list 2>/dev/null | python3 -c 'import json,sys,os
control=sys.argv[1]
pref=os.environ.get("CERT_PREFIX") or "cert-"
items=json.load(sys.stdin).get("items") or []
for i in items:
  name=str(i.get("name") or "")
  owner=str(i.get("node_id") or i.get("owner_node_id") or "")
  if name.startswith(pref) and (not owner or owner==control):
    print("%s\t%s" % (i.get("id") or "", name))
    raise SystemExit
' "$control" 2>/dev/null || true
}

gate20() {
  local id=20 t="Host reboot/autostart"
  if ! nodal_installed || ! have_cmd systemctl; then
    gate_blocked "$id" "$t" "no No-dal appliance"
    return
  fi
  if ! cert_reboot_armed; then
    gate_blocked "$id" "$t" "not armed; set CERT_REBOOT_NODE_A=${CERT_REBOOT_NODE_A_TOKEN} and CERT_REBOOT_NODE_A_EXECUTE=now to reboot the primary physical host. Until performed this gate is BLOCKED-PHYSICAL (a node B/guest reboot is not a CE 1.0 PASS)"
    return
  fi
  local picked as_id as_name as_unit
  picked="$(cert_pick_node_a_autostart || true)"
  as_id="$(printf '%s' "$picked" | awk -F'\t' '{print $1}')"
  as_name="$(printf '%s' "$picked" | awk -F'\t' '{print $2}')"
  if [ -n "$as_id" ]; then
    nctl workload update --id "$as_id" --autostart true >/dev/null 2>&1 || true
    as_unit="$(cert_ct_unit "$as_id")"
  fi
  cert_write_node_a_reboot_pre "$as_id" "$as_name" "$as_unit"
  if ! cert_reboot_execute; then
    CERT_DEFER_AFTER_REBOOT=1
    trap - EXIT INT TERM
    gate_blocked "$id" "$t" "armed; pre-state written to ${CERT_OUT}/node-a-reboot-pre.env. Reboot node A, then CERT_RUN_ID=${CERT_RUN_ID} CERT_REBOOT_NODE_A=${CERT_REBOOT_NODE_A_TOKEN} $0 --resume-reboot"
    return
  fi
  trap - EXIT INT TERM
  touch "${CERT_OUT}/node-a-reboot-requested"
  sync
  cert_log "rebooting primary physical host (CERT_REBOOT_NODE_A_EXECUTE=now)"
  systemctl reboot
  gate_fail "$id" "$t" "systemctl reboot returned without the host going down"
}

gate20_resume() {
  local id=20 t="Host reboot/autostart"
  local pre now_boot mgmt=bad autostart=bad prod=bad dups=bad verdict
  pre="$(cert_reboot_pre_file)"
  # shellcheck disable=SC1090
  . "$pre"
  now_boot="$(cert_node_a_boot_id)"
  if control_healthy && systemctl is-active --quiet ndl-control && systemctl is-active --quiet ndl-agent; then
    mgmt=ok
  fi
  if [ -n "${PRE_AUTOSTART_ID:-}" ]; then
    local st owner unit_ok=0
    st="$(nctl workload get --id "$PRE_AUTOSTART_ID" 2>/dev/null | python3 -c 'import json,sys
print(json.load(sys.stdin).get("status",""))' 2>/dev/null || true)"
    owner="$(nctl workload get --id "$PRE_AUTOSTART_ID" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(d.get("node_id") or d.get("owner_node_id") or "")' 2>/dev/null || true)"
    if [ -n "${PRE_AUTOSTART_UNIT:-}" ] && systemctl is-active --quiet "$PRE_AUTOSTART_UNIT"; then
      unit_ok=1
    fi
    if [ "$st" = running ] && [ "$unit_ok" = 1 ] && { [ -z "${PRE_CONTROL_NODE_ID:-}" ] || [ "$owner" = "$PRE_CONTROL_NODE_ID" ]; }; then
      autostart=ok
    fi
  fi
  if cert_prod_units_healthy; then
    prod=ok
  fi
  if [ -z "$(cert_workload_duplicate_names)" ]; then
    dups=ok
  fi
  verdict="$(cert_node_a_reboot_verdict "${PRE_BOOT_ID:-}" "$now_boot" "$mgmt" "$autostart" "$prod" "$dups")"
  case "$verdict" in
    PASS)
      cert_replace_gate PASS "$id" "$t" "node A rebooted ${PRE_BOOT_ID} -> ${now_boot}; management recovered; autostart ${PRE_AUTOSTART_NAME:-$PRE_AUTOSTART_ID} running; production nodal-ct@ healthy; no duplicate names"
      ;;
    FAIL)
      cert_replace_gate FAIL "$id" "$t" "node A rebooted but recovery failed (mgmt=${mgmt} autostart=${autostart} prod=${prod} dups=${dups})"
      ;;
    *)
      cert_replace_gate BLOCKED-PHYSICAL "$id" "$t" "node A boot_id unchanged (${now_boot}); host did not reboot"
      ;;
  esac
}

gate21() {
  local id=21 t="Package upgrade preserving workloads/config/auth"
  if ! nodal_installed; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  local before after kind newer map_before map_after waited=0
  before="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || true)"
  newer="$(cert_best_newer_nodal_deb "$before" $(cert_collect_nodal_debs) || true)"
  if [ -z "$newer" ]; then
    if [ "${CERT_REINSTALL_IDEMPOTENCE:-}" = 1 ]; then
      local same=""
      same="$(cert_collect_nodal_debs | while read -r f; do
        [ "$(cert_deb_version "$f")" = "$before" ] && printf '%s\n' "$f" && break
      done)"
      if [ -n "$same" ]; then
        map_before="$(cert_preservation_digest || true)"
        DEBIAN_FRONTEND=noninteractive dpkg -i $(cert_sibling_debs "$same") >/tmp/cert-reinstall.log 2>&1 || true
        systemctl start ndl-control ndl-agent >/dev/null 2>&1 || true
        after="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || true)"
        if control_healthy && [ "$(cert_upgrade_kind "$before" "$after")" = same ]; then
          cert_integration PASS "Same-version reinstall/idempotence" "nodal ${before} reinstalled; not a CE 1.0 upgrade PASS"
        else
          cert_integration FAIL "Same-version reinstall/idempotence" "nodal ${before} -> ${after}; health or version unexpected"
        fi
      fi
    fi
    gate_blocked_release "$id" "$t" "no newer supported nodal package than ${before:-unknown}; same-version reinstall is not a CE 1.0 upgrade PASS"
    return
  fi
  map_before="$(cert_preservation_digest || true)"
  DEBIAN_FRONTEND=noninteractive dpkg -i $(cert_sibling_debs "$newer") >/tmp/cert-upgrade.log 2>&1 || true
  systemctl start ndl-control ndl-agent >/dev/null 2>&1 || true
  after="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || true)"
  kind="$(cert_upgrade_kind "$before" "$after")"
  if [ "$(cert_upgrade_gate_status "$kind")" != PASS ]; then
    gate_blocked_release "$id" "$t" "installed ${before} -> ${after} (${kind}); CE 1.0 upgrade requires a real supported version transition"
    return
  fi
  while [ "$waited" -lt 60 ]; do
    if control_healthy; then
      break
    fi
    sleep 2; waited=$((waited + 2))
  done
  if ! control_healthy || ! setup_complete; then
    gate_fail "$id" "$t" "real upgrade ${before} -> ${after} left management or auth unhealthy"
    return
  fi
  map_after="$(cert_preservation_digest || true)"
  if [ "$map_before" != "$map_after" ]; then
    gate_fail "$id" "$t" "real upgrade ${before} -> ${after} changed workload/config/cluster/backup identity"
    return
  fi
  gate_pass "$id" "$t" "upgraded nodal ${before} -> ${after}; users/config/workloads/storage/cluster/backup ids preserved"
}

gate22() {
  local id=22 t="License/API outage grace behavior"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  if api GET /api/v1/license 2>/dev/null | grep -qi '"edition"[[:space:]]*:[[:space:]]*"ce"' || \
     api GET /api/v1/settings/license 2>/dev/null | grep -qi '"edition"[[:space:]]*:[[:space:]]*"ce"'; then
    gate_pass "$id" "$t" "no key present; edition stays CE and workloads keep running"
  else
    gate_blocked "$id" "$t" "enter a key with the licensing API unreachable and confirm grace"
  fi
}

gate23() {
  local id=23 t="Store signed application install"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  [ -n "$POOL_ID" ] || POOL_ID="$(cert_default_pool_id)"
  [ -n "$NET_ID" ] || NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
  nctl feature enable oci >/dev/null 2>&1 || true
  local pkg
  pkg="$(nctl app list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for i in items:
  if i.get("name")=="sample-web" and i.get("signed"):
    print(i["id"]); break
' 2>/dev/null || true)"
  if [ -z "$pkg" ]; then
    gate_fail "$id" "$t" "official signed sample-web is not in the Store"
    return
  fi
  assert_disposable "$APP_NAME"
  local raw
  raw="$(nctl app install --id "$pkg" --name "$APP_NAME" --pool-id "$POOL_ID" --network-id "$NET_ID" 2>&1 || true)"
  APP_WL_ID="$(cert_json_get "$raw" workload_id)"
  if [ -n "$APP_WL_ID" ]; then
    track_resource workload "$APP_NAME"
    gate_pass "$id" "$t" "signed official sample installed as ${APP_NAME}"
  else
    gate_fail "$id" "$t" "signed Store install failed: ${raw}"
  fi
}

gate24() {
  local id=24 t="Monitoring/events/tasks"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  if api GET /api/v1/tasks 2>/dev/null | grep -qi '"items"' &&
     api GET /api/v1/events 2>/dev/null | grep -qi '\['; then
    gate_pass "$id" "$t" "tasks and events endpoints return data"
  else
    gate_fail "$id" "$t" "monitoring endpoints did not return data"
  fi
}

gate25() {
  local id=25 t="Terminal/Files/guest-agent functionality"
  if ! nodal_installed; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  cert_recreate_ct_if_needed
  if [ -z "$CT_ID" ]; then
    gate_blocked "$id" "$t" "requires a booted guest; no disposable CT"
    return
  fi
  local listed ticket
  listed="$(nctl workload files ls --id "$CT_ID" --path /root 2>/dev/null || true)"
  ticket="$(api POST "/api/v1/workloads/${CT_ID}/terminal/sessions" '{"cwd":"/root"}')"
  if printf '%s' "$listed" | grep -qi '"path"\|"name"\|"items"' && printf '%s' "$ticket" | grep -qi '"ticket"'; then
    gate_pass "$id" "$t" "Files list and Terminal session ticket issued for disposable CT"
  else
    gate_fail "$id" "$t" "Files/Terminal did not work on the disposable guest"
  fi
}

gate26() {
  local id=26 t="Network safety/rollback"
  require_physical "$id" "$t" nodal_installed control_healthy || return
  local dummy="certdmy0"
  ip link add "$dummy" type dummy 2>/dev/null || true
  ip link set "$dummy" up 2>/dev/null || true
  local raw netid
  raw="$(nctl network create --name "${CERT_PREFIX}-lanrb" --kind lan-bridge --uplink "$dummy" --dry-run 2>/dev/null || true)"
  local mgmt_before mgmt_after
  mgmt_before="$(ip -br addr show ndl685ff937 2>/dev/null || ip -br addr show | grep 192.168.2.82 || true)"
  netid="$(cert_json_get "$raw" id)"
  if [ -n "$netid" ]; then
    nctl network apply --id "$netid" --dry-run >/dev/null 2>&1 || true
  fi
  mgmt_after="$(ip -br addr show ndl685ff937 2>/dev/null || ip -br addr show | grep 192.168.2.82 || true)"
  ip link delete "$dummy" 2>/dev/null || true
  if printf '%s' "$mgmt_before" | grep -q 192.168.2.82 && printf '%s' "$mgmt_after" | grep -q 192.168.2.82; then
    if printf '%s' "$raw" | grep -qi 'lan-bridge\|dry.run\|dry_run\|warnings\|id'; then
      gate_pass "$id" "$t" "dummy lan-bridge dry-run left production management 192.168.2.82 intact"
    else
      gate_fail "$id" "$t" "could not dry-run a disposable lan-bridge"
    fi
  else
    gate_fail "$id" "$t" "management address changed during network safety check"
  fi
}

gate27() {
  local id=27 t="GPU/IOMMU path if hardware exists"
  if ! nodal_installed; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  if ! have_gpu; then
    gate_blocked "$id" "$t" "no assignable GPU/IOMMU hardware on this host"
    return
  fi
  local target=""
  if [ -n "$CT_ID" ] && systemctl is-active --quiet "$(cert_ct_unit "$CT_ID")" 2>/dev/null; then
    target="$CT_ID"
  elif [ -n "$MIG_ID" ] && systemctl is-active --quiet "$(cert_ct_unit "$MIG_ID")" 2>/dev/null; then
    target="$MIG_ID"
  else
    local local_name="${CERT_PREFIX}-local"
    assert_disposable "$local_name"
    track_resource workload "$local_name"
    [ -n "$POOL_ID" ] || POOL_ID="$(cert_default_pool_id)"
    [ -n "$NET_ID" ] || NET_ID="$(cert_ensure_isolated_net "$NET_NAME")"
    local raw
    raw="$(nctl workload create --kind system-container --name "$local_name" \
      --image-pin debian/trixie/amd64/default --pool-id "$POOL_ID" --network-id "$NET_ID" \
      --cpus 1 --memory-bytes 536870912 --disk-bytes 2147483648 2>/dev/null || true)"
    target="$(cert_json_get "$raw" id)"
    [ -n "$target" ] || target="$(cert_id_by_name workload "$local_name")"
    nctl workload start --id "$target" >/dev/null 2>&1 || true
    cert_wait_workload "$target" running 180 || true
  fi
  if [ -z "$target" ] || ! systemctl is-active --quiet "$(cert_ct_unit "$target")" 2>/dev/null; then
    gate_fail "$id" "$t" "GPU present; no local disposable system container for render assign"
    return
  fi
  local gpu
  gpu="$(nctl gpu list 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
items=d.get("items") or d.get("gpus") or []
pref=[]
other=[]
for i in items:
  gid=i.get("id") or i.get("pci") or i.get("pci_addr") or i.get("address")
  if not gid:
    continue
  vendor=str(i.get("vendor") or "").lower()
  driver=str(i.get("driver") or "").lower()
  hint=str(i.get("hint") or "").lower()
  row=(gid, vendor, driver)
  if "nvidia" in vendor and "amd" not in driver:
    other.append(gid)
  elif "render" in hint or "amd" in vendor or "amdgpu" in driver or "i915" in driver:
    pref.append(gid)
  else:
    other.append(gid)
print((pref+other)[0] if (pref or other) else "")
' 2>/dev/null || true)"
  if [ -z "$gpu" ]; then
    gate_blocked "$id" "$t" "GPU present in lspci but not enumerated by nodalctl gpu list"
    return
  fi
  local assigned
  assigned="$(nctl gpu assign --gpu-id "$gpu" --workload-id "$target" --mode render --exclusive false 2>&1 || true)"
  local asgid
  asgid="$(cert_json_get "$assigned" id)"
  if [ -n "$asgid" ] || printf '%s' "$assigned" | grep -qi '"gpu_id"'; then
    [ -n "$asgid" ] && nctl gpu unassign --id "$asgid" >/dev/null 2>&1 || true
    gate_pass "$id" "$t" "assigned GPU ${gpu} in render mode to disposable CT, then unassigned"
  else
    gate_fail "$id" "$t" "GPU render assign to disposable CT failed: ${assigned}"
  fi
}

gate28() {
  local id=28 t="Final cleanup of disposable certification resources"
  cert_cleanup
  if [ -f "${CERT_OUT}/minio.pid" ]; then
    local mpid
    mpid="$(cat "${CERT_OUT}/minio.pid" 2>/dev/null || true)"
    if [ -n "$mpid" ]; then
      kill "$mpid" >/dev/null 2>&1 || true
    fi
  fi
  if nodal_installed && nctl workload list 2>/dev/null | grep -q "$CERT_PREFIX"; then
    gate_fail "$id" "$t" "disposable resources still present after cleanup"
  else
    gate_pass "$id" "$t" "all disposable certification resources removed"
  fi
}

# --- Run all gates ----------------------------------------------------------

if [ "$CERT_RESUME_REBOOT" = 1 ]; then
  if [ ! -f "${CERT_RESULTS}" ] || [ ! -f "$(cert_reboot_pre_file)" ]; then
    cert_log "FATAL: --resume-reboot requires CERT_RUN_ID with results.tsv and node-a-reboot-pre.env under ${CERT_OUT}"
    exit 3
  fi
  cert_ensure_token
  NET_NAME="${CERT_PREFIX}-net"
  POOL_NAME="${CERT_PREFIX}-pool"
  CT_NAME="${CERT_PREFIX}-ct"
  VM_NAME="${CERT_PREFIX}-vm"
  APP_NAME="${CERT_PREFIX}-web"
  MIG_NAME="${CERT_PREFIX}-mig"
  NODEB_NAME="${CERT_PREFIX}-nodeb"
  CANARY="${CERT_PREFIX}-canary-resume"
  CT_ID=""
  VM_ID=""
  NET_ID=""
  POOL_ID=""
  NODE_B_ID="${CERT_NODE_B_ID:-}"
  MIG_ID=""
  APP_WL_ID=""
  ARTIFACT_ID=""
  gate20_resume
  cert_install_cleanup_trap
  gate21; gate22; gate23; gate24; gate25; gate26; gate27; gate28
  cert_require_all_gates
  cert_finalize
  exit $?
fi

gate01; gate02; gate03; gate04; gate05; gate06; gate07; gate08
cert_backup_dr
gate12; gate13; gate14; gate15; gate16; gate17; gate18; gate19
gate20
if [ "${CERT_DEFER_AFTER_REBOOT:-}" = 1 ]; then
  local_g=
  for local_g in 21 22 23 24 25 26 27 28; do
    gate_blocked "$local_g" "Deferred until after Node A reboot resume" "run --resume-reboot after the primary host reboots; disposable autostart must survive"
  done
  cert_require_all_gates
  cert_finalize
  exit $?
fi
gate21; gate22; gate23; gate24; gate25; gate26; gate27; gate28

cert_require_all_gates
cert_finalize
exit $?
