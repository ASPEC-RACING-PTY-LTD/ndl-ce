//go:build unix

package peercred

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// FromConn reads SO_PEERCRED from a unix connection.
func FromConn(conn net.Conn) (Creds, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Creds{}, fmt.Errorf("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Creds{}, err
	}
	var cred *unix.Ucred
	var inner error
	if err := raw.Control(func(fd uintptr) {
		cred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return Creds{}, err
	}
	if inner != nil {
		return Creds{}, inner
	}
	return Creds{UID: cred.Uid, GID: cred.Gid, PID: cred.Pid}, nil
}
