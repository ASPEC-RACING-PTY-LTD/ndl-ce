#!/usr/bin/env bash
# Honesty helpers for CE 1.0 physical gates. Sourced by lib.sh.
# These functions exist so a virtual Node B, an unarmed Node A reboot, or a
# same-version reinstall cannot be recorded as a required physical PASS.

# Token that arms the destructive primary-host reboot gate. The harness still
# will not reboot until CERT_REBOOT_NODE_A_EXECUTE=now.
CERT_REBOOT_NODE_A_TOKEN="I-UNDERSTAND-REBOOT-PRIMARY"

# cert_virt_kind_from_facts DETECT_VIRT DETECT_RC SYS_VENDOR PRODUCT_NAME [CPU_FLAGS]
# Returns a virt kind on stdout. A guest/VM/container is never "none".
# "none" is only returned when systemd-detect-virt reports none (exit 1) and
# DMI/cpuinfo do not indicate a hypervisor. Anything else is fail-closed
# (unknown or a specific guest kind) and is not physical.
cert_virt_kind_from_facts() {
  local virt="${1:-}" rc="${2:-1}" vendor="${3:-}" product="${4:-}" flags="${5:-}"
  virt="$(printf '%s' "$virt" | tr '[:upper:]' '[:lower:]' | tr -d '\r')"
  vendor="$(printf '%s' "$vendor" | tr '[:upper:]' '[:lower:]' | tr -d '\r')"
  product="$(printf '%s' "$product" | tr '[:upper:]' '[:lower:]' | tr -d '\r')"
  flags="$(printf '%s' "$flags" | tr '[:upper:]' '[:lower:]')"
  case "$virt" in
    none|"") : ;;
    kvm|qemu|qemu-kvm|qemu-user|uml|bochs|microsoft|hyperv|hyper-v|vmware|oracle|xen|parallels|bhyve|acrn|powervm|amazon|google)
      printf '%s\n' "$virt"; return 0 ;;
    lxc|lxc-libvirt|docker|podman|container|openvz|systemd-nspawn|proot|wsl|pvm)
      printf '%s\n' "$virt"; return 0 ;;
    *)
      if [ "$rc" = 0 ] && [ -n "$virt" ]; then
        printf '%s\n' "$virt"
        return 0
      fi
      ;;
  esac
  case "$vendor $product" in
    *qemu*|*bochs*|*seabios*|*innotek*|*virtualbox*|*vmware*|*xen*|*hyper-v*|*microsoft*virtual*|*google*|*amazon*|*digitalocean*|*kubevirt*|*openstack*|*ovirt*|*rhev*)
      printf 'qemu\n'; return 0 ;;
  esac
  case "$product" in
    *virtual*|*kvm*|*standard*pc*|*openstack*|*compute*engine*)
      printf 'qemu\n'; return 0 ;;
  esac
  case "$flags" in
    *hypervisor*) printf 'hypervisor\n'; return 0 ;;
  esac
  if [ "$virt" = "none" ] && [ "$rc" != 0 ]; then
    printf 'none\n'
    return 0
  fi
  printf 'unknown\n'
}

# cert_virt_is_physical KIND: 0 only for a proven bare-metal host.
cert_virt_is_physical() {
  [ "${1:-}" = "none" ]
}

# cert_physical_join_verdict VIRT NODE_ID
# PASS only when a node id is present and virt kind is proven physical.
cert_physical_join_verdict() {
  local virt="${1:-unknown}" node_id="${2:-}"
  if ! cert_virt_is_physical "$virt"; then
    printf 'BLOCKED-PHYSICAL\n'
    return 0
  fi
  if [ -z "$node_id" ]; then
    printf 'FAIL\n'
    return 0
  fi
  printf 'PASS\n'
}

# cert_upgrade_kind BEFORE AFTER
# real | same | downgrade | missing
cert_upgrade_kind() {
  local before="${1:-}" after="${2:-}"
  if [ -z "$before" ] || [ -z "$after" ]; then
    printf 'missing\n'
    return 0
  fi
  if [ "$before" = "$after" ]; then
    printf 'same\n'
    return 0
  fi
  if command -v dpkg >/dev/null 2>&1; then
    if dpkg --compare-versions "$after" gt "$before" 2>/dev/null; then
      printf 'real\n'
      return 0
    fi
    if dpkg --compare-versions "$after" lt "$before" 2>/dev/null; then
      printf 'downgrade\n'
      return 0
    fi
    printf 'same\n'
    return 0
  fi
  printf 'missing\n'
}

