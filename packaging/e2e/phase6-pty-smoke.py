#!/usr/bin/env python3
"""Stdlib websocket smoke test for nodal.term.v1."""
import base64
import json
import os
import socket
import struct
import sys
import time
import urllib.request
from http.cookiejar import MozillaCookieJar

API = "http://127.0.0.1:8080"
COOKIE_JAR = sys.argv[1] if len(sys.argv) > 1 else "/tmp/ndl-phase6.cj"


def session_cookie():
    jar = MozillaCookieJar(COOKIE_JAR)
    jar.load(ignore_discard=True, ignore_expires=True)
    for c in jar:
        if c.name == "ndl_session":
            return c.value
    raise SystemExit("session cookie missing")


def mint(path, body="{}"):
    req = urllib.request.Request(
        API + path,
        data=body.encode(),
        headers={
            "Content-Type": "application/json",
            "Cookie": "ndl_session=" + session_cookie(),
        },
    )
    with urllib.request.urlopen(req) as res:
        return json.load(res)


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
    else:
        hdr.append(0x80 | 126)
        hdr.extend(struct.pack("!H", n))
    sock.sendall(bytes(hdr) + ws_mask(data))


def ws_recv(sock):
    hdr = sock.recv(2)
    if len(hdr) < 2:
        return None
    n = hdr[1] & 0x7F
    extra = 0
    if n == 126:
        extra = 2
        n = struct.unpack("!H", sock.recv(2))[0]
    elif n == 127:
        extra = 8
        n = struct.unpack("!Q", sock.recv(8))[0]
    _ = extra
    data = b""
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            break
        data += chunk
    return data


def attach(sess, marker):
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
        f"Cookie: ndl_session={session_cookie()}\r\n"
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
    head = buf.split(b"\r\n\r\n", 1)[0]
    status = head.split(b"\r\n", 1)[0].decode()
    print("handshake", status)
    if " 101 " not in status:
        raise SystemExit("websocket handshake failed: " + status)
    ws_send(sock, frame(3, struct.pack("!HH", 24, 80)))
    ws_send(sock, frame(1, ("echo %s\n" % marker).encode()))
    deadline = time.time() + 12
    text = b""
    while time.time() < deadline:
        raw = ws_recv(sock)
        if not raw:
            continue
        if len(raw) >= 5 and raw[0] == 2:
            text += raw[5:]
            if marker.encode() in text:
                print("pty ok", sid)
                sock.close()
                return
        if len(raw) >= 5 and raw[0] == 7:
            raise SystemExit("pty error: " + raw[5:].decode(errors="replace"))
    sock.close()
    raise SystemExit("pty marker not seen: " + text.decode(errors="replace")[:400])


def main():
    kind = sys.argv[2] if len(sys.argv) > 2 else "ct"
    if kind == "ct":
        wl = sys.argv[3] if len(sys.argv) > 3 else "1376e70b-cbe0-44ee-88a9-16ce206ad056"
        attach(mint("/api/v1/workloads/%s/terminal/sessions" % wl), "PHASE6_CT_PTY_OK")
    elif kind == "host":
        node = sys.argv[3] if len(sys.argv) > 3 else "2cbd41b6-6ea2-4095-b779-649080ee1785"
        attach(mint("/api/v1/nodes/%s/terminal/sessions" % node), "PHASE6_HOST_PTY_OK")
    else:
        raise SystemExit("usage: phase6-pty-smoke.py COOKIE_JAR ct|host ID")


if __name__ == "__main__":
    main()
