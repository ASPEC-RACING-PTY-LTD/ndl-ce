package metrics

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Collector scrapes host gauges from a live root or a fixture tree.
type Collector struct {
	FSRoot      string // fixture or "/"
	Store       *Store
	StorageRoot string // Statfs target; empty skips storage.avail_bytes

	mu      sync.Mutex
	prevCPU *cpuSnap
	prevIO  *ioSnap
}

type cpuSnap struct {
	idle  uint64
	total uint64
}

type ioSnap struct {
	readSectors  uint64
	writeSectors uint64
	readTicks    uint64
	writeTicks   uint64
	reads        uint64
	writes       uint64
}

// Scrape reads proc files and records only values that were observed.
func (c *Collector) Scrape(now time.Time) error {
	if c == nil || c.Store == nil {
		return errors.New("metrics: collector store is nil")
	}
	now = now.UTC()
	var first error
	record := func(name string, value float64) {
		if err := c.Store.Record(name, now, value); err != nil && first == nil {
			first = err
		}
	}

	if raw, err := os.ReadFile(c.procPath("proc/stat")); err == nil {
		if idle, total, ok := parseStatCPU(string(raw)); ok {
			c.mu.Lock()
			prev := c.prevCPU
			c.prevCPU = &cpuSnap{idle: idle, total: total}
			c.mu.Unlock()
			if prev != nil {
				dIdle := idle - prev.idle
				dTotal := total - prev.total
				if dTotal > 0 {
					busy := 1 - float64(dIdle)/float64(dTotal)
					if busy < 0 {
						busy = 0
					}
					if busy > 1 {
						busy = 1
					}
					record(MetricCPUBusyRatio, busy)
				}
			}
		}
	}

	if raw, err := os.ReadFile(c.procPath("proc/meminfo")); err == nil {
		total, avail, hasTotal, hasAvail := parseMeminfo(string(raw))
		if hasTotal {
			record(MetricMemoryTotalBytes, float64(total))
		}
		if hasAvail {
			record(MetricMemoryAvailBytes, float64(avail))
		}
		if hasTotal && hasAvail {
			used := total - avail
			if avail > total {
				used = 0
			}
			record(MetricMemoryUsedBytes, float64(used))
		}
	}

	if raw, err := os.ReadFile(c.procPath("proc/net/dev")); err == nil {
		if rx, tx, ok := parseNetDev(string(raw)); ok {
			record(MetricNetRxBytes, float64(rx))
			record(MetricNetTxBytes, float64(tx))
		}
		for _, iface := range parseNetDevIfaces(string(raw)) {
			record(netIfacePrefix+iface.Name+".rx_bytes", float64(iface.Rx))
			record(netIfacePrefix+iface.Name+".tx_bytes", float64(iface.Tx))
		}
	}

	if raw, err := os.ReadFile(c.procPath("proc/diskstats")); err == nil {
		if snap, ok := parseDiskstats(string(raw)); ok {
			c.mu.Lock()
			prev := c.prevIO
			c.prevIO = &snap
			c.mu.Unlock()
			if prev != nil {
				dRead := snap.readSectors - prev.readSectors
				dWrite := snap.writeSectors - prev.writeSectors
				record(MetricIOReadBytes, float64(dRead*512))
				record(MetricIOWriteBytes, float64(dWrite*512))
				if snap.reads > prev.reads {
					record(MetricDiskReadLatencyMS, float64(snap.readTicks-prev.readTicks)/float64(snap.reads-prev.reads))
				}
				if snap.writes > prev.writes {
					record(MetricDiskWriteLatencyMS, float64(snap.writeTicks-prev.writeTicks)/float64(snap.writes-prev.writes))
				}
			}
		}
	}

	if root := strings.TrimSpace(c.StorageRoot); root != "" {
		if avail, ok := statfsAvail(root); ok {
			record(MetricStorageAvailBytes, float64(avail))
		}
	}
	return first
}

