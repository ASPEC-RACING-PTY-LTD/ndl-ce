#!/bin/sh
# Phase 7 Debian acceptance. Run inside the disposable guest.
# Required gate items fail the script. TCG without /dev/kvm is an honest pass.
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase7.cj
NOTES=""
WL=""
UNIT_WAS_ACTIVE=0
ACCEL_KIND="tcg"

note() {
  NOTES="${NOTES}${NOTES:+; }$1"
}

fail() {
  echo "PHASE7_ACCEPT_FAIL: $1" >&2
  exit 1
}

if [ -e /dev/kvm ]; then
  ACCEL_KIND="kvm"
  echo "ACCEL=kvm"
else
  echo "ACCEL=tcg"
  note "KVM device is absent; QEMU TCG is the honest accelerator"
fi

command -v qemu-system-x86_64 >/dev/null || fail "qemu-system-x86_64 is missing; ndl-agent must Depend on qemu-system-x86"

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent >/dev/null || fail "control plane or agent is not active"

curl -fsS "$API/api/v1/health" >/dev/null
curl -fsS "$API/api/v1/setup/status" | grep -q '"open":false' || fail "setup is still open"

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-login.json
python3 - <<'PY'
import json
me=json.load(open("/tmp/ndl-login.json"))
assert me["username"]=="admin"
assert me["cluster_id"]=="086be497-e232-4d69-8bb3-0423c31ba734"
print("login ok", me["user_id"], me["cluster_id"])
PY

NODES=$(curl -fsS -b "$CJ" "$API/api/v1/nodes")
echo "$NODES" | python3 -c 'import json,sys; n=json.load(sys.stdin)["items"][0]; assert n["id"]=="2cbd41b6-6ea2-4095-b779-649080ee1785"; print("node", n["id"])'
NODE_ID=2cbd41b6-6ea2-4095-b779-649080ee1785

