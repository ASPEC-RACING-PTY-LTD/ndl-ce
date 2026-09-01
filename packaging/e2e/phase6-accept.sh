#!/bin/sh
# Phase 6 Debian acceptance. Run inside the disposable guest.
set -eu

API=http://127.0.0.1:8080
CJ=/tmp/ndl-phase6.cj
OPCJ=/tmp/ndl-phase6-op.cj
VWCJ=/tmp/ndl-phase6-vw.cj

systemctl restart ndl-agent ndl-control
sleep 2
systemctl is-active ndl-control ndl-agent

curl -fsS "$API/api/v1/health"
echo
curl -fsS "$API/api/v1/setup/status" | grep -q '"open":false'

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-login.json
python3 - <<'PY'
import json
me=json.load(open("/tmp/ndl-login.json"))
assert me["username"]=="admin"
assert me["cluster_id"]=="086be497-e232-4d69-8bb3-0423c31ba734"
print("login ok", me["user_id"], me["cluster_id"])
open("/tmp/ndl-cluster-id","w").write(me["cluster_id"])
open("/tmp/ndl-admin-id","w").write(me["user_id"])
PY
CLUSTER=$(cat /tmp/ndl-cluster-id)

NODES=$(curl -fsS -b "$CJ" "$API/api/v1/nodes")
echo "$NODES" | python3 -c 'import json,sys; n=json.load(sys.stdin)["items"][0]; assert n["id"]=="2cbd41b6-6ea2-4095-b779-649080ee1785"; print("node", n["id"])'
NODE_ID=2cbd41b6-6ea2-4095-b779-649080ee1785

