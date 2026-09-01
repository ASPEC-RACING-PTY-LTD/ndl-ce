package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndnet"
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
	h := &agentrpc.Handler{Ident: identity.Files{Dir: dir}, Metrics: ms}
	recoverStaleNetwork(dir)
	go scrapeMetrics(ms)
	go h.RefreshLoop(30 * time.Second)
	if err := agentrpc.Serve(h); err != nil {
		log.Fatal(err)
	}
}

func recoverStaleNetwork(dataDir string) {
	eng := &ndnet.Engine{StateDir: filepath.Join(dataDir, "net")}
	_ = eng.RecoverStale(time.Now().UTC())
}

func scrapeMetrics(ms *metrics.Store) {
	col := &metrics.Collector{FSRoot: "/", Store: ms}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	_ = col.Scrape(time.Now().UTC())
	for range t.C {
		_ = col.Scrape(time.Now().UTC())
	}
}
