#!/bin/sh
# Phase 8 Debian product-VM acceptance. Run on an appliance with packages installed.
# Cloud CI does not execute this. TCG without /dev/kvm is an honest pass.
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase8.cj
NOTES=""
WL=""
ACCEL_KIND="tcg"

note() {
  NOTES="${NOTES}${NOTES:+; }$1"
}

fail() {
  echo "PHASE8_ACCEPT_FAIL: $1" >&2
  exit 1
}

if [ -e /dev/kvm ]; then
  ACCEL_KIND="kvm"
  echo "ACCEL=kvm"
else
  echo "ACCEL=tcg"
  note "KVM device is absent; QEMU TCG is the honest accelerator"
fi

command -v qemu-system-x86_64 >/dev/null || fail "qemu-system-x86_64 is missing"
command -v ip >/dev/null || fail "iproute2 is missing"

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent >/dev/null || fail "control plane or agent is not active"

curl -fsS "$API/api/v1/health" >/dev/null
curl -fsS "$API/api/v1/setup/status" | grep -q '"open":false' || fail "setup is still open"

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-p8-login.json

curl -fsS -b "$CJ" "$API/api/v1/storage/pools" >/tmp/ndl-p8-pools.json
POOL_ID=$(python3 -c 'import json
items=json.load(open("/tmp/ndl-p8-pools.json")).get("items") or []
for p in items:
    if p.get("status") in ("available","warning"):
        print(p["id"]); break
')
[ -n "$POOL_ID" ] || fail "no available storage pool"

curl -fsS -b "$CJ" "$API/api/v1/networks" >/tmp/ndl-p8-nets.json
NET_ID=$(python3 -c 'import json
items=json.load(open("/tmp/ndl-p8-nets.json")).get("items") or []
for n in items:
    if n.get("status") in ("available","warning"):
        print(n["id"]); break
')
[ -n "$NET_ID" ] || fail "no available network"

NAME="phase8-vm"
EXISTING=$(curl -fsS -b "$CJ" "$API/api/v1/workloads" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for w in items:
    if w.get("name")=="phase8-vm" and w.get("kind")=="vm":
        print(w["id"]); break
')
if [ -n "$EXISTING" ]; then
  WL="$EXISTING"
  echo "reusing $WL"
else
  curl -sS -o /tmp/ndl-p8-create.json -w '%{http_code}' -b "$CJ" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$NAME\",\"kind\":\"vm\",\"pool_id\":\"$POOL_ID\",\"network_id\":\"$NET_ID\",\"cpus\":1,\"memory_bytes\":268435456,\"nocloud\":{\"enable\":true,\"hostname\":\"phase8\",\"username\":\"debian\"},\"desired_power\":\"running\"}" \
    "$API/api/v1/workloads" >/tmp/ndl-p8-create-code
  CODE=$(cat /tmp/ndl-p8-create-code)
  if [ "$CODE" != "201" ] && [ "$CODE" != "200" ]; then
    cat /tmp/ndl-p8-create.json || true
    fail "product VM create failed HTTP $CODE"
  fi
  WL=$(python3 -c 'import json; print(json.load(open("/tmp/ndl-p8-create.json"))["id"])')
fi
[ -n "$WL" ] || fail "workload id is empty"
echo "workload $WL"

# start is idempotent
curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/start" >/tmp/ndl-p8-start.json || true

if ! systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  sleep 3
fi
if ! systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  systemctl status "nodal-vm@${WL}.service" --no-pager -l || true
  journalctl -u "nodal-vm@${WL}.service" -n 80 --no-pager || true
  fail "nodal-vm@${WL}.service is not active"
fi

UID_NAME=$(systemctl show -p User --value "nodal-vm@${WL}.service" || true)
[ "$UID_NAME" = "ndl-qemu" ] || fail "QEMU unit user is $UID_NAME not ndl-qemu"

MAINPID=$(systemctl show -p MainPID --value "nodal-vm@${WL}.service")
if [ -z "$MAINPID" ] || [ "$MAINPID" = "0" ]; then
  fail "MainPID missing"
fi
PROC_UID=$(ps -o uid= -p "$MAINPID" | tr -d ' ')
[ "$PROC_UID" != "0" ] || fail "QEMU process is root"

systemctl cat "nodal-vm@${WL}.service" | grep -q 'BindsTo=ndl-control' && fail "nodal-vm binds to ndl-control"
systemctl cat "nodal-vm@${WL}.service" | grep -q 'BindsTo=ndl-agent' && fail "nodal-vm binds to ndl-agent"

BEFORE=$(systemctl list-units --all --no-legend 'nodal-vm@*.service' | awk '{print $1}' | grep -c '^nodal-vm@' || true)
curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/start" >/dev/null
AFTER=$(systemctl list-units --all --no-legend 'nodal-vm@*.service' | awk '{print $1}' | grep -c '^nodal-vm@' || true)
[ "$BEFORE" = "$AFTER" ] || fail "duplicate start leaked a unit ($BEFORE -> $AFTER)"

systemctl stop ndl-control ndl-agent
sleep 1
if ! systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "VM died after ndl-control ndl-agent stop"
fi
systemctl start ndl-agent ndl-control
sleep 2

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null
curl -fsS -b "$CJ" "$API/api/v1/workloads/$WL" >/tmp/ndl-p8-get.json
python3 - <<'PY'
import json
w=json.load(open("/tmp/ndl-p8-get.json"))
assert w.get("kind")=="vm"
assert w.get("nics") and w["nics"][0].get("mac")
print("rediscovered", w["id"], w.get("status"), w["nics"][0]["mac"])
PY

MAC1=$(python3 -c 'import json; print(json.load(open("/tmp/ndl-p8-get.json"))["nics"][0]["mac"])')
curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/restart" >/tmp/ndl-p8-restart.json
sleep 2
curl -fsS -b "$CJ" "$API/api/v1/workloads/$WL" >/tmp/ndl-p8-get2.json
MAC2=$(python3 -c 'import json; print(json.load(open("/tmp/ndl-p8-get2.json"))["nics"][0]["mac"])')
[ "$MAC1" = "$MAC2" ] || fail "MAC changed across restart ($MAC1 -> $MAC2)"

curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{"mode":"serial"}' \
  "$API/api/v1/workloads/$WL/console/sessions" >/tmp/ndl-p8-console.json
python3 - <<'PY'
import json
s=json.load(open("/tmp/ndl-p8-console.json"))
assert s.get("ticket")
assert s.get("kind")=="console"
assert "unix" in str(s.get("backend"))
print("console ticket ok")
PY

curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/stop" >/dev/null
sleep 2
if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "graceful stop left the unit active"
fi
curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/start" >/dev/null
sleep 2
curl -fsS -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/force-stop" >/dev/null
sleep 1
if systemctl is-active --quiet "nodal-vm@${WL}.service"; then
  fail "force-stop left the unit active"
fi

echo "PHASE8_ACCEPT_PASS accel=$ACCEL_KIND notes=${NOTES:-none}"
