#!/usr/bin/env bash
# No-dal CE 1.0 certification harness: shared library.
#
# Source this file; do not execute it. It provides the results ledger, the
# absolute production-safety guards, the disposable-resource registry with a
# cleanup trap, prerequisite probes, and thin wrappers over nodalctl and the
# control-plane API.
#
# Design rules enforced here:
#   - A gate is PASS only when a real product operation ran and was verified.
#   - A gate that cannot run because hardware or external infrastructure is
#     genuinely unavailable is BLOCKED-PHYSICAL, never PASS and never silently
#     skipped.
#   - Every mutating/destructive operation must target a disposable resource
#     whose name carries the run-scoped certification prefix. Anything else is
#     refused, so production workloads can never be touched.

set -uo pipefail

# --- Run identity and output locations -------------------------------------

CERT_RUN_ID="${CERT_RUN_ID:-$(date -u +%Y%m%d-%H%M%S)-$$}"
CERT_PREFIX="${CERT_PREFIX:-cert-${CERT_RUN_ID}}"
CERT_OUT="${CERT_OUT:-/var/lib/ndl/cert/${CERT_RUN_ID}}"
CERT_RESULTS="${CERT_OUT}/results.tsv"
CERT_EVIDENCE="${CERT_OUT}/evidence"
CERT_REPORT="${CERT_OUT}/report.md"

# Control-plane access. Prefer nodalctl (local peer-cred socket). When the API
# is used, NODAL_URL and NODAL_TOKEN come from the operator environment.
NODAL_URL="${NODAL_URL:-http://127.0.0.1:8080}"
NODAL_TOKEN="${NODAL_TOKEN:-}"
NODALCTL="${NODALCTL:-nodalctl}"

# Names that must never be treated as disposable. Skila is production and is
# explicitly off limits. The list is a case-insensitive substring denylist.
CERT_PRODUCTION_DENY="${CERT_PRODUCTION_DENY:-skila}"

# --- Ledger ----------------------------------------------------------------

cert_init() {
  mkdir -p "$CERT_OUT" "$CERT_EVIDENCE"
  : > "$CERT_RESULTS"
  cert_log "certification run ${CERT_RUN_ID}"
  cert_log "disposable prefix: ${CERT_PREFIX}"
  cert_log "results: ${CERT_RESULTS}"
}

cert_log() { printf '[cert] %s\n' "$*" >&2; }

# _cert_record STATUS GATE TITLE DETAIL
_cert_record() {
  local status="$1" gate="$2" title="$3" detail="${4:-}"
  printf '%s\t%s\t%s\t%s\n' "$status" "$gate" "$title" "$detail" >> "$CERT_RESULTS"
  cert_log "${status}  ${gate}  ${title}${detail:+  --  ${detail}}"
}

gate_pass()    { _cert_record PASS "$1" "$2" "${3:-}"; }
gate_fail()    { _cert_record FAIL "$1" "$2" "${3:-}"; }
gate_blocked() { _cert_record BLOCKED-PHYSICAL "$1" "$2" "${3:-}"; }

# Honesty helpers (virt detection, real upgrade, armed Node A reboot).
# shellcheck source=packaging/cert/honesty.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/honesty.sh"

# Evidence capture: append labeled command output to an evidence file and echo
# its path so a gate can cite it.
cert_evidence() {
  local name="$1"; shift
  local path="${CERT_EVIDENCE}/${name}.txt"
  {
    printf '### %s\n' "$name"
    printf '$ %s\n' "$*"
    "$@" 2>&1
    printf '\n(exit %s)\n' "$?"
  } >> "$path"
  printf '%s' "$path"
}

# --- Safety guards ----------------------------------------------------------

cert_die() { cert_log "FATAL: $*"; exit 3; }

# assert_disposable NAME: refuse any name that is not run-scoped disposable, or
# that matches the production denylist. This is the single choke point that
# prevents the harness from ever mutating a production resource.
assert_disposable() {
  local name="${1:-}"
  [ -n "$name" ] || cert_die "assert_disposable called with an empty name"
  local lower
  lower="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')"
  local deny
  for deny in $CERT_PRODUCTION_DENY; do
    case "$lower" in
      *"$deny"*) cert_die "refusing to touch protected/production name: $name" ;;
    esac
  done
  case "$name" in
    "${CERT_PREFIX}"*|nodal-restore-*) : ;;
    *) cert_die "refusing to mutate non-disposable resource: $name (must start with ${CERT_PREFIX})" ;;
  esac
}

