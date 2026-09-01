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
	FSRoot string // fixture or "/"
	Store  *Store

	mu      sync.Mutex
	prevCPU *cpuSnap
}

type cpuSnap struct {
	idle  uint64
	total uint64
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
	parsed := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		iface, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		iface = strings.TrimSpace(iface)
		if iface == "" || iface == "lo" || strings.Contains(iface, "|") {
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
		rx += r
		tx += t
		parsed = true
	}
	return rx, tx, parsed
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}
