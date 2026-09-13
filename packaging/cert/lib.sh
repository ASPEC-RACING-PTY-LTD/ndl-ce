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
    "${CERT_PREFIX}"*) : ;;
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
  # Reverse order so dependents are removed before their dependencies.
  local kind name
  while IFS=$'\t' read -r kind name; do
    [ -n "${name:-}" ] || continue
    assert_disposable "$name"
    case "$kind" in
      workload) nctl workload delete --id "$name" >/dev/null 2>&1 || true ;;
      pool)     nctl pool delete --id "$name" >/dev/null 2>&1 || true ;;
      network)  nctl network delete --id "$name" >/dev/null 2>&1 || true ;;
      target)   nctl backup target delete --id "$name" >/dev/null 2>&1 || true ;;
      dir)      case "$name" in "${CERT_PREFIX}"*|/var/lib/ndl/cert/*) rm -rf "$name" ;; esac ;;
      *)        cert_log "unknown resource kind on cleanup: $kind $name" ;;
    esac
  done < <(tac "$CERT_REGISTRY")
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
#   0  every required gate PASS (no FAIL, no BLOCKED-PHYSICAL)
#   1  at least one FAIL (a real defect)
#   2  no FAIL, but BLOCKED-PHYSICAL gates remain (needs physical hardware)
cert_finalize() {
  local pass fail blocked total
  pass="$(awk -F'\t' '$1=="PASS"{n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
  fail="$(awk -F'\t' '$1=="FAIL"{n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
  blocked="$(awk -F'\t' '$1=="BLOCKED-PHYSICAL"{n++} END{print n+0}' "$CERT_RESULTS" 2>/dev/null)"
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
    printf '\n**PASS %s / FAIL %s / BLOCKED-PHYSICAL %s (total %s)**\n' "$pass" "$fail" "$blocked" "$total"
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
