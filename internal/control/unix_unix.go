//go:build unix

package control

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/no-dal/ndl-ce/internal/httpapi"
)

func listenControlUnix(path string) (net.Listener, error) {
	if fd, ok := systemdFD("ndl-control.socket", true); ok {
		f := os.NewFile(uintptr(fd), "control.sock")
		ln, err := net.FileListener(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("systemd control socket: %w", err)
		}
		return ln, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func systemdListenFDs() int {
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n < 1 {
		return 0
	}
	if pid := os.Getenv("LISTEN_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	return n
}

// systemdFD finds a socket systemd passed by its name (FileDescriptorName,
// by default the socket unit's name). Without names, the first socket is
// the control socket, as before named sockets were used.
func systemdFD(name string, firstWithoutNames bool) (int, bool) {
	n := systemdListenFDs()
	if n < 1 {
		return 0, false
	}
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")
	if os.Getenv("LISTEN_FDNAMES") == "" {
		return 3, firstWithoutNames
	}
	for i := 0; i < n && i < len(names); i++ {
		if names[i] == name {
			return 3 + i, true
		}
	}
	return 0, false
}

// inheritedTCP returns the HTTP listener systemd holds for the control
// plane (a socket named "http"), so a restart queues requests instead of
// refusing them.
func inheritedTCP(name string) net.Listener {
	fd, ok := systemdFD(name, false)
	if !ok {
		return nil
	}
	f := os.NewFile(uintptr(fd), name)
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		log.Printf("systemd %s socket: %v", name, err)
		return nil
	}
	return ln
}

func newUnixHTTPInstance(path string, handler http.Handler) *httpInstance {
	return &httpInstance{
		name: "unix",
		srv: &http.Server{
			Addr:        path,
			Handler:     httpapi.WithUnixPeerCreds(handler),
			ConnContext: httpapi.UnixConnContext,
		},
	}
}

func appendLocalControlSocket(path string, handler http.Handler, instances []*httpInstance) []*httpInstance {
	if path == "" {
		return instances
	}
	inst := newUnixHTTPInstance(path, handler)
	ln, err := listenControlUnix(path)
	if err != nil {
		log.Printf("local control socket %s: %v (HTTP still listens)", path, err)
		return instances
	}
	inst.serve = func() error {
		return inst.srv.Serve(ln)
	}
	return append(instances, inst)
}