# cert_upgrade_gate_status KIND
# The CE 1.0 upgrade gate PASSes only on a real version transition.
cert_upgrade_gate_status() {
  case "${1:-}" in
    real) printf 'PASS\n' ;;
    *) printf 'BLOCKED-PHYSICAL/RELEASE-ARTIFACT\n' ;;
  esac
}

cert_deb_version() {
  local f="$1"
  [ -f "$f" ] || return 1
  dpkg-deb -f "$f" Version 2>/dev/null
}

cert_upgrade_deb_dirs() {
  if [ -n "${CERT_UPGRADE_DEB_DIR:-}" ]; then
    printf '%s\n' "$CERT_UPGRADE_DEB_DIR"
  fi
  printf '%s\n' "${CERT_TREE_DEBS:-/root/ndl-ce/out/debs}"
  printf '%s\n' /var/cache/apt/archives
}

# cert_best_newer_nodal_deb INSTALLED [FILE...]
# Prints the newest nodal_*.deb whose Version is greater than INSTALLED.
cert_best_newer_nodal_deb() {
  local installed="${1:-}" best="" best_ver="" f ver kind
  shift || true
  [ -n "$installed" ] || return 1
  for f in "$@"; do
    [ -f "$f" ] || continue
    case "$f" in
      */nodal_*.deb) : ;;
      *) continue ;;
    esac
    ver="$(cert_deb_version "$f" || true)"
    [ -n "$ver" ] || continue
    kind="$(cert_upgrade_kind "$installed" "$ver")"
    if [ "$kind" != "real" ]; then
      continue
    fi
    if [ -z "$best_ver" ] || { command -v dpkg >/dev/null 2>&1 && dpkg --compare-versions "$ver" gt "$best_ver" 2>/dev/null; }; then
      best="$f"
      best_ver="$ver"
    fi
  done
  [ -n "$best" ] || return 1
  printf '%s\n' "$best"
}

cert_collect_nodal_debs() {
  local d f
  while read -r d; do
    [ -d "$d" ] || continue
    for f in "$d"/nodal_*.deb; do
      [ -f "$f" ] || continue
      printf '%s\n' "$f"
    done
  done < <(cert_upgrade_deb_dirs)
}

cert_sibling_debs() {
  local nodal_deb="$1" dir ver p f
  dir="$(dirname "$nodal_deb")"
  ver="$(cert_deb_version "$nodal_deb")"
  [ -n "$ver" ] || return 1
  for p in nodal ndl-control ndl-agent nodalctl; do
    for f in "$dir"/${p}_*.deb; do
      [ -f "$f" ] || continue
      if [ "$(cert_deb_version "$f")" = "$ver" ]; then
        printf '%s\n' "$f"
        break
      fi
    done
  done
}

cert_integration() {
  local status="$1" title="$2" detail="${3:-}"
  mkdir -p "${CERT_OUT:-/tmp}"
  printf '%s\t%s\t%s\n' "$status" "$title" "$detail" >> "${CERT_OUT}/integration.tsv"
  if command -v cert_log >/dev/null 2>&1; then
    cert_log "INTEGRATION  ${status}  ${title}${detail:+  --  ${detail}}"
  fi
}

gate_blocked_release() {
  _cert_record "BLOCKED-PHYSICAL/RELEASE-ARTIFACT" "$1" "$2" "${3:-}"
}

cert_replace_gate() {
  local status="$1" gate="$2" title="$3" detail="${4:-}"
  local tmp
  tmp="$(mktemp)"
  if [ -f "$CERT_RESULTS" ]; then
    awk -F'\t' -v g="$gate" '$2!=g' "$CERT_RESULTS" > "$tmp" || true
    mv "$tmp" "$CERT_RESULTS"
  else
    rm -f "$tmp"
  fi
  _cert_record "$status" "$gate" "$title" "$detail"
}

