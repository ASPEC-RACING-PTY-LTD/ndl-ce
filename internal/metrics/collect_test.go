package metrics

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeProc(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFirstCPUScrapeCollecting(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/stat", "cpu  100 0 100 800 0 0 0 0\n")
	writeProc(t, root, "proc/meminfo", "MemTotal: 1024 kB\nMemAvailable: 512 kB\n")
	writeProc(t, root, "proc/net/dev", netDevFixture(100, 200))
	s := openTestStore(t)
	c := &Collector{FSRoot: root, Store: s}
	now := time.Now().UTC().Truncate(time.Second)
	if err := c.Scrape(now); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query([]string{MetricCPUBusyRatio}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricCPUBusyRatio)
	if ser.Status != StatusCollecting {
		t.Fatalf("first cpu status %q", ser.Status)
	}
	if len(ser.Points) != 0 {
		t.Fatalf("first cpu must not invent a point: %+v", ser.Points)
	}
}

func TestSecondCPUScrapeAvailable(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/stat", "cpu  100 0 100 800 0 0 0 0\n")
	writeProc(t, root, "proc/meminfo", "MemTotal: 2048 kB\nMemAvailable: 1024 kB\n")
	writeProc(t, root, "proc/net/dev", netDevFixture(10, 20))
	s := openTestStore(t)
	c := &Collector{FSRoot: root, Store: s}
	t1 := time.Now().UTC().Truncate(time.Second)
	if err := c.Scrape(t1); err != nil {
		t.Fatal(err)
	}
	// user+system +100, idle +50, total +150 => busy = 100/150
	writeProc(t, root, "proc/stat", "cpu  150 0 150 850 0 0 0 0\n")
	t2 := t1.Add(15 * time.Second)
	if err := c.Scrape(t2); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query([]string{MetricCPUBusyRatio}, t1.Add(-time.Minute), t2.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricCPUBusyRatio)
	if ser.Status != StatusAvailable {
		t.Fatalf("second cpu status %q", ser.Status)
	}
	if len(ser.Points) != 1 {
		t.Fatalf("want one cpu point, got %+v", ser.Points)
	}
	want := 1 - float64(50)/float64(150)
	if ser.Points[0].Value < want-1e-9 || ser.Points[0].Value > want+1e-9 {
		t.Fatalf("busy %v want %v", ser.Points[0].Value, want)
	}
	if !ser.Points[0].Time.Equal(t2) {
		t.Fatalf("point time %s", ser.Points[0].Time)
	}
}

func TestScrapeMemoryAndNet(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/stat", "cpu  1 0 1 8 0 0 0 0\n")
	writeProc(t, root, "proc/meminfo", "MemTotal: 16384 kB\nMemAvailable: 4096 kB\n")
	writeProc(t, root, "proc/net/dev", netDevFixture(1000, 2000)+"  eth1: 3000 1 0 0 0 0 0 0 4000 1 0 0 0 0 0 0\n")
	s := openTestStore(t)
	c := &Collector{FSRoot: root, Store: s}
	now := time.Now().UTC().Truncate(time.Second)
	if err := c.Scrape(now); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(
		[]string{MetricMemoryTotalBytes, MetricMemoryAvailBytes, MetricMemoryUsedBytes, MetricNetRxBytes, MetricNetTxBytes},
		now.Add(-time.Minute), now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertPoint(t, res, MetricMemoryTotalBytes, 16384*1024)
	assertPoint(t, res, MetricMemoryAvailBytes, 4096*1024)
	assertPoint(t, res, MetricMemoryUsedBytes, (16384-4096)*1024)
	assertPoint(t, res, MetricNetRxBytes, 4000)
	assertPoint(t, res, MetricNetTxBytes, 6000)
}

func TestDiskstatsAndStorage(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/stat", "cpu  1 0 1 8 0 0 0 0\n")
	writeProc(t, root, "proc/meminfo", "MemTotal: 1024 kB\nMemAvailable: 512 kB\n")
	writeProc(t, root, "proc/net/dev", netDevFixture(1, 2)+"  nvab12: 9 1 0 0 0 0 0 0 8 1 0 0 0 0 0 0\n")
	writeProc(t, root, "proc/diskstats", "   8       0 sda 10 0 20 40 5 0 10 20 0 0 0 0 0 0 0 0\n   7       0 loop0 99 0 99 99 99 0 99 99 0 0 0 0 0 0 0 0\n")
	s := openTestStore(t)
	storeRoot := t.TempDir()
	c := &Collector{FSRoot: root, Store: s, StorageRoot: storeRoot}
	t1 := time.Now().UTC().Truncate(time.Second)
	if err := c.Scrape(t1); err != nil {
		t.Fatal(err)
	}
	writeProc(t, root, "proc/diskstats", "   8       0 sda 20 0 40 80 15 0 30 50 0 0 0 0 0 0 0 0\n")
	t2 := t1.Add(15 * time.Second)
	if err := c.Scrape(t2); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(
		[]string{MetricIOReadBytes, MetricIOWriteBytes, MetricDiskReadLatencyMS, MetricStorageAvailBytes, netIfacePrefix + "nvab12.rx_bytes"},
		t1.Add(-time.Minute), t2.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertPoint(t, res, MetricIOReadBytes, float64((40-20)*512))
	assertPoint(t, res, MetricIOWriteBytes, float64((30-10)*512))
	lat := seriesByName(res, MetricDiskReadLatencyMS)
	if lat.Status != StatusAvailable || len(lat.Points) != 1 {
		t.Fatalf("latency %+v", lat)
	}
	if lat.Points[0].Value != 4 {
		t.Fatalf("read latency %v want 4", lat.Points[0].Value)
	}
	stor := seriesByName(res, MetricStorageAvailBytes)
	if stor.Status != StatusAvailable || len(stor.Points) == 0 {
		t.Fatalf("storage avail must be observed: %+v", stor)
	}
	tap := seriesByName(res, netIfacePrefix+"nvab12.rx_bytes")
	if tap.Status != StatusAvailable || len(tap.Points) == 0 || tap.Points[0].Value != 9 {
		t.Fatalf("tap %+v", tap)
	}
}

func TestMissingProcNoPanic(t *testing.T) {
	s := openTestStore(t)
	c := &Collector{FSRoot: filepath.Join(t.TempDir(), "empty"), Store: s}
	now := time.Now().UTC()
	if err := c.Scrape(now); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(KnownNames, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, ser := range res.Series {
		if ser.Status == StatusAvailable {
			t.Fatalf("missing proc must not be available: %+v", ser)
		}
		if len(ser.Points) != 0 {
			t.Fatalf("missing proc invented points: %+v", ser)
		}
		if ser.Status != StatusCollecting && ser.Status != StatusUnavailable {
			t.Fatalf("missing proc status %q", ser.Status)
		}
	}
	absent, err := s.Query(nil, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(absent.Series) != 0 {
		t.Fatalf("missing proc series should be absent: %+v", absent.Series)
	}
}

func TestCPUIdlePlusIowait(t *testing.T) {
	idle, total, ok := parseStatCPU("cpu  10 2 3 40 5 1 2 1 99 99\ncpu0 1 1 1 1\n")
	if !ok {
		t.Fatal("parse")
	}
	// guest fields ignored; idle=40+5; total=10+2+3+40+5+1+2+1
	if idle != 45 {
		t.Fatalf("idle %d", idle)
	}
	if total != 64 {
		t.Fatalf("total %d", total)
	}
}

func netDevFixture(rx, tx int) string {
	return "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 99999 1 0 0 0 0 0 0 88888 1 0 0 0 0 0 0\n" +
		"  eth0: " + strconv.Itoa(rx) + " 10 0 0 0 0 0 0 " + strconv.Itoa(tx) + " 20 0 0 0 0 0 0\n"
}

func assertPoint(t *testing.T, res QueryResult, name string, want float64) {
	t.Helper()
	ser := seriesByName(res, name)
	if ser.Status != StatusAvailable || len(ser.Points) != 1 {
		t.Fatalf("%s: %+v", name, ser)
	}
	if ser.Points[0].Value != want {
		t.Fatalf("%s value %v want %v", name, ser.Points[0].Value, want)
	}
}
