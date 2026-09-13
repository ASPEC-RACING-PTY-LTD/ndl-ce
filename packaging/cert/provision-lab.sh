#!/usr/bin/env bash
# Provision disposable certification lab resources on this appliance:
# a MinIO S3 target, a Debian cloud image, and a second Debian 13 node
# (KVM guest on the existing LAN bridge) that joins the cluster.
#
# Never touches production workloads, Skila, secrets, or the production
# backup repository. All names are cert-prefixed.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=packaging/cert/lib.sh
. "${HERE}/lib.sh"

if [ "${CERT_I_UNDERSTAND:-}" != "disposable-only" ]; then
  echo "set CERT_I_UNDERSTAND=disposable-only" >&2
  exit 3
fi

cert_init
cert_ensure_token

LAB_DIR="${CERT_OUT}/lab"
mkdir -p "$LAB_DIR"
ENV_FILE="${CERT_OUT}/lab.env"
KEY="${LAB_DIR}/id_ed25519"
NODEB_NAME="${CERT_PREFIX}-nodeb"
IMG_DIR="/var/lib/ndl/cert/images"
mkdir -p "$IMG_DIR"

if [ ! -f "${KEY}" ]; then
  ssh-keygen -t ed25519 -N "" -f "$KEY" -C "$NODEB_NAME" >/dev/null
fi
PUB="$(cat "${KEY}.pub")"

POOL_ID="$(cert_default_pool_id)"
LAN_ID="$(cert_lan_network_id)"
if [ -z "$POOL_ID" ] || [ -z "$LAN_ID" ]; then
  echo "missing pool or lan-bridge" >&2
  exit 1
fi

IMG="${IMG_DIR}/debian-13-genericcloud-amd64.qcow2"
if [ ! -s "$IMG" ]; then
  cert_log "downloading Debian 13 genericcloud image"
  curl -fL --retry 3 -o "${IMG}.part" \
    "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2"
  mv "${IMG}.part" "$IMG"
fi

IMAGE_ID="$(nctl storage image list 2>/dev/null | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for i in items:
  if (i.get("display_name") or i.get("filename") or "").find("debian-13-genericcloud")>=0 and i.get("id"):
    print(i["id"]); break
' 2>/dev/null || true)"
if [ -z "$IMAGE_ID" ]; then
  raw="$(nctl storage image upload --pool-id "$POOL_ID" --kind cloud-image --file "$IMG")"
  IMAGE_ID="$(cert_json_get "$raw" id)"
fi
[ -n "$IMAGE_ID" ] || { echo "cloud image upload failed" >&2; exit 1; }

track_resource workload "$NODEB_NAME"
raw="$(nctl workload create --kind vm --name "$NODEB_NAME" --network-id "$LAN_ID" \
  --pool-id "$POOL_ID" --cloud-image-id "$IMAGE_ID" --firmware bios \
  --cpus 2 --memory-bytes 2147483648 --autostart \
  --nocloud-user debian --nocloud-host "$NODEB_NAME" \
  --ssh-authorized-key "$PUB")"
NODEB_WL="$(cert_json_get "$raw" id)"
[ -n "$NODEB_WL" ] || NODEB_WL="$(cert_id_by_name workload "$NODEB_NAME")"
[ -n "$NODEB_WL" ] || { echo "node B VM create failed: $raw" >&2; exit 1; }
nctl workload start --id "$NODEB_WL" >/dev/null 2>&1 || true
cert_wait_workload "$NODEB_WL" running 360 || { echo "node B VM did not start" >&2; exit 1; }

IP=""
for _ in $(seq 1 90); do
  IP="$(nctl workload get --id "$NODEB_WL" | python3 -c 'import json,sys
d=json.load(sys.stdin)
for n in d.get("nics") or []:
  if n.get("ipv4"):
    print(n["ipv4"]); break
print(d.get("ipv4") or "")
' 2>/dev/null | head -1)"
  [ -n "$IP" ] && break
  sleep 4
done
[ -n "$IP" ] || { echo "node B did not get a DHCP address" >&2; exit 1; }
cert_log "node B VM ${NODEB_NAME} at ${IP}"

SSH=(ssh -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8)
for _ in $(seq 1 60); do
  "${SSH[@]}" "debian@${IP}" 'true' >/dev/null 2>&1 && break
  sleep 4
done
"${SSH[@]}" "debian@${IP}" 'true' || { echo "ssh to node B failed" >&2; exit 1; }

DEBDIR="${LAB_DIR}/debs"
mkdir -p "$DEBDIR"
if ls /root/ndl-ce/out/debs/ndl-agent_*.deb >/dev/null 2>&1; then
  cp /root/ndl-ce/out/debs/ndl-agent_*.deb /root/ndl-ce/out/debs/nodalctl_*.deb "$DEBDIR/" 2>/dev/null || true
fi
if ! ls "$DEBDIR"/ndl-agent_*.deb >/dev/null 2>&1; then
  if command -v dpkg-repack >/dev/null 2>&1 || apt-get install -y dpkg-repack >/dev/null 2>&1; then
    (cd "$DEBDIR" && dpkg-repack ndl-agent nodalctl)
  fi
fi
scp -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  "$DEBDIR"/ndl-agent_*.deb "$DEBDIR"/nodalctl_*.deb "debian@${IP}:/tmp/" 
"${SSH[@]}" "debian@${IP}" 'sudo DEBIAN_FRONTEND=noninteractive dpkg -i /tmp/ndl-agent_*.deb /tmp/nodalctl_*.deb || sudo apt-get -f install -y'

TOKEN_RAW="$(nctl cluster join-token create)"
JOIN_TOKEN="$(cert_json_get "$TOKEN_RAW" token)"
[ -n "$JOIN_TOKEN" ] || { echo "join token create failed" >&2; exit 1; }
CONTROL="http://192.168.2.82:8080"
"${SSH[@]}" "debian@${IP}" "sudo nodalctl cluster join --token '${JOIN_TOKEN}' --url '${CONTROL}' --hostname ${NODEB_NAME}"
"${SSH[@]}" "debian@${IP}" 'echo NODAL_AGENT_TCP_LISTEN=:9444 | sudo tee /etc/ndl/agent.env >/dev/null; sudo mkdir -p /etc/systemd/system/ndl-agent.service.d; printf "[Service]\nEnvironmentFile=-/etc/ndl/agent.env\n" | sudo tee /etc/systemd/system/ndl-agent.service.d/dest-tcp.conf >/dev/null; sudo systemctl daemon-reload; sudo systemctl enable --now ndl-agent'

NODE_B_ID="$(cert_id_by_name node "$NODEB_NAME")"
[ -n "$NODE_B_ID" ] || NODE_B_ID="$(nctl cluster nodes | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
host="'"$NODEB_NAME"'"
for n in items:
  if n.get("hostname")==host or n.get("name")==host:
    print(n["id"]); break
')"
[ -n "$NODE_B_ID" ]
nctl node dest-listen --id "$NODE_B_ID" --addr "${IP}:9444"

{
  printf 'export CERT_NODE_B=%s\n' "$IP"
  printf 'export CERT_NODE_B_ID=%s\n' "$NODE_B_ID"
  printf 'export CERT_VM_IMAGE=%s\n' "$IMAGE_ID"
  printf 'export CERT_NODE_B_KEY=%s\n' "$KEY"
} > "$ENV_FILE"
cert_log "lab env written to ${ENV_FILE}"
cat "$ENV_FILE"
