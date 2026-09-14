#!/usr/bin/env python3
"""Paste a commented multiline block through No-DAL's lxc-attach argv and
verify every command ran. Uses a guest file so display echo cannot fake PASS.
"""
import os
import pty
import select
import subprocess
import sys
import time

ID = os.environ.get("NDL_TEST_CT_ID", "")
if not ID:
    print("set NDL_TEST_CT_ID", file=sys.stderr)
    sys.exit(2)
LXC = os.environ.get("NDL_LXC_PATH", "/var/lib/ndl/runtime/lxc")
OUT = "/tmp/ndl-paste-proof.txt"

def attach_cmd(*cmd: str) -> subprocess.CompletedProcess[str]:
    argv = [
        "/usr/bin/lxc-attach",
        "-P",
        LXC,
        "-n",
        ID,
        "--clear-env",
        "-v",
        "PATH=/usr/sbin:/usr/bin:/sbin:/bin",
        "--",
        *cmd,
    ]
    return subprocess.run(argv, check=False, capture_output=True, text=True)


def main() -> int:
    attach_cmd("/bin/rm", "-f", OUT)
    argv = [
        "/usr/bin/lxc-attach",
        "-P",
        LXC,
        "-n",
        ID,
        "--clear-env",
        "-v",
        "TERM=xterm-256color",
        "-v",
        "LANG=C.UTF-8",
        "-v",
        "HOME=/root",
        "-v",
        "USER=root",
        "-v",
        "LOGNAME=root",
        "-v",
        "SHELL=/bin/bash",
        "--",
        "/bin/sh",
        "-c",
        "cd /root 2>/dev/null; if [ -x /bin/bash ]; then exec /bin/bash --login; fi; exec /bin/sh -l",
    ]
    pid, fd = pty.fork()
    if pid == 0:
        os.execv(argv[0], argv)
    os.set_blocking(fd, False)
    buf = b""
    deadline = time.time() + 8
    while time.time() < deadline:
        r, _, _ = select.select([fd], [], [], 0.2)
        if r:
            try:
                buf += os.read(fd, 4096)
            except OSError:
                break
        if b"[?2004h" in buf or b"#" in buf:
            time.sleep(0.4)
            break
    paste = (
        f"echo start >> {OUT}\r"
        "# this is a comment\r"
        f"echo middle >> {OUT}\r"
        "# another comment\r"
        f"echo 'hello # world' >> {OUT}\r"
        f"echo end >> {OUT}\r"
    )
    os.write(fd, b"\x1b[200~" + paste.encode() + b"\x1b[201~")
    time.sleep(1.2)
    os.write(fd, b"\r")
    time.sleep(1.5)
    os.write(fd, b"exit\r")
    time.sleep(0.3)
    os.close(fd)
    proof = attach_cmd("/bin/cat", OUT)
    text = proof.stdout
    print("----- GUEST FILE -----")
    print(text)
    print("----- PTY SNIP -----")
    print(buf.decode("utf-8", "replace")[-400:])
    wanted = ["start", "middle", "hello # world", "end"]
    ok = True
    for line in wanted:
        found = line in text.splitlines()
        print(f"{'ok' if found else 'FAIL'} file has {line!r}")
        ok = ok and found
    if not ok:
        return 1
    print("PTY_PASTE_OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
