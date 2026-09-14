//go:build unix

package control

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/no-dal/ndl-ce/internal/httpapi"
)

func listenControlUnix(path string) (net.Listener, error) {
	if systemdListenFDs() > 0 {
		f := os.NewFile(3, "control.sock")
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
