//go:build linux

package lxc

import (
	"fmt"
	"net"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// netnsIPv4 returns the first IPv4 address on iface inside the network
// namespace of process pid, or "" when it cannot tell. It switches one
// locked OS thread into that namespace and back, so it runs no program,
// in the container or on the host.
func netnsIPv4(pid int, iface string) string {
	if pid <= 0 {
		return ""
	}
	target, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return ""
	}
	defer target.Close()
	result := make(chan string, 1)
	go func() {
		runtime.LockOSThread()
		self, err := os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid()))
		if err != nil {
			runtime.UnlockOSThread()
			result <- ""
			return
		}
		defer self.Close()
		if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			result <- ""
			return
		}
		addr := ""
		if ifc, err := net.InterfaceByName(iface); err == nil {
			if addrs, err := ifc.Addrs(); err == nil {
				for _, a := range addrs {
					if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
						addr = n.IP.To4().String()
						break
					}
				}
			}
		}
		// Back to our own namespace. If that fails the thread stays
		// locked, and Go ends it with this goroutine rather than reuse it.
		if err := unix.Setns(int(self.Fd()), unix.CLONE_NEWNET); err == nil {
			runtime.UnlockOSThread()
		}
		result <- addr
	}()
	return <-result
}