cert_node_b_ssh() {
  local host="${CERT_NODE_B:-}"
  [ -n "$host" ] || return 1
  local ssh_opts=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8)
  if [ -n "${CERT_NODE_B_KEY:-}" ] && [ -f "$CERT_NODE_B_KEY" ]; then
    ssh_opts+=(-i "$CERT_NODE_B_KEY")
  fi
  ssh "${ssh_opts[@]}" "debian@${host}" "$@" 2>/dev/null || \
    ssh "${ssh_opts[@]}" "root@${host}" "$@" 2>/dev/null
}

# cert_node_b_virt_kind probes Node B. Fail-closed: unknown if SSH/facts missing.
# CERT_NODE_B_VIRT_FACTS is honored only when CERT_HONESTY_TEST=1 (selftest hook).
cert_node_b_virt_kind() {
  local raw virt rc=1 vendor product flags
  if [ "${CERT_HONESTY_TEST:-}" = 1 ] && [ -n "${CERT_NODE_B_VIRT_FACTS:-}" ]; then
    IFS='|' read -r virt rc vendor product flags <<EOF
${CERT_NODE_B_VIRT_FACTS}
EOF
    cert_virt_kind_from_facts "$virt" "$rc" "$vendor" "$product" "$flags"
    return 0
  fi
  raw="$(cert_node_b_ssh 'sh -s' <<'REMOTE' || true
virt="$(systemd-detect-virt 2>/dev/null)"
rc=$?
if [ -z "$virt" ]; then
  virt="$(systemd-detect-virt --vm 2>/dev/null || true)"
fi
if [ -f /.dockerenv ] || [ -f /run/.containerenv ]; then
  virt="${virt:-docker}"
  rc=0
fi
if [ -r /run/systemd/container ]; then
  virt="${virt:-container}"
  rc=0
fi
vendor="$(cat /sys/class/dmi/id/sys_vendor 2>/dev/null || cat /sys/devices/virtual/dmi/id/sys_vendor 2>/dev/null || true)"
product="$(cat /sys/class/dmi/id/product_name 2>/dev/null || cat /sys/devices/virtual/dmi/id/product_name 2>/dev/null || true)"
bios="$(cat /sys/class/dmi/id/bios_vendor 2>/dev/null || true)"
printf 'VIRT=%s\n' "$virt"
printf 'VIRT_RC=%s\n' "$rc"
printf 'VENDOR=%s %s\n' "$vendor" "$bios"
printf 'PRODUCT=%s\n' "$product"
printf 'FLAGS=%s\n' "$(grep -m1 '^flags' /proc/cpuinfo 2>/dev/null || true)"
REMOTE
)"
  virt="$(printf '%s\n' "$raw" | sed -n 's/^VIRT=//p' | head -1)"
  rc="$(printf '%s\n' "$raw" | sed -n 's/^VIRT_RC=//p' | head -1)"
  vendor="$(printf '%s\n' "$raw" | sed -n 's/^VENDOR=//p' | head -1)"
  product="$(printf '%s\n' "$raw" | sed -n 's/^PRODUCT=//p' | head -1)"
  flags="$(printf '%s\n' "$raw" | sed -n 's/^FLAGS=//p' | head -1)"
  if [ -z "$raw" ]; then
    printf 'unknown\n'
    return 0
  fi
  cert_virt_kind_from_facts "$virt" "${rc:-1}" "$vendor" "$product" "$flags"
}

cert_reboot_armed() {
  [ "${CERT_REBOOT_NODE_A:-}" = "$CERT_REBOOT_NODE_A_TOKEN" ]
}

cert_reboot_execute() {
  cert_reboot_armed && [ "${CERT_REBOOT_NODE_A_EXECUTE:-}" = "now" ]
}

cert_node_a_boot_id() {
  cat /proc/sys/kernel/random/boot_id 2>/dev/null || true
}

cert_prod_ct_units() {
  systemctl list-units --type=service --state=running --no-legend 'nodal-ct@*' 2>/dev/null | awk '{print $1}' | sort
}

