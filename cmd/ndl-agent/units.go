package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// orphanKind is one No-dal template unit and the state file an instance
// needs. An enabled instance without that file can never start: its network
// or workload was deleted while the unit stayed enabled, and it fails on
// every boot.
type orphanKind struct {
	prefix string
	// guard must exist before any instance is judged; a missing data
	// directory (late mount) must not disable live workloads.
	guard string
	state func(id string) string
	stop  bool
}

func orphanKinds(dataDir string) []orphanKind {
	net := filepath.Join(dataDir, "net")
	wl := filepath.Join(dataDir, "workloads")
	applied := func(id string) string { return filepath.Join(wl, id, "last-applied.json") }
	return []orphanKind{
		{prefix: "ndl-dnsmasq@", guard: net, stop: true, state: func(id string) string { return filepath.Join(net, "dnsmasq", id+".conf") }},
		{prefix: "ndl-nat@", guard: net, stop: true, state: func(id string) string { return filepath.Join(net, "nft", id+".nft") }},
		{prefix: "nodal-ct@", guard: wl, state: applied},
		{prefix: "nodal-oci@", guard: wl, state: applied},
	}
}

// findOrphanUnits lists enabled No-dal instances whose state file is gone.
func findOrphanUnits(unitDir, dataDir string) map[string]bool {
	out := map[string]bool{}
	wants, _ := filepath.Glob(filepath.Join(unitDir, "*.wants", "*@*.service"))
	kinds := orphanKinds(dataDir)
	for _, link := range wants {
		unit := filepath.Base(link)
		for _, k := range kinds {
			if !strings.HasPrefix(unit, k.prefix) {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(unit, k.prefix), ".service")
			if _, err := uuid.Parse(id); err != nil {
				break
			}
			if fi, err := os.Stat(k.guard); err != nil || !fi.IsDir() {
				break
			}
			if _, err := os.Stat(k.state(id)); os.IsNotExist(err) {
				out[unit] = k.stop
			}
			break
		}
	}
	return out
}

// disableOrphanUnits disables enabled instances that can never start.
func disableOrphanUnits(dataDir string) {
	orphans := findOrphanUnits("/etc/systemd/system", dataDir)
	if len(orphans) == 0 {
		return
	}
	units := make([]string, 0, len(orphans))
	for u := range orphans {
		units = append(units, u)
	}
	sort.Strings(units)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, unit := range units {
		args := []string{"disable", unit}
		if orphans[unit] {
			args = []string{"disable", "--now", unit}
		}
		if out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", args...).CombinedOutput(); err != nil {
			log.Printf("orphan unit %s: disable: %v: %s", unit, err, strings.TrimSpace(string(out)))
			continue
		}
		_ = exec.CommandContext(ctx, "/usr/bin/systemctl", "reset-failed", unit).Run()
		log.Printf("orphan unit %s: disabled; its network or workload state no longer exists", unit)
	}
}
