#!/bin/sh
# Phase 3 Debian acceptance. Run inside the disposable guest.
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase3.cj
ISO=/tmp/ndl-phase3.iso
POOL_PATH=/var/lib/ndl/storage/accept

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent
test -S /run/ndl/agent.sock

curl -fsS "$API/api/v1/health"
echo
SETUP=$(curl -fsS "$API/api/v1/setup/status")
echo "setup=$SETUP"
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

ME=$(curl -fsS -b "$CJ" "$API/api/v1/me")
echo "$ME"
NODES=$(curl -fsS -b "$CJ" "$API/api/v1/nodes")
echo "$NODES" | python3 -c 'import json,sys; n=json.load(sys.stdin)["items"][0]; assert n["id"]=="2cbd41b6-6ea2-4095-b779-649080ee1785"; print("node", n["id"], n.get("status"))'

# Reuse a previous accept pool if this script is re-run after a rebuild.
POOLS=$(curl -fsS -b "$CJ" "$API/api/v1/storage/pools")
echo "$POOLS"
EXISTING=$(echo "$POOLS" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for p in items:
    if p.get("name")=="accept":
        print(p["id"]); break')
if [ -n "$EXISTING" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/storage/pools/$EXISTING" >/tmp/ndl-pool.json
else
  curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
    -d "{\"name\":\"accept\",\"path\":\"$POOL_PATH\",\"create\":true}" \
    "$API/api/v1/storage/pools" >/tmp/ndl-pool.json
fi
python3 - <<'PY'
import json
p=json.load(open("/tmp/ndl-pool.json"))
assert p["backend_type"]=="directory"
assert p["status"] in ("available","warning")
assert p["usable_bytes"] is None or p["usable_bytes"]>0
assert "incremental_send" not in (p.get("capabilities") or {}) or p["capabilities"].get("incremental_send") is False
warns=p.get("warnings") or []
texts=p.get("warning_text") or []
print("pool", p["id"], p["status"], p.get("locator"), p.get("usable_bytes"), warns)
if "root_filesystem" in warns:
    assert any("root filesystem" in t for t in texts)
    print("root-filesystem warning present")
open("/tmp/ndl-pool-id","w").write(p["id"])
PY
POOL_ID=$(cat /tmp/ndl-pool-id)
test -f "$POOL_PATH/.ndl-pool.json"

# Minimal ISO9660-looking fixture.
python3 - <<'PY'
b=bytearray(65536)
b[32769:32774]=b"CD001"
open("/tmp/ndl-phase3.iso","wb").write(b)
print("iso bytes", len(b))
PY

HAVE_IMG=$(curl -fsS -b "$CJ" "$API/api/v1/storage/images?pool_id=$POOL_ID" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
print(items[0]["id"] if items else "")')
if [ -n "$HAVE_IMG" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/storage/images/$HAVE_IMG" >/tmp/ndl-image.json
else
  curl -fsS -b "$CJ" \
    -F "pool_id=$POOL_ID" -F "kind=iso" -F "filename=phase3-accept.iso" \
    -F "file=@$ISO;type=application/x-iso9660-image" \
    "$API/api/v1/storage/images" >/tmp/ndl-image.json
fi
python3 - <<'PY'
import hashlib,json
item=json.load(open("/tmp/ndl-image.json"))
raw=open("/tmp/ndl-phase3.iso","rb").read()
assert item["kind"]=="iso"
assert item["display_name"]=="phase3-accept.iso"
assert item["size_bytes"]==len(raw)
assert item["checksum_sha256"]==hashlib.sha256(raw).hexdigest()
assert item["status"]=="available"
print("image", item["id"], item["checksum_sha256"], item["backend_ref"])
open("/tmp/ndl-image-id","w").write(item["id"])
open("/tmp/ndl-image-ref","w").write(item["backend_ref"])
PY
test -f "$POOL_PATH/$(cat /tmp/ndl-image-ref)"

curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
  -d "{\"pool_id\":\"$POOL_ID\",\"class\":\"vm-disk\",\"size_bytes\":104857600}" \
  "$API/api/v1/storage/volumes" >/tmp/ndl-vol.json
python3 - <<'PY'
import json
v=json.load(open("/tmp/ndl-vol.json"))
assert v["class"]=="vm-disk"
assert v["status"]=="available"
assert v["id"]
assert v["backend_ref"].startswith("volumes/")
print("volume", v["id"], v["backend_ref"], v.get("xattr_state"), v.get("allocated_bytes"))
open("/tmp/ndl-vol-id","w").write(v["id"])
open("/tmp/ndl-vol-ref","w").write(v["backend_ref"])
PY
VOL_FILE="$POOL_PATH/$(cat /tmp/ndl-vol-ref)"
test -f "$VOL_FILE"
if command -v getfattr >/dev/null 2>&1; then
  getfattr -n user.ndl.volume_id --only-values "$VOL_FILE" || true
fi
VOL_ID=$(cat /tmp/ndl-vol-id)
XATTR=$(getfattr -n user.ndl.volume_id --only-values "$VOL_FILE" 2>/dev/null || true)
if [ -n "$XATTR" ]; then
  echo "xattr=$XATTR"
  [ "$XATTR" = "$VOL_ID" ]
else
  echo "xattr not readable via getfattr; attr package may be absent"
fi

AFTER=$(curl -fsS -b "$CJ" "$API/api/v1/storage/pools/$POOL_ID")
echo "$AFTER" | python3 -c 'import json,sys; p=json.load(sys.stdin); print("capacity", p.get("usable_bytes"), p.get("allocated_bytes"), p.get("provisioned_bytes")); assert p.get("usable_bytes") not in (0,); assert p.get("provisioned_bytes") is None or p["provisioned_bytes"]>=104857600'

systemctl restart ndl-agent
sleep 1
systemctl restart ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent
curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null
curl -fsS -b "$CJ" "$API/api/v1/storage/pools/$POOL_ID" | python3 -c 'import json,sys; p=json.load(sys.stdin); assert p["status"] in ("available","warning"); print("after restart", p["status"], p["id"])'
curl -fsS -b "$CJ" "$API/api/v1/storage/volumes/$(cat /tmp/ndl-vol-id)" | python3 -c 'import json,sys; v=json.load(sys.stdin); assert v["status"]=="available"; print("volume remains", v["id"])'
curl -fsS -b "$CJ" "$API/api/v1/storage/images/$(cat /tmp/ndl-image-id)" | python3 -c 'import json,sys; i=json.load(sys.stdin); assert i["status"]=="available"; print("image remains", i["id"])'

# Simulate missing pool path without deleting DB objects.
mv "$POOL_PATH" /var/lib/ndl/storage/accept.offline
sleep 1
curl -fsS -b "$CJ" "$API/api/v1/storage/pools/$POOL_ID" >/tmp/ndl-unavail.json
python3 - <<'PY'
import json
p=json.load(open("/tmp/ndl-unavail.json"))
print("unavailable pool", p)
assert p["status"]=="unavailable"
assert p.get("usable_bytes") in (None,)
PY
curl -fsS -b "$CJ" "$API/api/v1/storage/volumes/$(cat /tmp/ndl-vol-id)" | python3 -c 'import json,sys; v=json.load(sys.stdin); print(v); assert v["status"]=="unavailable"'
curl -fsS -b "$CJ" "$API/api/v1/storage/images/$(cat /tmp/ndl-image-id)" | python3 -c 'import json,sys; i=json.load(sys.stdin); print(i); assert i["status"]=="unavailable"'
# Rows still exist.
python3 - <<'PY'
import json
p=json.load(open("/tmp/ndl-unavail.json"))
assert p["id"]
print("db objects retained")
PY

mv /var/lib/ndl/storage/accept.offline "$POOL_PATH"
sleep 1
curl -fsS -b "$CJ" "$API/api/v1/storage/pools/$POOL_ID" | python3 -c 'import json,sys; p=json.load(sys.stdin); print("recovered", p["status"]); assert p["status"] in ("available","warning")'
curl -fsS -b "$CJ" "$API/api/v1/storage/volumes/$(cat /tmp/ndl-vol-id)" | python3 -c 'import json,sys; v=json.load(sys.stdin); assert v["status"]=="available"'
curl -fsS -b "$CJ" "$API/api/v1/setup/status" | grep -q '"open":false'
echo "PHASE3_ACCEPT_OK"