cert_workload_name_counts() {
  nctl workload list 2>/dev/null | python3 -c 'import json,sys
from collections import Counter
items=json.load(sys.stdin).get("items") or []
c=Counter(str(i.get("name") or "") for i in items if i.get("name"))
for n,k in sorted(c.items()):
  print("%s\t%s" % (n,k))
' 2>/dev/null || true
}

# cert_node_a_reboot_verdict PRE_BOOT NOW_BOOT MGMT AUTOSTART PROD DUPS
# MGMT/AUTOSTART/PROD/DUPS are "ok" or any other token.
# PASS only when boot_id changed and every recovery check is ok.
cert_node_a_reboot_verdict() {
  local pre_boot="${1:-}" now_boot="${2:-}" mgmt="${3:-}" autostart="${4:-}" prod="${5:-}" dups="${6:-}"
  if [ -z "$pre_boot" ] || [ -z "$now_boot" ] || [ "$pre_boot" = "$now_boot" ]; then
    printf 'BLOCKED-PHYSICAL\n'
    return 0
  fi
  if [ "$mgmt" != ok ] || [ "$autostart" != ok ] || [ "$prod" != ok ] || [ "$dups" != ok ]; then
    printf 'FAIL\n'
    return 0
  fi
  printf 'PASS\n'
}

cert_reboot_pre_file() { printf '%s\n' "${CERT_OUT}/node-a-reboot-pre.env"; }

cert_write_node_a_reboot_pre() {
  local pre boot control autostart_id="${1:-}" autostart_name="${2:-}" autostart_unit="${3:-}"
  mkdir -p "$CERT_OUT"
  pre="$(cert_reboot_pre_file)"
  boot="$(cert_node_a_boot_id)"
  control="$(cert_control_node_id 2>/dev/null || true)"
  cert_prod_ct_units > "${CERT_OUT}/node-a-reboot-prod-units.txt"
  cert_workload_name_counts > "${CERT_OUT}/node-a-reboot-names.tsv"
  {
    printf 'PRE_BOOT_ID=%q\n' "$boot"
    printf 'PRE_AUTOSTART_ID=%q\n' "$autostart_id"
    printf 'PRE_AUTOSTART_NAME=%q\n' "$autostart_name"
    printf 'PRE_AUTOSTART_UNIT=%q\n' "$autostart_unit"
    printf 'PRE_CONTROL_NODE_ID=%q\n' "$control"
    printf 'PRE_CERT_PREFIX=%q\n' "${CERT_PREFIX}"
    printf 'PRE_WRITTEN_AT=%q\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$pre"
}

cert_prod_units_healthy() {
  local pre="${CERT_OUT}/node-a-reboot-prod-units.txt" u
  [ -f "$pre" ] || return 1
  while read -r u; do
    [ -n "$u" ] || continue
    systemctl is-active --quiet "$u" || return 1
  done < "$pre"
  return 0
}

cert_workload_duplicate_names() {
  cert_workload_name_counts | awk -F'\t' '$2+0>1 {print}'
}

# Names and ids only. Never dump secrets, tokens, or backup payloads.
cert_preservation_digest() {
  python3 - <<'PY'
import json, os, subprocess, urllib.request

def nctl(*args):
    try:
        out = subprocess.check_output(["nodalctl", *args], stderr=subprocess.DEVNULL, text=True)
        return json.loads(out)
    except Exception:
        return {}

def ids(kind, *args, key="items"):
    data = nctl(*args)
    items = data.get(key) or data.get("nodes") or []
    rows = []
    for i in items:
        rows.append("%s\t%s\t%s" % (kind, i.get("id") or "", i.get("name") or i.get("hostname") or i.get("role") or ""))
    return sorted(rows)

url = os.environ.get("NODAL_URL") or "http://127.0.0.1:8080"
try:
    with urllib.request.urlopen(url + "/api/v1/setup/status", timeout=5) as r:
        setup = json.load(r)
except Exception:
    setup = {}
print("setup_open=%s" % setup.get("open"))
for row in ids("workload", "workload", "list"):
    print(row)
for row in ids("pool", "storage", "pool", "list"):
    print(row)
for row in ids("network", "network", "list"):
    print(row)
for row in ids("node", "cluster", "nodes"):
    print(row)
for row in ids("target", "backup", "target", "list"):
    print(row)
PY
}
