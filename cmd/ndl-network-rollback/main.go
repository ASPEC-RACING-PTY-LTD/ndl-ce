package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func main() {
	dir := os.Getenv("NODAL_NET_STATE")
	if dir == "" {
		dir = "/var/lib/ndl/net"
	}
	root := os.Getenv("NODAL_HOST_ROOT")
	activePath := filepath.Join(dir, "rollback", "active.json")
	okPath := filepath.Join(dir, "rollback", "active.ok")
	active, err := ndnet.LoadActiveRollback(activePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Fatal(err)
	}
	deadline := active.Deadline
	if deadline.IsZero() {
		deadline = time.Now().UTC().Add(ndnet.ProbeWindow)
	}
	eng := &ndnet.Engine{StateDir: dir, NetworkDir: active.TargetDir, Root: root}
	for time.Now().UTC().Before(deadline) {
		if _, err := os.Stat(okPath); err == nil {
			return
		}
		host, herr := ndnet.CollectHostView(root)
		if herr == nil {
			if err := ndnet.ProbeManagement(host, active.ManagementIfName, active.ManagementIfIndex, active.ManagementAddresses...); err != nil {
				if rerr := eng.RestoreActive(); rerr != nil {
					log.Fatal(rerr)
				}
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	if _, err := os.Stat(okPath); err == nil {
		return
	}
	host, herr := ndnet.CollectHostView(root)
	if herr == nil && ndnet.ProbeManagement(host, active.ManagementIfName, active.ManagementIfIndex, active.ManagementAddresses...) == nil {
		return
	}
	if err := eng.RestoreActive(); err != nil {
		log.Fatal(err)
	}
}
