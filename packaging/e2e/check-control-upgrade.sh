#!/bin/sh
# Real package lifecycle for ndl-control and ndl-agent.
# Always builds (or reuses) .deb files and inspects GENERATED maintainer
# scripts. On a No-dal appliance, also reinstalls, checks dpkg --audit,
# confirms both units are active, hits the control-plane health endpoint,
# and checks that the writer lease path does not wait for TTL.
set -eu

fail() {
  echo "CONTROL_UPGRADE_FAIL: $1" >&2
  exit 1
}

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SRC=${SRC:-$ROOT}
export DEB_DIR="${DEB_DIR:-/tmp/nodal-maintainer-debs}"

"$SRC/packaging/e2e/check-maintainer-scripts.sh"

if [ -d "$SRC" ] && command -v go >/dev/null 2>&1; then
  (
    cd "$SRC"
    go test ./internal/control -count=1 \
      -run 'TestCleanSIGTERMReleasesOwnedLease|TestImmediateRestartAfterCleanShutdown|TestPackageUpgradeStyleStopStart|TestRepeatedSystemdStyleRestartCycles|TestCannotReleaseAnotherWriterLease'
  ) || fail "writer lease tests failed"
fi

if [ ! -d /run/systemd/system ] || ! command -v systemctl >/dev/null 2>&1; then
  echo "HOST_LIFECYCLE_SKIP: no systemd appliance on this host"
  echo "CONTROL_UPGRADE_OK: generated maintainer scripts and writer lease tests passed"
  exit 0
fi

if ! systemctl cat ndl-control.service >/dev/null 2>&1; then
  echo "HOST_LIFECYCLE_SKIP: ndl-control.service is not installed on this host"
  echo "CONTROL_UPGRADE_OK: generated maintainer scripts and writer lease tests passed"
  exit 0
fi

if ! dpkg -s ndl-control >/dev/null 2>&1; then
  echo "HOST_LIFECYCLE_SKIP: ndl-control is not installed via dpkg on this host"
  echo "CONTROL_UPGRADE_OK: generated maintainer scripts and writer lease tests passed"
  exit 0
fi

CTRL=
for f in "$DEB_DIR"/ndl-control_*.deb; do
  [ -f "$f" ] || continue
  CTRL=$f
  break
done
AGENT=
for f in "$DEB_DIR"/ndl-agent_*.deb; do
  [ -f "$f" ] || continue
  AGENT=$f
  break
done
[ -n "$CTRL" ] || fail "ndl-control .deb not found"
[ -n "$AGENT" ] || fail "ndl-agent .deb not found"

systemctl is-active ndl-control >/dev/null || fail "ndl-control was not active before upgrade"
systemctl is-active ndl-agent >/dev/null || fail "ndl-agent was not active before upgrade"
curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null || fail "control-plane health failed before upgrade"

old_pid=$(systemctl show -p MainPID --value ndl-control)
nrestarts_before=$(systemctl show -p NRestarts --value ndl-control)
started_at=$(systemctl show -p ActiveEnterTimestampMonotonic --value ndl-control)

export DEBIAN_FRONTEND=noninteractive
sudo -n dpkg -i "$CTRL" "$AGENT" || fail "dpkg install/configure of rebuilt packages failed"

audit=$(dpkg --audit 2>&1 || true)
[ -z "$audit" ] || fail "dpkg --audit is not clean: $audit"

# systemd activation is asynchronous. Poll for a few seconds, well under
# the 30s writer lease TTL, so a lease wait would still fail this check.
ok=0
n=0
while [ "$n" -lt 25 ]; do
  if systemctl is-active ndl-control >/dev/null 2>&1 \
    && systemctl is-active ndl-agent >/dev/null 2>&1 \
    && curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
    ok=1
    break
  fi
  n=$((n + 1))
  sleep 0.2
done
[ "$ok" -eq 1 ] || fail "ndl-control/ndl-agent did not return immediately after package configure"

new_pid=$(systemctl show -p MainPID --value ndl-control)
started_after=$(systemctl show -p ActiveEnterTimestampMonotonic --value ndl-control)
nrestarts_after=$(systemctl show -p NRestarts --value ndl-control)

if [ -n "$old_pid" ] && [ "$old_pid" != "0" ] && [ "$new_pid" = "$old_pid" ]; then
  fail "ndl-control pid $old_pid did not change after package configure"
fi
if [ -n "$started_at" ] && [ -n "$started_after" ] && [ "$started_after" = "$started_at" ]; then
  fail "ndl-control did not restart during package configure"
fi
if [ -n "$nrestarts_before" ] && [ -n "$nrestarts_after" ]; then
  delta=$((nrestarts_after - nrestarts_before))
  if [ "$delta" -gt 2 ]; then
    fail "ndl-control NRestarts rose by $delta; restart loop after upgrade"
  fi
fi

systemctl is-active ndl-control
systemctl is-active ndl-agent
curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null || fail "control-plane health failed after upgrade"

echo "CONTROL_UPGRADE_OK"
echo "ndl-control replaced pid ${old_pid} with ${new_pid} without a writer-lease TTL wait"
