//go:build !unix

package peercred

import (
	"fmt"
	"net"
)

// FromConn is unavailable on this OS.
func FromConn(net.Conn) (Creds, error) {
	return Creds{}, fmt.Errorf("SO_PEERCRED is only available on unix")
}
