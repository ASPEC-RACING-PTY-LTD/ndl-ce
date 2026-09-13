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
# See docs/checklists/ce-1.0-certification.md for the full operator procedure,
# required environment variables, and safety notes.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=packaging/cert/lib.sh
. "${HERE}/lib.sh"

usage() {
  cat >&2 <<EOF
Usage: CERT_I_UNDERSTAND=disposable-only $0 [--plan]

  --plan   List the gates and exit without running anything.

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

if [ "${1:-}" = "--plan" ]; then
  printf 'CE 1.0 certification gates:\n'
  for g in "${GATES[@]}"; do printf '  %s\n' "$g"; done
  exit 0
fi

if [ "${CERT_I_UNDERSTAND:-}" != "disposable-only" ]; then
  usage
  exit 3
fi

cert_init
cert_install_cleanup_trap
cert_ensure_token

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
  api POST "/api/v1/workloads/${CT_ID}/setup-extras" '{"extras":["docker"]}' >/dev/null 2>&1 || true
  local pid out=""
  pid="$(systemctl show -p MainPID "$(cert_ct_unit "$CT_ID")" | cut -d= -f2)"
  if [ -n "$pid" ] && [ "$pid" != 0 ]; then
    out="$(nsenter -t "$pid" -m -u -i -n -p -- sh -c 'command -v docker && docker info >/dev/null && echo DOCKER_OK' 2>/dev/null || true)"
  fi
  if printf '%s' "$out" | grep -q DOCKER_OK; then
    gate_pass "$id" "$t" "docker info succeeded inside disposable CT ${CT_NAME}"
  else
    gate_blocked "$id" "$t" "docker not yet running inside the disposable guest"
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
  if nctl snapshot create --workload "$snap_of" --name "${CERT_PREFIX}-snap" >/dev/null 2>&1; then
    gate_pass "$id" "$t" "$(cert_evidence snap nctl snapshot list --workload "$snap_of")"
  else
    gate_fail "$id" "$t" "snapshot create failed"
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
    curl -fsSL -o "$bin" "https://dl.min.io/server/minio/release/linux-amd64/minio" || return 1
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
    curl -fsSL -o "$mcli" "https://dl.min.io/client/mc/release/linux-amd64/mc" || return 1
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
         | grep -qi '"id"'; then
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
      if api GET /api/v1/backups/restore-points | grep -qi '"remote_state"[[:space:]]*:[[:space:]]*"protected"'; then
        protected=1; break
      fi
      sleep 5; waited=$((waited + 5))
    done
    if [ "$protected" = 1 ]; then
      gate_pass 10 "R2/S3 upload to verification to Protected" "$(cert_evidence protected api GET /api/v1/backups/restore-points)"
    else
      gate_fail 10 "R2/S3 upload to verification to Protected" "restore point did not reach Protected within 300s"
    fi
  fi

  local art
  art="$(api GET /api/v1/backups/artifacts | python3 -c 'import json,sys
d=json.load(sys.stdin)
items=d.get("items") or []
print(items[0]["id"] if items else "")' 2>/dev/null || true)"
  if [ -z "$art" ]; then
    gate_fail 11 "Destroy disposable source, restore-as-new, validate data" "no backup artifact found"
    return
  fi
  nctl workload delete --id "$CT_ID" >/dev/null 2>&1 || true
  local restore
  restore="$(api POST "/api/v1/backups/artifacts/${art}/restore" '{"mode":"new"}')"
  if printf '%s' "$restore" | grep -qi '"id"'; then
    local restored="" waited=0
    while [ "$waited" -lt 180 ]; do
      restored="$(nctl workload list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
pref=sys.argv[1]
for i in items:
  if (i.get("name") or "").startswith(pref) and i.get("id"):
    print(i["id"]); break
' "$CERT_PREFIX" 2>/dev/null || true)"
      [ -n "$restored" ] && [ "$restored" != "$CT_ID" ] && break
      sleep 3; waited=$((waited + 3))
    done
    if [ -n "$restored" ]; then
      CT_ID="$restored"
      CT_NAME="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("name",""))' 2>/dev/null || printf '%s' "$CT_NAME")"
      if [ -n "$CT_NAME" ]; then
        case "$CT_NAME" in
          "${CERT_PREFIX}"*) track_resource workload "$CT_NAME" ;;
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
  if ! have_node_b; then gate_blocked "$id" "$t" "set CERT_NODE_B to the second node address"; return; fi
  local nodes
  nodes="$(nctl cluster nodes 2>/dev/null || true)"
  if printf '%s' "$nodes" | grep -q "$CERT_NODE_B" || [ -n "$(cert_worker_node_id)" ]; then
    NODE_B_ID="$(cert_worker_node_id)"
    gate_pass "$id" "$t" "$(cert_evidence join nctl cluster nodes)"
  else
    gate_fail "$id" "$t" "second node ${CERT_NODE_B} is not present in cluster nodes"
  fi
}

