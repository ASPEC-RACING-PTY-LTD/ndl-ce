//go:build linux

package lxc

import (
	"net"

	"github.com/no-dal/ndl-ce/internal/netns"
)

// netnsIPv4 returns the first IPv4 address on iface inside the network
// namespace of process pid, or "" when it cannot tell. It runs no program,
// in the container or on the host; netns.Do keeps the switch off the
// agent's main thread, so the host's /proc/net stays the host's.
func netnsIPv4(pid int, iface string) string {
	addr := ""
	_ = netns.Do(pid, func() error {
		ifc, err := net.InterfaceByName(iface)
		if err != nil {
			return err
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			return err
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				addr = n.IP.To4().String()
				return nil
			}
		}
		return nil
	})
	return addr
}