EXISTING=$(curl -fsS -b "$CJ" "$API/api/v1/workloads" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for w in items:
    if w.get("name")=="accept-ct":
        print(w["id"]); break
')
if [ -n "$EXISTING" ]; then
  curl -fsS -b "$CJ" "$API/api/v1/workloads/$EXISTING" >/tmp/ndl-wl.json
else
  POOLS=$(curl -fsS -b "$CJ" "$API/api/v1/storage/pools")
  POOL_ID=$(echo "$POOLS" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for p in items:
    if p.get("status") in ("available","warning"):
        print(p["id"]); break
')
  test -n "$POOL_ID"
  NETS=$(curl -fsS -b "$CJ" "$API/api/v1/networks")
  NET_ID=$(echo "$NETS" | python3 -c 'import json,sys
items=json.load(sys.stdin).get("items") or []
for n in items:
    if n.get("name")=="accept-iso" and n.get("status") in ("available","warning"):
        print(n["id"]); break
')
  test -n "$NET_ID"
  curl --max-time 600 -fsS -b "$CJ" -H 'Content-Type: application/json' \
    -H 'Idempotency-Key: phase6-accept-alpine' \
    -d "{\"name\":\"accept-ct\",\"kind\":\"system-container\",\"image_pin\":\"alpine/3.21/amd64/default\",\"pool_id\":\"$POOL_ID\",\"network_id\":\"$NET_ID\",\"cpus\":1,\"memory_bytes\":268435456}" \
    "$API/api/v1/workloads" >/tmp/ndl-wl.json
fi
python3 - <<'PY'
import json
w=json.load(open("/tmp/ndl-wl.json"))
assert w["kind"]=="system-container"
assert w["id"]
open("/tmp/ndl-wl-id","w").write(w["id"])
print("workload", w["id"], w.get("status"))
PY
WL=$(cat /tmp/ndl-wl-id)

curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/start" >/tmp/ndl-start.json || true
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

# Operator and viewer accounts (Argon2id of password1).
OP_HASH='$argon2id$v=19$m=65536,t=3,p=1$IMk5Ow+DjpcAZz3EGV6iHw$tX3D/N4slB7hDyNPnchxyRaJTqeAxKyk2U5gdolhv9w'
runuser -u postgres -- psql -d nodal -v ON_ERROR_STOP=1 <<SQL
INSERT INTO users (id, cluster_id, username, password_hash)
VALUES ('aaaaaaaa-bbbb-cccc-dddd-000000000001', '$CLUSTER', 'operator', '$OP_HASH')
ON CONFLICT (cluster_id, username) DO UPDATE SET password_hash = EXCLUDED.password_hash;
INSERT INTO users (id, cluster_id, username, password_hash)
VALUES ('aaaaaaaa-bbbb-cccc-dddd-000000000002', '$CLUSTER', 'viewer', '$OP_HASH')
ON CONFLICT (cluster_id, username) DO UPDATE SET password_hash = EXCLUDED.password_hash;
INSERT INTO role_bindings (id, cluster_id, user_id, role_id)
SELECT gen_random_uuid(), '$CLUSTER', u.id, r.id
FROM users u, roles r
WHERE u.username='operator' AND r.name='operator' AND u.cluster_id=r.cluster_id
ON CONFLICT (user_id, role_id) DO NOTHING;
INSERT INTO role_bindings (id, cluster_id, user_id, role_id)
SELECT gen_random_uuid(), '$CLUSTER', u.id, r.id
FROM users u, roles r
WHERE u.username='viewer' AND r.name='viewer' AND u.cluster_id=r.cluster_id
ON CONFLICT (user_id, role_id) DO NOTHING;
SQL

curl -fsS -c "$OPCJ" -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"password1"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-op.json
curl -fsS -c "$VWCJ" -H 'Content-Type: application/json' \
  -d '{"username":"viewer","password":"password1"}' \
  "$API/api/v1/auth/login" >/tmp/ndl-vw.json

# CT files: mkdir, upload, download.
curl -fsS -b "$CJ" -H 'Content-Type: application/json' \
  -d '{"path":"ndl-accept"}' \
  "$API/api/v1/workloads/$WL/files/mkdir" >/tmp/ndl-mkdir.json
printf 'phase6-file-body\n' > /tmp/ndl-upload.txt
curl -fsS -b "$CJ" -F 'path=ndl-accept/hello.txt' -F 'file=@/tmp/ndl-upload.txt' \
  "$API/api/v1/workloads/$WL/files/upload" >/tmp/ndl-up.json
curl -fsS -b "$CJ" -o /tmp/ndl-dl.txt \
  "$API/api/v1/workloads/$WL/files/download?path=ndl-accept/hello.txt"
grep -qx 'phase6-file-body' /tmp/ndl-dl.txt
echo "CT files upload/download ok"

# Terminal tickets.
curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{"cwd":"/"}' \
  "$API/api/v1/workloads/$WL/terminal/sessions" >/tmp/ndl-ct-term.json
python3 - <<'PY'
import json
s=json.load(open("/tmp/ndl-ct-term.json"))
assert s.get("ticket")
assert s.get("id")
print("CT terminal ticket", s["id"])
PY
curl -fsS -b "$OPCJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/terminal/sessions" >/tmp/ndl-ct-term-op.json
python3 - <<'PY'
import json
s=json.load(open("/tmp/ndl-ct-term-op.json"))
assert s.get("ticket")
print("operator CT terminal ticket", s["id"])
PY
code=$(curl -sS -o /tmp/ndl-vw-term.json -w '%{http_code}' -b "$VWCJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/terminal/sessions")
test "$code" = "403"
echo "viewer CT terminal 403"

curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/nodes/$NODE_ID/terminal/sessions" >/tmp/ndl-host-term.json
python3 - <<'PY'
import json
s=json.load(open("/tmp/ndl-host-term.json"))
assert s.get("ticket")
print("host terminal ticket", s["id"])
PY
code=$(curl -sS -o /tmp/ndl-op-host.json -w '%{http_code}' -b "$OPCJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/nodes/$NODE_ID/terminal/sessions")
test "$code" = "403"
echo "operator host terminal 403"

python3 - <<'PY'
import base64, json, os, socket, struct, sys, time
from http.cookiejar import MozillaCookieJar

def cookie(path):
    jar = MozillaCookieJar(path)
    jar.load(ignore_discard=True, ignore_expires=True)
    for c in jar:
        if c.name == "ndl_session":
            return c.value
    raise SystemExit("session cookie missing")

def frame(typ, payload=b""):
    return bytes([typ]) + struct.pack("!I", len(payload)) + payload

def ws_mask(data):
    key = os.urandom(4)
    return key + bytes(b ^ key[i % 4] for i, b in enumerate(data))

def ws_send(sock, data):
    n = len(data)
    hdr = bytearray([0x82])
    if n < 126:
        hdr.append(0x80 | n)
    elif n < 65536:
        hdr.append(0x80 | 126)
        hdr.extend(struct.pack("!H", n))
    else:
        hdr.append(0x80 | 127)
        hdr.extend(struct.pack("!Q", n))
    sock.sendall(bytes(hdr) + ws_mask(data))

def ws_recv(sock):
    hdr = sock.recv(2)
    if len(hdr) < 2:
        raise SystemExit("short websocket header")
    opcode = hdr[0] & 0x0F
    n = hdr[1] & 0x7F
    if n == 126:
        n = struct.unpack("!H", sock.recv(2))[0]
    elif n == 127:
        n = struct.unpack("!Q", sock.recv(8))[0]
    data = b""
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            break
        data += chunk
    if opcode == 8:
        return None
    return data

def attach(cookie_path, session_path, marker):
    sess = json.load(open(session_path))
    ticket = sess["ticket"]
    sid = sess["id"]
    key = base64.b64encode(os.urandom(16)).decode()
    req = (
        f"GET /api/v1/io/sessions/{sid}/ws HTTP/1.1\r\n"
        "Host: 127.0.0.1:8080\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        f"Sec-WebSocket-Protocol: ndl.ticket.{ticket}\r\n"
        f"Cookie: ndl_session={cookie(cookie_path)}\r\n"
        "\r\n"
    )
    sock = socket.create_connection(("127.0.0.1", 8080), 10)
    sock.settimeout(15)
    sock.sendall(req.encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = sock.recv(4096)
        if not chunk:
            raise SystemExit("websocket handshake closed")
        buf += chunk
    head, rest = buf.split(b"\r\n\r\n", 1)
    if b" 101 " not in head.split(b"\r\n", 1)[0]:
        raise SystemExit("websocket handshake failed: " + head.split(b"\r\n", 1)[0].decode())
    leftover = rest
    ws_send(sock, frame(3, struct.pack("!HH", 24, 80)))
    ws_send(sock, frame(1, ("echo %s\n" % marker).encode()))
    deadline = time.time() + 12
    text = leftover
    while time.time() < deadline:
        if leftover:
            leftover = b""
        raw = ws_recv(sock)
        if raw is None:
            break
        if len(raw) >= 5 and raw[0] == 2:
            text += raw[5:]
            if marker.encode() in text:
                print("pty ok", sid)
                sock.close()
                return
        elif len(raw) >= 5 and raw[0] == 7:
            raise SystemExit("pty error: " + raw[5:].decode(errors="replace"))
    sock.close()
    raise SystemExit("pty marker not seen: " + text.decode(errors="replace")[:400])

attach("/tmp/ndl-phase6.cj", "/tmp/ndl-ct-term.json", "PHASE6_CT_PTY_OK")
attach("/tmp/ndl-phase6.cj", "/tmp/ndl-host-term.json", "PHASE6_HOST_PTY_OK")
print("terminal pty attach ok")
PY

code=$(curl -sS -o /tmp/ndl-vw-dl.json -w '%{http_code}' -b "$VWCJ" \
  "$API/api/v1/workloads/$WL/files/download?path=ndl-accept/hello.txt")
test "$code" = "403"
echo "viewer download 403"

# Identities stay the same UUIDs.
curl -fsS -b "$CJ" "$API/api/v1/workloads/$WL" | python3 -c 'import json,sys; w=json.load(sys.stdin); assert w["id"]; print("identity", w["id"])'
test "$(cat /tmp/ndl-wl-id)" = "$WL"
test -f /var/lib/ndl/workloads/"$WL"/last-applied.json

# Opening a PTY must not bind the CT unit to the control plane.
curl -fsS -b "$CJ" -H 'Content-Type: application/json' -d '{}' \
  "$API/api/v1/workloads/$WL/terminal/sessions" >/tmp/ndl-ct-term2.json
systemctl stop ndl-control
sleep 2
systemctl is-active "nodal-ct@${WL}.service"
echo "CT stayed up after ndl-control stop"
systemctl start ndl-control
sleep 3
systemctl is-active ndl-control

curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct-horse"}' \
  "$API/api/v1/auth/login" >/dev/null
curl -fsS -b "$CJ" "$API/api/v1/setup/status" | grep -q '"open":false'
echo "PHASE6_ACCEPT_OK"