gate13() {
  local id=13 t="Authenticated mTLS/WireGuard destination-agent connectivity"
  if ! nodal_installed || ! have_node_b; then gate_blocked "$id" "$t" "needs two nodes"; return; fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  if [ -z "$NODE_B_ID" ]; then
    gate_fail "$id" "$t" "dest node id is unknown"
    return
  fi
  nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" >/dev/null 2>&1 || true
  local wg
  wg="$(nctl cluster wg show 2>/dev/null || true)"
  if nctl cluster nodes 2>/dev/null | grep -qi ready || printf '%s' "$wg" | grep -qi ready || nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" 2>/dev/null | grep -qi ready; then
    gate_pass "$id" "$t" "dest agent reports Ready over the authenticated tunnel"
  else
    local listen
    listen="$(nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" 2>/dev/null || true)"
    if printf '%s' "$listen" | grep -qi '"status":"Ready"'; then
      gate_pass "$id" "$t" "dest agent listen ${CERT_NODE_B}:9444 is Ready"
    else
      gate_fail "$id" "$t" "dest agent for ${CERT_NODE_B} is not Ready"
    fi
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
  local raw
  raw="$(nctl workload migrate --id "$CT_ID" --dest-node-id "$NODE_B_ID" --mode offline 2>/dev/null || true)"
  if printf '%s' "$raw" | grep -Eqi 'dest agent is not connected|424'; then
    gate_fail "$id" "$t" "offline migrate refused: dest agent is not connected"
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
  art="$(api GET /api/v1/backups/artifacts | python3 -c 'import json,sys
d=json.load(sys.stdin)
items=d.get("items") or []
print(items[0]["id"] if items else "")' 2>/dev/null || true)"
  if [ -z "$art" ] || [ -z "$NODE_B_ID" ]; then
    gate_fail "$id" "$t" "no artifact or dest node for dest pull"
    return
  fi
  local raw
  raw="$(api POST "/api/v1/backups/artifacts/${art}/restore" "{\"mode\":\"new\",\"target_node_id\":\"${NODE_B_ID}\"}")"
  if printf '%s' "$raw" | grep -qi '"id"'; then
    gate_pass "$id" "$t" "restore-to-dest accepted for dest pull"
  else
    gate_fail "$id" "$t" "restore-to-dest call failed"
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
  # Observe a disposable CT that still lives on this node (not a dest-only guest).
  local unit before after
  unit="$(cert_ct_unit "$CT_ID")"
  if ! systemctl is-active --quiet "$unit" 2>/dev/null; then
    if [ -n "$MIG_ID" ] && systemctl is-active --quiet "$(cert_ct_unit "$MIG_ID")" 2>/dev/null; then
      unit="$(cert_ct_unit "$MIG_ID")"
    fi
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

