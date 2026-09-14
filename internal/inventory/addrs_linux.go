//go:build linux

package inventory

import (
	"net"
	"sort"
)

func liveInterfaceAddresses() map[string][]string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var list []string
		for _, a := range addrs {
			s := a.String()
			if s != "" {
				list = append(list, s)
			}
		}
		if len(list) > 0 {
			sort.Strings(list)
			out[iface.Name] = list
		}
	}
	return out
}