curl -fsS -b "$CJ" "$API/api/v1/storage/pools" >/tmp/ndl-pools.json
POOL_ID=$(python3 -c 'import json
items=json.load(open("/tmp/ndl-pools.json")).get("items") or []
for p in items:
    if p.get("status") in ("available","warning"):
        print(p["id"]); break
')
if [ -z "$POOL_ID" ]; then
  echo "no available storage pool; creating qemu-proto Directory pool"
  curl -sS -o /tmp/ndl-pool-create.json -w '%{http_code}' -b "$CJ" \
    -H 'Content-Type: application/json' \
    -d '{"name":"qemu-proto","path":"/var/lib/ndl/storage/qemu-proto","create":true}' \
    "$API/api/v1/storage/pools" >/tmp/ndl-pool-code
  POOL_CODE=$(cat /tmp/ndl-pool-code)
  if [ "$POOL_CODE" != "201" ] && [ "$POOL_CODE" != "200" ]; then
    cat /tmp/ndl-pool-create.json || true
    fail "could not create a Directory pool for qemu-proto (HTTP $POOL_CODE)"
  fi
  POOL_ID=$(python3 -c 'import json; print(json.load(open("/tmp/ndl-pool-create.json"))["id"])')
fi
[ -n "$POOL_ID" ] || fail "storage pool id is empty"
echo "pool $POOL_ID"

EXISTING=$(curl -fsS -b "$CJ" "$API/api/v1/storage/volumes" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for v in items:
    if v.get("class")=="vm-disk" and v.get("status")=="available":
        print(v["id"]); break
')
if [ -n "$EXISTING" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/storage/volumes/$EXISTING" >/tmp/ndl-vol.json
else
  VOL_CODE=$(curl -sS -o /tmp/ndl-vol.json -w '%{http_code}' -b "$CJ" \
    -H 'Content-Type: application/json' \
    -d "{\"pool_id\":\"$POOL_ID\",\"class\":\"vm-disk\",\"size_bytes\":104857600}" \
    "$API/api/v1/storage/volumes" || true)
  if [ "$VOL_CODE" != "201" ] && [ "$VOL_CODE" != "200" ]; then
    cat /tmp/ndl-vol.json || true
    fail "could not create a vm-disk volume (HTTP $VOL_CODE)"
  fi
fi
python3 - <<'PY'
import json
v=json.load(open("/tmp/ndl-vol.json"))
assert v["class"]=="vm-disk"
assert v["status"]=="available"
assert v["id"]
assert v.get("backend_ref")
open("/tmp/ndl-vol-id","w").write(v["id"])
print("volume", v["id"], v["backend_ref"])
PY
VOL=$(cat /tmp/ndl-vol-id)

PROTO_CODE=$(curl -sS -o /tmp/ndl-qemu-proto.json -w '%{http_code}' -b "$CJ" \
  -H 'Content-Type: application/json' \
  -d "{\"volume_id\":\"$VOL\",\"autostart\":true}" \
  "$API/api/v1/lab/qemu-proto" || true)
echo "POST /api/v1/lab/qemu-proto -> $PROTO_CODE"
if [ "$PROTO_CODE" != "200" ] && [ "$PROTO_CODE" != "201" ]; then
  cat /tmp/ndl-qemu-proto.json || true
  fail "POST /api/v1/lab/qemu-proto returned $PROTO_CODE"
fi

python3 - <<'PY'
import json
row=json.load(open("/tmp/ndl-qemu-proto.json"))
wid=row.get("id") or ""
assert wid, row
machine=str(row.get("machine") or "")
assert machine.startswith("pc-q35-"), row
accel=str(row.get("accel") or "")
assert accel in ("kvm","tcg"), row
open("/tmp/ndl-qemu-wl","w").write(wid)
open("/tmp/ndl-qemu-machine","w").write(machine)
print("workload", wid, "machine", machine, "accel", accel)
PY
WL=$(cat /tmp/ndl-qemu-wl)
MACHINE=$(cat /tmp/ndl-qemu-machine)

ok=0
i=0
while [ "$i" -lt 30 ]; do
  if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
    ok=1
    UNIT_WAS_ACTIVE=1
    break
  fi
  i=$((i+1))
  sleep 2
done
if [ "$ok" != 1 ]; then
  systemctl status "nodal-vm@${WL}.service" --no-pager -l || true
  journalctl -u "nodal-vm@${WL}.service" -n 80 --no-pager || true
  fail "nodal-vm@${WL}.service is not active"
fi
echo "unit active nodal-vm@${WL}"

for sock in qmp serial vnc qga; do
  path="/var/lib/ndl/runtime/qemu/${WL}/${sock}.sock"
  if [ ! -S "$path" ]; then
    fail "required socket missing: $path"
  fi
  echo "socket $path"
done

if ! systemctl is-enabled --quiet "nodal-vm@${WL}.service"; then
  fail "autostart was requested but nodal-vm@${WL} is not enabled"
fi
echo "autostart enabled nodal-vm@${WL}"

curl -fsS -b "$CJ" "$API/api/v1/lab/qemu-proto" >/tmp/ndl-qemu-live.json
python3 - <<PY
import json
row=json.load(open("/tmp/ndl-qemu-live.json"))
assert row.get("id")=="$WL", row
assert str(row.get("status") or "")=="running", row
assert str(row.get("machine") or "").startswith("pc-q35-"), row
accel=str(row.get("accel") or "")
assert accel=="$ACCEL_KIND", (accel, "$ACCEL_KIND", row)
uid=str(row.get("running_as") or "")
if not uid:
    raise SystemExit("status omitted running_as")
if uid=="0":
    raise SystemExit("qemu is running as root")
print("observe running_as", uid, "accel", accel)
PY

systemctl cat "nodal-vm@${WL}.service" | grep -q 'BindsTo=ndl-control' && fail "nodal-vm binds to ndl-control"
systemctl cat "nodal-vm@${WL}.service" | grep -q 'BindsTo=ndl-agent' && fail "nodal-vm binds to ndl-agent"

AGAIN=$(curl -sS -o /tmp/ndl-qemu-again.json -w '%{http_code}' -b "$CJ" \
  -H 'Content-Type: application/json' \
  -d "{\"volume_id\":\"$VOL\",\"autostart\":true}" \
  "$API/api/v1/lab/qemu-proto" || true)
if [ "$AGAIN" != "200" ] && [ "$AGAIN" != "201" ]; then
  cat /tmp/ndl-qemu-again.json || true
  fail "second start returned HTTP $AGAIN"
fi
UNITS=$(systemctl list-units --all --no-legend 'nodal-vm@*.service' | awk '{print $1}' | grep -c '^nodal-vm@' || true)
if [ "$UNITS" -gt 1 ]; then
  systemctl list-units --all 'nodal-vm@*.service' || true
  fail "interrupted/second start leaked extra nodal-vm units ($UNITS)"
fi
echo "second start did not leak a second unit"

systemctl stop ndl-control ndl-agent
sleep 2
if ! systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "nodal-vm@${WL} died after ndl-control ndl-agent stop"
fi
echo "VM stayed up after ndl-control ndl-agent stop"

systemctl start ndl-agent ndl-control
sleep 3
systemctl is-active ndl-control ndl-agent >/dev/null || fail "control plane or agent did not return"
curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null

STATUS_CODE=$(curl -sS -o /tmp/ndl-qemu-status.json -w '%{http_code}' -b "$CJ" \
  "$API/api/v1/lab/qemu-proto" || true)
[ "$STATUS_CODE" = "200" ] || fail "GET /api/v1/lab/qemu-proto after agent restart returned $STATUS_CODE"
python3 - <<'PY'
import json
row=json.load(open("/tmp/ndl-qemu-status.json"))
st=str(row.get("status") or "")
print("qmp reconnect status", st, row.get("reason"), row.get("machine"))
if st != "running":
    raise SystemExit("status API lost the running VM after agent restart")
machine=str(row.get("machine") or "")
want=open("/tmp/ndl-qemu-machine").read().strip()
if machine!=want:
    raise SystemExit("machine ABI changed after restart: %s vs %s" % (machine, want))
PY

QMP="/var/lib/ndl/runtime/qemu/${WL}/qmp.sock"
[ -S "$QMP" ] || fail "qmp.sock missing after agent restart"

systemctl kill "nodal-vm@${WL}.service" || true
sleep 2
STATE=$(systemctl show -p ActiveState --value "nodal-vm@${WL}.service" || true)
echo "after systemctl kill ActiveState=$STATE"
case "$STATE" in
  failed|inactive|deactivating) ;;
  *) fail "expected failed/stopped after kill, got $STATE" ;;
esac
if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "qemu still active after systemctl kill"
fi

KILL_CODE=$(curl -sS -o /tmp/ndl-qemu-dead.json -w '%{http_code}' -b "$CJ" \
  "$API/api/v1/lab/qemu-proto" || true)
[ "$KILL_CODE" = "200" ] || fail "GET status after qemu kill returned $KILL_CODE"
python3 - <<'PY'
import json, sys
row=json.load(open("/tmp/ndl-qemu-dead.json"))
st=str(row.get("status") or "").lower()
print("status after qemu kill", st, row.get("reason"))
if st in ("running","ok","success"):
    print("silent success after qemu kill is forbidden", file=sys.stderr)
    sys.exit(1)
if st not in ("failed","stopped","inactive","unavailable"):
    print("unexpected status after kill", st, file=sys.stderr)
    sys.exit(1)
PY

RESTART=$(curl -sS -o /tmp/ndl-qemu-restart.json -w '%{http_code}' -b "$CJ" \
  -H 'Content-Type: application/json' \
  -d "{\"volume_id\":\"$VOL\"}" \
  "$API/api/v1/lab/qemu-proto" || true)
if [ "$RESTART" != "200" ] && [ "$RESTART" != "201" ]; then
  cat /tmp/ndl-qemu-restart.json || true
  fail "restart after crash returned HTTP $RESTART"
fi
i=0
ok=0
while [ "$i" -lt 20 ]; do
  if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
    ok=1
    break
  fi
  i=$((i+1))
  sleep 1
done
[ "$ok" = 1 ] || fail "qemu did not return after crash restart"
STOP_CODE=$(curl -sS -o /tmp/ndl-qemu-stop.json -w '%{http_code}' -b "$CJ" \
  -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/lab/qemu-proto/stop" || true)
[ "$STOP_CODE" = "200" ] || fail "graceful stop returned HTTP $STOP_CODE"
sleep 2
if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  echo "ACPI/stop left the unit active; forcing stop"
  curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
    "$API/api/v1/lab/qemu-proto/kill" >/tmp/ndl-qemu-force.json || true
  sleep 2
fi
if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "graceful and forced stop left qemu active"
fi
echo "graceful shutdown path completed"

test "$(python3 -c 'import json; print(json.load(open("/tmp/ndl-login.json"))["cluster_id"])')" = "086be497-e232-4d69-8bb3-0423c31ba734"
test "$NODE_ID" = "2cbd41b6-6ea2-4095-b779-649080ee1785"

if aa-status >/dev/null 2>&1; then
  echo "apparmor is present"
else
  note "AppArmor aa-status is unavailable in this guest; QMP/sockets were proven without an enforcing profile"
fi

if [ -n "$NOTES" ]; then
  echo "PHASE7_ACCEPT_NOTES: $NOTES"
fi
echo "PHASE7_ACCEPT_OK"