# --- Disposable-resource registry and cleanup ------------------------------

CERT_REGISTRY="${CERT_OUT}/registry.tsv"

# track_resource TYPE NAME: record a disposable resource for later cleanup.
track_resource() {
  local kind="$1" name="$2"
  assert_disposable "$name"
  mkdir -p "$CERT_OUT"
  printf '%s\t%s\n' "$kind" "$name" >> "$CERT_REGISTRY"
}

# cert_cleanup destroys only tracked, disposable resources. It never fills a
# filesystem, never touches production, and tolerates partial state.
cert_cleanup() {
  [ -f "$CERT_REGISTRY" ] || return 0
  cert_log "cleaning up disposable certification resources"
  # Dest-owned leftovers (gate 15 restore-to-dest) need dest-listen after
  # gate 21 dpkg replaces the control process.
  if [ -n "${CERT_NODE_B_ID:-}" ] && [ -n "${CERT_NODE_B:-}" ]; then
    nctl node dest-listen --id "$CERT_NODE_B_ID" --addr "${CERT_NODE_B}:9444" >/dev/null 2>&1 || true
  fi
  # Reverse order so dependents are removed before their dependencies.
  local kind name id
  while IFS=$'\t' read -r kind name; do
    [ -n "${name:-}" ] || continue
    assert_disposable "$name"
    case "$kind" in
      workload)
        id="$(cert_id_by_name workload "$name")"
        [ -n "$id" ] && nctl workload delete --id "$id" >/dev/null 2>&1 || true
        ;;
      pool)
        id="$(cert_id_by_name pool "$name")"
        [ -n "$id" ] && nctl storage pool delete --id "$id" >/dev/null 2>&1 || true
        ;;
      network)
        id="$(cert_id_by_name network "$name")"
        [ -n "$id" ] && nctl network delete --id "$id" >/dev/null 2>&1 || true
        ;;
      target)
        id="$(cert_id_by_name target "$name")"
        [ -n "$id" ] && nctl backup target delete --id "$id" >/dev/null 2>&1 || true
        ;;
      node)
        id="$(cert_id_by_name node "$name")"
        [ -n "$id" ] && nctl cluster node revoke --id "$id" >/dev/null 2>&1 || true
        ;;
      dir)      case "$name" in "${CERT_PREFIX}"*|/var/lib/ndl/cert/*) rm -rf "$name" ;; esac ;;
      *)        cert_log "unknown resource kind on cleanup: $kind $name" ;;
    esac
  done < <(tac "$CERT_REGISTRY")
  # Run-scoped leftovers that were created but not tracked (Store install).
  if nodal_installed; then
    nctl workload list 2>/dev/null | python3 -c 'import json,sys,os
want=os.environ.get("CERT_PREFIX") or ""
items=json.load(sys.stdin).get("items") or []
for i in items:
  n=str(i.get("name") or "")
  if want and n.startswith(want) and i.get("id"):
    print(i["id"])
' 2>/dev/null | while read -r leftover; do
      [ -n "$leftover" ] || continue
      nctl workload delete --id "$leftover" >/dev/null 2>&1 || true
    done
  fi
}

cert_install_cleanup_trap() { trap cert_cleanup EXIT INT TERM; }

# --- Control-plane wrappers -------------------------------------------------

nctl() { "$NODALCTL" "$@"; }

# api METHOD PATH [BODY]: call the control-plane API with the operator token.
api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS -X "$method" -H "Content-Type: application/json")
  [ -n "$NODAL_TOKEN" ] && args+=(-H "Authorization: Bearer ${NODAL_TOKEN}")
  [ -n "$body" ] && args+=(--data "$body")
  curl "${args[@]}" "${NODAL_URL}${path}"
}

# cert_json_get JSON FIELD: print a top-level JSON string/number field.
cert_json_get() {
  printf '%s' "$1" | python3 -c 'import json,sys
raw=sys.stdin.read().strip()
if not raw:
  print(""); raise SystemExit
try:
  d=json.loads(raw)
except Exception:
  print(""); raise SystemExit
if not isinstance(d, dict):
  print(""); raise SystemExit
v=d.get(sys.argv[1],"")
if v is None:
  v=""
print(v)' "$2"
}

# cert_json_find_id JSON NAME: find id in items/nodes by name or hostname.
cert_json_find_id() {
  printf '%s' "$1" | python3 -c 'import json,sys
d=json.load(sys.stdin)
name=sys.argv[1]
items=d.get("items") or d.get("nodes") or []
if isinstance(d, list):
  items=d
for i in items:
  if i.get("name")==name or i.get("hostname")==name:
    print(i.get("id") or "")
    break
' "$2"
}

cert_id_by_name() {
  local kind="$1" name="$2" raw=""
  case "$kind" in
    workload) raw="$(nctl workload list 2>/dev/null || true)" ;;
    pool)     raw="$(nctl storage pool list 2>/dev/null || true)" ;;
    network)  raw="$(nctl network list 2>/dev/null || true)" ;;
    target)   raw="$(nctl backup target list 2>/dev/null || true)" ;;
    node)     raw="$(nctl cluster nodes 2>/dev/null || true)" ;;
    *)        return 1 ;;
  esac
  cert_json_find_id "$raw" "$name"
}

cert_wait_workload() {
  local id="$1" want="${2:-running}" timeout="${3:-240}" waited=0 st=""
  while [ "$waited" -lt "$timeout" ]; do
    st="$(nctl workload get --id "$id" 2>/dev/null | python3 -c 'import json,sys
print(json.load(sys.stdin).get("status",""))' 2>/dev/null || true)"
    [ "$st" = "$want" ] && return 0
    sleep 2
    waited=$((waited + 2))
  done
  return 1
}

cert_default_pool_id() {
  nctl storage pool list 2>/dev/null | python3 -c 'import json,sys
items=(json.load(sys.stdin).get("items") or [])
for p in items:
  if p.get("status") in ("available","warning") and p.get("name")!="":
    print(p["id"]); break
' 2>/dev/null
}

cert_files_put() {
  local id="$1" path="$2" body="$3"
  local tmp
  tmp="$(mktemp)"
  printf '%s' "$body" > "$tmp"
  local args=(-sS -X POST -F "path=${path}" -F "file=@${tmp}")
  [ -n "$NODAL_TOKEN" ] && args+=(-H "Authorization: Bearer ${NODAL_TOKEN}")
  curl "${args[@]}" "${NODAL_URL}/api/v1/workloads/${id}/files/upload"
  local rc=$?
  rm -f "$tmp"
  return "$rc"
}

cert_decode_files_content() {
  python3 -c 'import json,sys
raw=sys.stdin.read()
try:
    d=json.loads(raw)
except Exception:
    sys.stdout.write(raw)
    raise SystemExit
if isinstance(d, dict) and "content" in d and d.get("content") is not None:
    sys.stdout.write(str(d["content"]))
else:
    sys.stdout.write(raw)
'
}

cert_files_get() {
  local id="$1" path="$2"
  local args=(-sS)
  [ -n "$NODAL_TOKEN" ] && args+=(-H "Authorization: Bearer ${NODAL_TOKEN}")
  curl "${args[@]}" "${NODAL_URL}/api/v1/workloads/${id}/files/content?path=${path}" | cert_decode_files_content
}

cert_ct_unit() { printf 'nodal-ct@%s.service' "$1"; }
cert_vm_unit() { printf 'nodal-vm@%s.service' "$1"; }

# cert_ensure_token creates a disposable API token when NODAL_TOKEN is unset.
# The plaintext is kept in the environment only and is never written to evidence.
cert_ensure_token() {
  [ -n "${NODAL_TOKEN:-}" ] && return 0
  nodal_installed || return 0
  local raw
  raw="$(nctl token create --name "${CERT_PREFIX}-token" 2>/dev/null || true)"
  NODAL_TOKEN="$(cert_json_get "$raw" token)"
  export NODAL_TOKEN
}

# cert_ipv4_from_arp_table MAC [TABLE] reads a /proc/net/arp snapshot.
cert_ipv4_from_arp_table() {
  local mac="$1" table="${2:-/proc/net/arp}"
  python3 -c 'import sys
mac=sys.argv[1].lower().replace("-",":").strip()
path=sys.argv[2]
if not mac:
    raise SystemExit
for i,line in enumerate(open(path, encoding="utf-8", errors="replace")):
    if i==0:
        continue
    f=line.split()
    if len(f)<4:
        continue
    if f[3].lower()!=mac:
        continue
    if len(f)>2 and f[2]=="0x0":
        continue
    print(f[0]); break
' "$mac" "$table" 2>/dev/null
}

# cert_discover_vm_ipv4 ID waits for nics[].ipv4 or a host ARP entry for the VM MAC.
cert_discover_vm_ipv4() {
  local id="$1" raw mac ip i
  for i in $(seq 1 90); do
    raw="$(nctl workload get --id "$id" 2>/dev/null || true)"
    ip="$(printf '%s' "$raw" | python3 -c 'import json,sys
d=json.load(sys.stdin)
for n in d.get("nics") or []:
    if n.get("ipv4"):
        print(n["ipv4"]); raise SystemExit
print(d.get("ipv4") or "")
' 2>/dev/null || true)"
    if [ -n "$ip" ]; then
      printf '%s' "$ip"
      return 0
    fi
    mac="$(printf '%s' "$raw" | python3 -c 'import json,sys
d=json.load(sys.stdin)
print((d.get("mac") or ""))
' 2>/dev/null || true)"
    if [ -n "$mac" ]; then
      ip="$(cert_ipv4_from_arp_table "$mac")"
      if [ -n "$ip" ]; then
        printf '%s' "$ip"
        return 0
      fi
    fi
    sleep 4
  done
  return 1
}

cert_lan_network_id() {
  nctl network list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for n in items:
  if n.get("kind")=="lan-bridge" and n.get("status") in ("available","warning"):
    print(n["id"]); break
' 2>/dev/null
}

# cert_isolated_cidr is unique per run so leftover 10.77.0.0/24 nets cannot
# collide. Isolated-nat leftover nets are deleted by `nodalctl network delete`.
cert_isolated_cidr() {
  local n
  n="$(printf '%s' "$CERT_RUN_ID" | cksum | awk '{print $1}')"
  printf '10.77.%s.0/24' "$(( n % 200 + 20 ))"
}

# cert_ensure_isolated_net creates a disposable isolated-nat network (no uplink).
cert_ensure_isolated_net() {
  local name="$1" raw id cidr
  id="$(cert_id_by_name network "$name")"
  if [ -n "$id" ]; then
    printf '%s' "$id"
    return 0
  fi
  assert_disposable "$name"
  track_resource network "$name"
  cidr="$(cert_isolated_cidr)"
  raw="$(nctl network create --name "$name" --kind isolated-nat --cidr "$cidr" 2>/dev/null || true)"
  id="$(cert_json_get "$raw" id)"
  [ -n "$id" ] || id="$(cert_id_by_name network "$name")"
  printf '%s' "$id"
}

cert_control_node_id() {
  nctl cluster show 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for n in d.get("nodes") or []:
  if n.get("role") in ("control","") or n.get("name")=="local":
    print(n.get("id") or ""); break
' 2>/dev/null
}

cert_worker_node_id() {
  if [ -n "${CERT_NODE_B_ID:-}" ]; then
    printf '%s' "$CERT_NODE_B_ID"
    return 0
  fi
  nctl cluster nodes 2>/dev/null | python3 -c 'import json,sys,os
items=json.load(sys.stdin).get("items") or []
want=(os.environ.get("CERT_NODE_B") or "").strip()
for n in items:
  if n.get("role")=="worker" and not n.get("revoked_at"):
    print(n.get("id") or ""); raise SystemExit
if want:
  for n in items:
    if n.get("revoked_at"):
      continue
    blob=" ".join(str(n.get(k) or "") for k in ("id","name","hostname","address","listen_addr"))
    if want in blob:
      print(n.get("id") or ""); raise SystemExit
' 2>/dev/null
}

# --- Prerequisite probes (return 0 when available) -------------------------

have_cmd() { command -v "$1" >/dev/null 2>&1; }

is_debian13() {
  [ -r /etc/os-release ] || return 1
  # shellcheck disable=SC1091
  . /etc/os-release
  [ "${ID:-}" = "debian" ] && case "${VERSION_ID:-}" in 13*) return 0 ;; *) return 1 ;; esac
}

is_root() { [ "$(id -u)" -eq 0 ]; }

nodal_installed() { have_cmd "$NODALCTL" && [ -d /var/lib/ndl ]; }

control_healthy() {
  curl -fsS -m 5 "${NODAL_URL}/api/v1/health" >/dev/null 2>&1
}

setup_complete() {
  # Setup is closed once a Super Administrator exists. The status endpoint
  # reports whether first-run setup is still open.
  local out
  out="$(curl -fsS -m 5 "${NODAL_URL}/api/v1/setup/status" 2>/dev/null || true)"
  printf '%s' "$out" | grep -qi '"open"[[:space:]]*:[[:space:]]*false'
}

have_node_b() { [ -n "${CERT_NODE_B:-}" ]; }

have_r2() {
  # Only names/addresses come from the environment; secret VALUES are read by
  # the product from its own secret store, never printed by this harness.
  [ -n "${CERT_R2_BUCKET:-}" ] && [ -n "${CERT_R2_ENDPOINT:-}" ] && [ -n "${CERT_R2_TARGET_ID:-}" ]
}

have_gpu() {
  if [ -e /dev/dri/card0 ] || [ -e /dev/dri/renderD128 ] || [ -d /dev/dri ]; then
    return 0
  fi
  [ -d /sys/class/drm ] && have_cmd lspci && lspci 2>/dev/null | grep -Eiq 'vga|3d|display'
}

# require_physical GATE TITLE PROBE...: record BLOCKED-PHYSICAL and return 1 if
# any probe fails. Gates use this to stay honest on a non-appliance host.
require_physical() {
  local gate="$1" title="$2"; shift 2
  local probe
  for probe in "$@"; do
    if ! "$probe"; then
      gate_blocked "$gate" "$title" "prerequisite not met: $probe"
      return 1
    fi
  done
  return 0
}

# --- Verdict ----------------------------------------------------------------

# cert_finalize writes the report and sets the exit status:
#   0  every required gate PASS (no FAIL, no BLOCKED-*)
#   1  at least one FAIL (a real defect)
#   2  no FAIL, but BLOCKED-PHYSICAL or BLOCKED-PHYSICAL/RELEASE-ARTIFACT remain
# cert_require_all_gates records FAIL for any CE 1.0 gate the harness never
# wrote. Missing rows must never be treated as implicit PASS.
cert_require_all_gates() {
  local g
  for g in 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28; do
    if ! awk -F'\t' -v g="$g" '$2==g { found=1 } END { exit !found }' "$CERT_RESULTS" 2>/dev/null; then
      gate_fail "$g" "CE 1.0 gate was not recorded" "harness did not record a result for gate ${g}"
    fi
  done
}

cert_finalize() {
  local pass fail blocked total
  pass="$(awk -F'\t' '$1=="PASS"{n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
  fail="$(awk -F'\t' '$1=="FAIL"{n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
  blocked="$(awk -F'\t' '$1 ~ /^BLOCKED/ {n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
  pass=${pass:-0}; fail=${fail:-0}; blocked=${blocked:-0}
  total=$((pass + fail + blocked))

  {
    printf '# No-dal CE 1.0 certification report\n\n'
    printf '%s\n' "- Run: \`${CERT_RUN_ID}\`"
    printf '%s\n' "- Host: \`$(uname -a)\`"
    printf '%s\n\n' "- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '| Result | Gate | Title | Detail |\n|---|---|---|---|\n'
    local status gate title detail
    while IFS=$'\t' read -r status gate title detail; do
      printf '| %s | %s | %s | %s |\n' "$status" "$gate" "$title" "${detail//|/\\|}"
    done < "$CERT_RESULTS"
    printf '\n**PASS %s / FAIL %s / BLOCKED %s (total %s)**\n' "$pass" "$fail" "$blocked" "$total"
    if [ -s "${CERT_OUT}/integration.tsv" ]; then
      printf '\n## Integration evidence (not a physical PASS)\n\n'
      printf '| Result | Title | Detail |\n|---|---|---|\n'
      while IFS=$'\t' read -r status title detail; do
        printf '| %s | %s | %s |\n' "$status" "$title" "${detail//|/\\|}"
      done < "${CERT_OUT}/integration.tsv"
    fi
  } > "$CERT_REPORT"

  cert_log "report written to ${CERT_REPORT}"
  cert_log "PASS ${pass}  FAIL ${fail}  BLOCKED-PHYSICAL ${blocked}"

  if [ "$fail" -gt 0 ]; then
    cert_log "VERDICT: FAILED. Real defects present. CE 1.0 is NOT certified."
    return 1
  fi
  if [ "$blocked" -gt 0 ]; then
    cert_log "VERDICT: INCOMPLETE. No defects, but physical gates remain BLOCKED-PHYSICAL."
    cert_log "CE 1.0 is NOT certified until the blocked gates pass on real hardware."
    return 2
  fi
  cert_log "VERDICT: ALL REQUIRED GATES PASSED on this run."
  return 0
}
