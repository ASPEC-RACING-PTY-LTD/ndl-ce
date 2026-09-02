package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func main() {
	dir := os.Getenv("NODAL_DATA_DIR")
	if dir == "" {
		dir = "/var/lib/ndl"
	}
	ms, err := metrics.Open(filepath.Join(dir, "agent", "metrics.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer ms.Close()
	h := &agentrpc.Handler{
		Ident:     identity.Files{Dir: dir},
		Metrics:   ms,
		Workloads: &lxc.Engine{DataDir: dir},
		QEMU:      &qemu.Engine{DataDir: dir},
		OCI:       &oci.Engine{DataDir: dir},
	}
	recoverStaleNetwork(dir)
	go scrapeMetrics(ms, dir)
	go h.RefreshLoop(30 * time.Second)
	go h.SessionLoop(dir, 30*time.Second)
	go reattachQEMU(h.QEMU)
	if err := agentrpc.Serve(h); err != nil {
		log.Fatal(err)
	}
}

func reattachQEMU(eng *qemu.Engine) {
	if eng == nil {
		return
	}
	_ = eng.ReattachApplied(context.Background())
}

func recoverStaleNetwork(dataDir string) {
	eng := &ndnet.Engine{StateDir: filepath.Join(dataDir, "net")}
	_ = eng.RecoverStale(time.Now().UTC())
	_ = eng.RestoreNAT(context.Background())
}

func scrapeMetrics(ms *metrics.Store, dataDir string) {
	col := &metrics.Collector{FSRoot: "/", Store: ms, StorageRoot: filepath.Join(dataDir, "storage")}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	_ = col.Scrape(time.Now().UTC())
	for range t.C {
		_ = col.Scrape(time.Now().UTC())
	}
}
