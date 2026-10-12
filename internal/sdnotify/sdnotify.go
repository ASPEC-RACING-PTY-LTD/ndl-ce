// Package sdnotify sends readiness to systemd for Type=notify units.
package sdnotify

import (
	"net"
	"os"
)

// Ready tells systemd the service is ready. It is a no-op outside systemd
// (NOTIFY_SOCKET unset). Errors are returned for logging only.
func Ready() error {
	return Send("READY=1")
}

// Send writes one notification state string to NOTIFY_SOCKET.
func Send(state string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	if path[0] == '@' {
		path = "\x00" + path[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}
