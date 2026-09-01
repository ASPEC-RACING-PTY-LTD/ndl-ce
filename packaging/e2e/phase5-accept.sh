#!/bin/sh
# Phase 5 Debian acceptance. Run inside the disposable guest.
# Does not implement Files jail (Phase 6).
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase5.cj

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent
test -S /run/ndl/agent.sock
test -f /lib/systemd/system/nodal-ct@.service

systemctl show -p LoadState --value lxc-net.service | grep -qx masked
systemctl is-active lxc-net.service >/dev/null 2>&1 && exit 1 || true
systemctl show -p LoadState --value dnsmasq.service | grep -qx masked

curl -fsS "$API/api/v1/health"
echo
SETUP=$(curl -fsS "$API/api/v1/setup/status")
echo "$SETUP" | grep -q '"open":false'

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

POOLS=$(curl -fsS -b "$CJ" "$API/api/v1/storage/pools")
POOL_ID=$(echo "$POOLS" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for p in items:
    if p.get("status") in ("available","warning"):
        print(p["id"]); break
')
test -n "$POOL_ID"
echo "pool $POOL_ID"

NETS=$(curl -fsS -b "$CJ" "$API/api/v1/networks")
NET_ID=$(echo "$NETS" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for n in items:
    if n.get("name")=="accept-iso" and n.get("status") in ("available","warning"):
        print(n["id"]); break
')
test -n "$NET_ID"
echo "network $NET_ID"

EXISTING=$(curl -fsS -b "$CJ" "$API/api/v1/workloads" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for w in items:
    if w.get("name")=="accept-ct":
        print(w["id"]); break
')
if [ -n "$EXISTING" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/workloads/$EXISTING" >/tmp/ndl-wl.json
else
  curl --max-time 600 -fsS -b "$CJ" -H 'Content-Type: application/json' \
    -H 'Idempotency-Key: phase5-accept-alpine' \
    -d "{\"name\":\"accept-ct\",\"kind\":\"system-container\",\"image_pin\":\"alpine/3.21/amd64/default\",\"pool_id\":\"$POOL_ID\",\"network_id\":\"$NET_ID\",\"cpus\":1,\"memory_bytes\":268435456}" \
    "$API/api/v1/workloads" >/tmp/ndl-wl.json
fi
python3 - <<'PY'
import json
w=json.load(open("/tmp/ndl-wl.json"))
assert w["kind"]=="system-container"
assert w["id"]
assert w.get("image_verified") is True, w
assert w.get("privileged") is False
disks=w.get("disks") or []
assert disks, w
print("workload", w["id"], w.get("status"), w.get("image_pin"), disks[0].get("volume_id"))
open("/tmp/ndl-wl-id","w").write(w["id"])
open("/tmp/ndl-vol-id","w").write(disks[0]["volume_id"])
PY
WL=$(cat /tmp/ndl-wl-id)
VOL=$(cat /tmp/ndl-vol-id)

# Replay must keep the same volume UUID.
curl --max-time 120 -fsS -b "$CJ" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: phase5-accept-alpine' \
  -d "{\"name\":\"accept-ct\",\"kind\":\"system-container\",\"image_pin\":\"alpine/3.21/amd64/default\",\"pool_id\":\"$POOL_ID\",\"network_id\":\"$NET_ID\"}" \
  "$API/api/v1/workloads" >/tmp/ndl-wl-replay.json
python3 - <<PY
import json
w=json.load(open("/tmp/ndl-wl-replay.json"))
assert w["id"]=="$WL"
disks=w.get("disks") or []
assert disks and disks[0]["volume_id"]=="$VOL", w
print("idempotent replay", w["id"], disks[0]["volume_id"])
PY

test -f /var/lib/ndl/workloads/"$WL"/last-applied.json
test -f /var/lib/ndl/runtime/lxc/"$WL"/config
test -d /var/lib/ndl/cache/lxc-images

# Start if the reused object was stopped.
curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/start" >/tmp/ndl-start.json || true

# systemd starts the CT. Wait for the unit.
ok=0
i=0
while [ "$i" -lt 60 ]; do
  if systemctl is-active --quiet "nodal-ct@${WL}.service"; then
    ok=1
    break
  fi
  i=$((i+1))
  sleep 2
done
test "$ok" = 1
echo "CT unit active nodal-ct@${WL}"

# The CT itself must receive a 10.64.0.0/24 address. dnsmasq running is not enough.
got_ip=0
i=0
while [ "$i" -lt 45 ]; do
  if lxc-info -P /var/lib/ndl/runtime/lxc -n "$WL" 2>/dev/null | grep -E '10\.64\.0\.'; then
    got_ip=1
    break
  fi
  i=$((i+1))
  sleep 2
done
if [ "$got_ip" != 1 ]; then
  echo "CT did not receive a 10.64.0.0/24 DHCP address" >&2
  lxc-info -P /var/lib/ndl/runtime/lxc -n "$WL" || true
  journalctl -u "nodal-ct@${WL}.service" -n 40 --no-pager || true
  exit 1
fi
echo "CT received isolated DHCP address"

# Workloads survive control plane and agent stop.
systemctl stop ndl-control ndl-agent
sleep 2
systemctl is-active "nodal-ct@${WL}.service"
lxc-info -P /var/lib/ndl/runtime/lxc -n "$WL" | grep -i running || true
echo "CT stayed up after ndl-control ndl-agent stop"

systemctl start ndl-agent ndl-control
sleep 3
systemctl is-active ndl-control ndl-agent
curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null
curl -fsS -b "$CJ" "$API/api/v1/workloads/$WL" | python3 -c 'import json,sys; w=json.load(sys.stdin); assert w["id"]; print("workload remains", w["status"], w["id"])'
curl -fsS -b "$CJ" "$API/api/v1/setup/status" | grep -q '"open":false'
echo "PHASE5_ACCEPT_OK"
