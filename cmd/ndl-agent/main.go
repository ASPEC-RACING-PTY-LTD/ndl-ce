package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/ctbackup"
	"github.com/no-dal/ndl-ce/internal/diskguard"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
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
		GameRoot:  dir,
		Disk: diskguard.New(filepath.Join(dir, "reserve", "ballast"),
			diskguard.Watch{Path: "/", Role: "root"},
			diskguard.Watch{Path: dir, Role: "data"},
			diskguard.Watch{Path: "/var/lib/postgresql", Role: "postgresql"},
		),
	}
	recoverStaleNetwork(dir)
	restoreDirectoryRoots(dir)
	ctbackup.RecoverOwnedFreezes()
	go ctbackup.WatchOwnedFreezes(nil)
	lxc.EnsureHostKeyringQuota()
	reconcileRuntimeLXC(h.Workloads)
	go scrapeMetrics(ms, dir)
	go watchDisk(h.Disk)
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

func restoreDirectoryRoots(dataDir string) {
	d := storage.Directory{Run: storage.LiveRun}
	_ = d.RestoreLoopMounts(context.Background(), filepath.Join(dataDir, "storage"))
}

func reconcileRuntimeLXC(eng *lxc.Engine) {
	if eng == nil {
		return
	}
	eng.ReconcileRuntimeConfigs()
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

// watchDisk refreshes host disk protection: levels, the gate, and the
// emergency reserve PostgreSQL relies on.
func watchDisk(g *diskguard.Guard) {
	last := diskguard.LevelOK
	note := ""
	for {
		st := g.Refresh()
		if st.Level != last {
			for _, fs := range st.Filesystems {
				log.Printf("disk protection: %s is %s (%.1f%% used, %d bytes free)", fs.Mount, fs.Level, fs.UsedPercent, fs.FreeBytes)
			}
			last = st.Level
		}
		if st.ReserveNote != "" && st.ReserveNote != note {
			log.Printf("disk protection: %s", st.ReserveNote)
		}
		note = st.ReserveNote
		time.Sleep(30 * time.Second)
	}
}
