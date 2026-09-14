#!/bin/sh
# Phase 4 Debian acceptance. Run inside the disposable guest.
# Does not enslave the management NIC.
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase4.cj

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent
test -S /run/ndl/agent.sock
test -f /lib/systemd/system/ndl-network-rollback.service
test -x /usr/lib/ndl/ndl-network-rollback

# Stock dnsmasq would be a second DHCP server on the LAN.
systemctl show -p LoadState --value dnsmasq.service | grep -qx masked
systemctl is-active dnsmasq.service >/dev/null 2>&1 && exit 1 || true

curl -fsS "$API/api/v1/health"
echo
SETUP=$(curl -fsS "$API/api/v1/setup/status")
echo "$SETUP" | grep -q '"open":false'

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-login.json

MGMT_IF=$(ip -o route show default | awk '{print $5; exit}')
MGMT_IDX=$(cat /sys/class/net/"$MGMT_IF"/ifindex)
echo "management $MGMT_IF ifindex $MGMT_IDX"

NETD_ENTER=$(systemctl show -p ActiveEnterTimestampMonotonic systemd-networkd --value || echo 0)

EXISTING=$(curl -fsS -b "$CJ" "$API/api/v1/networks" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for n in items:
    if n.get("name")=="accept-iso":
        print(n["id"]); break')
if [ -n "$EXISTING" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/networks/$EXISTING" >/tmp/ndl-net.json
else
  curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
    -d '{"name":"accept-iso","kind":"isolated","ipv4_cidr":"10.64.0.0/24"}' \
    "$API/api/v1/networks" >/tmp/ndl-net.json
fi
python3 - <<'PY'
import json
n=json.load(open("/tmp/ndl-net.json"))
assert n["kind"]=="isolated"
assert n["status"] in ("available","warning","checking")
assert n["dhcp"] is True
assert n["id"]
assert n.get("bridge_name","").startswith("ndl")
print("network", n["id"], n["bridge_name"], n["status"], n.get("gateway"))
open("/tmp/ndl-net-id","w").write(n["id"])
open("/tmp/ndl-net-br","w").write(n.get("bridge_name",""))
PY
NET_ID=$(cat /tmp/ndl-net-id)
BR=$(cat /tmp/ndl-net-br)

curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
  -d '{}' \
  "$API/api/v1/networks/$NET_ID/apply" >/tmp/ndl-apply.json
python3 - <<'PY'
import json
n=json.load(open("/tmp/ndl-apply.json"))
assert n.get("status") == "available", n
assert n.get("dhcp") is True
print("applied", n.get("bridge_name"), n.get("status"), n.get("gateway"))
PY

test -f /etc/systemd/network/50-ndl-"$NET_ID".netdev
test -f /var/lib/ndl/net/dnsmasq/"$NET_ID".conf
grep -q "interface=$BR" /var/lib/ndl/net/dnsmasq/"$NET_ID".conf
grep -q "listen-address=10.64.0.1" /var/lib/ndl/net/dnsmasq/"$NET_ID".conf
grep -q "ConfigureWithoutCarrier=yes" /etc/systemd/network/50-ndl-"$NET_ID".network
! grep -q "$MGMT_IF" /var/lib/ndl/net/dnsmasq/"$NET_ID".conf

# Isolated bridges have no enslaved ports, so operstate stays DOWN.
# Administrative UP plus the gateway address is the honest ready state.
ip -o link show "$BR" | grep -q ',UP'
ip -o addr show "$BR" | grep -q 'inet 10.64.0.1/24'
echo "isolated bridge $BR is administratively up with gateway"

AFTER_IDX=$(cat /sys/class/net/"$MGMT_IF"/ifindex)
[ "$AFTER_IDX" = "$MGMT_IDX" ]
echo "management ifindex unchanged $AFTER_IDX"

# Isolated DHCP must stay running and bind only the isolated gateway.
sleep 2
systemctl is-active "ndl-dnsmasq@${NET_ID}.service"
systemctl is-enabled "ndl-dnsmasq@${NET_ID}.service"
ss -ulnp | grep -F "10.64.0.1:53" >/tmp/ndl-dns-listen
ss -ulnp | grep ':67' | grep -F "$BR" >/tmp/ndl-dhcp-listen
if ss -ulnp | grep -E ' 0\.0\.0\.0:67 | \[::\]:67 '; then
  echo "DHCP must not listen on all addresses" >&2
  ss -ulnp
  exit 1
fi
if ss -ulnp | grep ':67' | grep -F "$MGMT_IF"; then
  echo "DHCP must not bind the management NIC" >&2
  ss -ulnp
  exit 1
fi
echo "dnsmasq bound to isolated bridge only"

# LAN-bridge of the management NIC requires typed confirm and must not persist.
HTTP=$(curl -sS -o /tmp/ndl-lan.json -w '%{http_code}' -b "$CJ" -H 'Content-Type: application/json' \
  -d "{\"name\":\"lan-mgmt\",\"kind\":\"lan-bridge\",\"uplink_ifname\":\"$MGMT_IF\"}" \
  "$API/api/v1/networks")
python3 - <<PY
import json
code=int("$HTTP")
body=json.load(open("/tmp/ndl-lan.json"))
print("lan-bridge without confirm", code, body)
assert code==409
assert body.get("code")=="confirmation_required"
assert body.get("typed_ifname")=="$MGMT_IF"
PY
! ls /etc/systemd/network/50-ndl-*-uplink.network >/dev/null 2>&1 || true

# Dry-run apply of the isolated network.
curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
  -d '{"dry_run":true}' \
  "$API/api/v1/networks/$NET_ID/apply?dry_run=true" >/tmp/ndl-dry.json
python3 - <<'PY'
import json
p=json.load(open("/tmp/ndl-dry.json"))
assert p.get("dry_run") is True
assert p.get("dhcp") is True
print("dry-run", p.get("bridge_name"), p.get("danger"))
PY

# Control-plane restart must not bounce networkd or isolated DHCP.
systemctl stop ndl-control
sleep 1
NETD_AFTER=$(systemctl show -p ActiveEnterTimestampMonotonic systemd-networkd --value || echo 0)
systemctl is-active "ndl-dnsmasq@${NET_ID}.service"
ip -o addr show "$BR" | grep -q 'inet 10.64.0.1/24'
systemctl start ndl-control
sleep 2
if [ -n "$NETD_ENTER" ] && [ -n "$NETD_AFTER" ] && [ "$NETD_ENTER" != "0" ]; then
  [ "$NETD_ENTER" = "$NETD_AFTER" ]
  echo "systemd-networkd was not restarted by control-plane stop"
fi

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null
curl -fsS -b "$CJ" "$API/api/v1/networks/$NET_ID" | python3 -c 'import json,sys; n=json.load(sys.stdin); assert n["id"]; assert n["status"]=="available", n; print("network remains", n["status"], n["id"])'
curl -fsS -b "$CJ" "$API/api/v1/setup/status" | grep -q '"open":false'
echo "PHASE4_ACCEPT_OK"