func (c *Collector) procPath(rel string) string {
	root := c.FSRoot
	rel = strings.TrimPrefix(rel, "/")
	if root == "" || root == "/" {
		return "/" + rel
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// parseStatCPU reads the aggregate cpu line. idle is idle+iowait.
func parseStatCPU(raw string) (idle, total uint64, ok bool) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return 0, 0, false
		}
		var user, nice, system, idleJ, iowait, irq, softirq, steal uint64
		var err error
		if user, err = parseUint(fields[1]); err != nil {
			return 0, 0, false
		}
		if nice, err = parseUint(fields[2]); err != nil {
			return 0, 0, false
		}
		if system, err = parseUint(fields[3]); err != nil {
			return 0, 0, false
		}
		if idleJ, err = parseUint(fields[4]); err != nil {
			return 0, 0, false
		}
		if len(fields) > 5 {
			if iowait, err = parseUint(fields[5]); err != nil {
				return 0, 0, false
			}
		}
		if len(fields) > 6 {
			if irq, err = parseUint(fields[6]); err != nil {
				return 0, 0, false
			}
		}
		if len(fields) > 7 {
			if softirq, err = parseUint(fields[7]); err != nil {
				return 0, 0, false
			}
		}
		if len(fields) > 8 {
			if steal, err = parseUint(fields[8]); err != nil {
				return 0, 0, false
			}
		}
		idle = idleJ + iowait
		total = user + nice + system + idleJ + iowait + irq + softirq + steal
		if total == 0 {
			return 0, 0, false
		}
		return idle, total, true
	}
	return 0, 0, false
}

func parseMeminfo(raw string) (total, avail uint64, hasTotal, hasAvail bool) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := parseUint(fields[0])
		if err != nil {
			continue
		}
		bytes := kb * 1024
		switch key {
		case "MemTotal":
			total = bytes
			hasTotal = true
		case "MemAvailable":
			avail = bytes
			hasAvail = true
		}
	}
	return total, avail, hasTotal, hasAvail
}

func parseNetDev(raw string) (rx, tx uint64, ok bool) {
	ifaces := parseNetDevIfaces(raw)
	if len(ifaces) == 0 {
		return 0, 0, false
	}
	for _, iface := range ifaces {
		rx += iface.Rx
		tx += iface.Tx
	}
	return rx, tx, true
}

type netIface struct {
	Name string
	Rx   uint64
	Tx   uint64
}

func parseNetDevIfaces(raw string) []netIface {
	var out []netIface
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		iface, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		iface = sanitizeIface(strings.TrimSpace(iface))
		if iface == "" || iface == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, err1 := parseUint(fields[0])
		t, err2 := parseUint(fields[8])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, netIface{Name: iface, Rx: r, Tx: t})
	}
	return out
}

func sanitizeIface(name string) string {
	if len(name) > 16 {
		name = name[:16]
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseDiskstats(raw string) (ioSnap, bool) {
	var snap ioSnap
	ok := false
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		if skipDisk(name) {
			continue
		}
		reads, err1 := parseUint(fields[3])
		readSectors, err2 := parseUint(fields[5])
		readTicks, err3 := parseUint(fields[6])
		writes, err4 := parseUint(fields[7])
		writeSectors, err5 := parseUint(fields[9])
		writeTicks, err6 := parseUint(fields[10])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
			continue
		}
		snap.reads += reads
		snap.readSectors += readSectors
		snap.readTicks += readTicks
		snap.writes += writes
		snap.writeSectors += writeSectors
		snap.writeTicks += writeTicks
		ok = true
	}
	return snap, ok
}

func skipDisk(name string) bool {
	switch {
	case strings.HasPrefix(name, "loop"), strings.HasPrefix(name, "ram"), strings.HasPrefix(name, "sr"):
		return true
	default:
		return false
	}
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}
