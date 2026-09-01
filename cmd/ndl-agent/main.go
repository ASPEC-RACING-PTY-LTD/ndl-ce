package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/metrics"
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
	go scrapeMetrics(ms)
	go h.RefreshLoop(30 * time.Second)
	if err := agentrpc.Serve(h); err != nil {
		log.Fatal(err)
	}
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