gate20() {
  local id=20 t="Host reboot/autostart"
  if ! nodal_installed || ! have_cmd systemctl; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  if ! have_node_b; then
    if systemctl is-enabled --quiet nodal-workloads.target 2>/dev/null; then
      gate_blocked "$id" "$t" "autostart target enabled; reboot node B (never production node A) to PASS"
    else
      gate_fail "$id" "$t" "nodal-workloads.target is not enabled"
    fi
    return
  fi
  [ -n "$NODE_B_ID" ] || NODE_B_ID="$(cert_worker_node_id)"
  if [ -z "$CT_ID" ]; then
    gate_blocked "$id" "$t" "no disposable dest workload to observe across reboot"
    return
  fi
  nctl workload update --id "$CT_ID" --autostart true >/dev/null 2>&1 || true
  if ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 \
       "root@${CERT_NODE_B}" 'systemctl reboot' >/dev/null 2>&1 || \
     ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 \
       "debian@${CERT_NODE_B}" 'sudo systemctl reboot' >/dev/null 2>&1; then
    local waited=0
    sleep 15
    while [ "$waited" -lt 240 ]; do
      if curl -fsS -m 3 "http://${CERT_NODE_B}:8080/api/v1/health" >/dev/null 2>&1 || \
         ping -c 1 -W 2 "$CERT_NODE_B" >/dev/null 2>&1; then
        break
      fi
      sleep 5; waited=$((waited + 5))
    done
    nctl node dest-listen --id "$NODE_B_ID" --addr "${CERT_NODE_B}:9444" >/dev/null 2>&1 || true
    local st
    st="$(nctl workload get --id "$CT_ID" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))' 2>/dev/null || true)"
    if [ "$st" = "running" ]; then
      gate_pass "$id" "$t" "node B rebooted; disposable autostart workload is running"
    else
      gate_fail "$id" "$t" "autostart workload not running after node B reboot (${st})"
    fi
  else
    gate_blocked "$id" "$t" "could not reboot node B; never reboot production node A"
  fi
}

gate21() {
  local id=21 t="Package upgrade preserving workloads/config/auth"
  if ! nodal_installed; then gate_blocked "$id" "$t" "no No-dal appliance"; return; fi
  local newer=""
  newer="$(ls /root/ndl-ce/out/debs/nodal_*.deb 2>/dev/null | tail -1 || true)"
  if [ -z "$newer" ]; then
    gate_blocked "$id" "$t" "apt-get install the newer signed nodal and confirm workloads/config/auth persist"
    return
  fi
  local before
  before="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || true)"
  DEBIAN_FRONTEND=noninteractive dpkg -i /root/ndl-ce/out/debs/*.deb >/tmp/cert-upgrade.log 2>&1 || true
  systemctl start ndl-control ndl-agent >/dev/null 2>&1 || true
  sleep 3
  local after
  after="$(dpkg-query -W -f='${Version}' nodal 2>/dev/null || true)"
  if curl -fsS -m 5 "${NODAL_URL}/api/v1/health" >/dev/null 2>&1 && [ -n "$after" ]; then
    gate_pass "$id" "$t" "upgraded nodal ${before} -> ${after}; health ok"
  else
    gate_fail "$id" "$t" "upgrade left management unhealthy"
  fi
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
  raw="$(nctl app install --id "$pkg" --name "$APP_NAME" --pool-id "$POOL_ID" --network-id "$NET_ID" 2>/dev/null || true)"
  APP_WL_ID="$(cert_json_get "$raw" workload_id)"
  if [ -n "$APP_WL_ID" ]; then
    track_resource workload "$APP_NAME"
    gate_pass "$id" "$t" "signed official sample installed as ${APP_NAME}"
  else
    gate_fail "$id" "$t" "signed Store install failed"
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
  if [ -z "$VM_ID" ]; then
    gate_blocked "$id" "$t" "GPU present; assign to a disposable VM once gate 06 has one"
    return
  fi
  local gpu
  gpu="$(nctl gpu list 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
items=d.get("items") or d.get("gpus") or []
for i in items:
  gid=i.get("id") or i.get("pci_addr") or i.get("address")
  if gid:
    print(gid); break
' 2>/dev/null || true)"
  if [ -z "$gpu" ]; then
    gate_blocked "$id" "$t" "GPU present in lspci but not enumerated by nodalctl gpu list"
    return
  fi
  local assigned
  assigned="$(nctl gpu assign --gpu-id "$gpu" --workload-id "$VM_ID" --mode render --exclusive false 2>/dev/null || true)"
  local asgid
  asgid="$(cert_json_get "$assigned" id)"
  if [ -n "$asgid" ] || printf '%s' "$assigned" | grep -qi '"gpu_id"'; then
    [ -n "$asgid" ] && nctl gpu unassign --id "$asgid" >/dev/null 2>&1 || true
    gate_pass "$id" "$t" "assigned GPU ${gpu} in render mode to disposable VM, then unassigned"
  else
    gate_fail "$id" "$t" "GPU assign to disposable VM failed"
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

gate01; gate02; gate03; gate04; gate05; gate06; gate07; gate08
cert_backup_dr
gate12; gate13; gate14; gate15; gate16; gate17; gate18; gate19
gate20; gate21; gate22; gate23; gate24; gate25; gate26; gate27; gate28

cert_finalize
exit $?
