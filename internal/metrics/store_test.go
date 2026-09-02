package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPersistAndQuery(t *testing.T) {
	s := openTestStore(t)
	ts := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.Record(MetricMemoryUsedBytes, ts, 1024); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(MetricMemoryTotalBytes, ts, 4096); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(
		[]string{MetricMemoryUsedBytes, MetricMemoryTotalBytes},
		ts.Add(-time.Minute),
		ts.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusAvailable {
		t.Fatalf("status %q", res.Status)
	}
	if len(res.Series) != 2 {
		t.Fatalf("series %d", len(res.Series))
	}
	if len(res.Series[0].Points) != 1 || res.Series[0].Points[0].Value != 1024 {
		t.Fatalf("used %+v", res.Series[0].Points)
	}
	if !res.Series[0].Points[0].Time.Equal(ts) {
		t.Fatalf("time %s", res.Series[0].Points[0].Time)
	}
	if res.Series[0].Unit != "bytes" {
		t.Fatalf("unit %q", res.Series[0].Unit)
	}
}

func TestRetentionPruneRemovesOldRows(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Record("keep", now, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("old", now.Add(-13*24*time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(now.Add(2 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	keep, err := s.Query([]string{"keep"}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := seriesByName(keep, "keep"); len(got.Points) != 1 {
		t.Fatalf("keep should remain: %+v", got)
	}
	oldRes, err := s.Query([]string{"old"}, now.Add(-13*24*time.Hour).Add(-time.Hour), now.Add(-13*24*time.Hour).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := seriesByName(oldRes, "old"); len(got.Points) != 0 {
		t.Fatalf("old should be pruned: %+v", got)
	}
}

func TestEmptyDBQuery(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	res, err := s.Query(nil, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCollecting && res.Status != StatusUnavailable {
		t.Fatalf("empty status %q", res.Status)
	}
	if len(res.Series) != 0 {
		t.Fatalf("empty series %+v", res.Series)
	}
	for _, ser := range res.Series {
		if len(ser.Points) != 0 {
			t.Fatalf("invented points %+v", ser.Points)
		}
	}

	res, err = s.Query(KnownNames, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCollecting && res.Status != StatusUnavailable {
		t.Fatalf("named empty status %q", res.Status)
	}
	for _, ser := range res.Series {
		if len(ser.Points) != 0 {
			t.Fatalf("named invented points %+v", ser)
		}
		if ser.Status == StatusAvailable {
			t.Fatalf("empty series must not be available: %+v", ser)
		}
	}
}

func TestNoFakeLineFromOneSample(t *testing.T) {
	s := openTestStore(t)
	ts := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.Record(MetricCPUBusyRatio, ts, 0.42); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query([]string{MetricCPUBusyRatio}, ts.Add(-time.Hour), ts.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricCPUBusyRatio)
	if len(ser.Points) != 1 {
		t.Fatalf("want one real row, got %+v", ser.Points)
	}
	if ser.Points[0].Value != 0.42 {
		t.Fatalf("value %v", ser.Points[0].Value)
	}
}

func TestRestartPreservesData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.db")
	ts := time.Now().UTC().Truncate(time.Millisecond)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(MetricNetRxBytes, ts, 99); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	res, err := s2.Query([]string{MetricNetRxBytes}, ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricNetRxBytes)
	if len(ser.Points) != 1 || ser.Points[0].Value != 99 {
		t.Fatalf("lost after reopen: %+v", ser)
	}
}

func TestStaleSeriesKeepsRealPoints(t *testing.T) {
	s := openTestStore(t)
	old := time.Now().UTC().Add(-10 * time.Minute)
	if err := s.Record(MetricMemoryUsedBytes, old, 123); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query([]string{MetricMemoryUsedBytes}, old.Add(-time.Minute), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricMemoryUsedBytes)
	if ser.Status != StatusStale {
		t.Fatalf("status=%s", ser.Status)
	}
	if len(ser.Points) != 1 || ser.Points[0].Value != 123 {
		t.Fatalf("stale must keep the real point: %+v", ser)
	}
}

func TestQueryKnownNamesIncludesCollectingCPU(t *testing.T) {
	s := openTestStore(t)
	ts := time.Now().UTC()
	if err := s.Record(MetricMemoryTotalBytes, ts, 4096); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(KnownNames, ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cpu := seriesByName(res, MetricCPUBusyRatio)
	if cpu.Status != StatusCollecting || len(cpu.Points) != 0 {
		t.Fatalf("cpu should be collecting with no fake point: %+v", cpu)
	}
}

func TestPruneCapsGrowth(t *testing.T) {
	s := openTestStore(t)
	s.maxRows = 8
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		if err := s.Record("cap", now.Add(time.Duration(i)*time.Second), float64(i)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Query([]string{"cap"}, now.Add(-time.Minute), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, "cap")
	if len(ser.Points) > 8 {
		t.Fatalf("uncontrolled growth: %d points", len(ser.Points))
	}
	if len(ser.Points) != 8 {
		t.Fatalf("want 8 newest, got %d", len(ser.Points))
	}
	if ser.Points[0].Value != 12 || ser.Points[7].Value != 19 {
		t.Fatalf("expected oldest dropped: %+v", ser.Points)
	}
}

func TestHourlyDownsampleDoesNotInventBuckets(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 9, 1, 0, 15, 0, 0, time.UTC)
	if err := s.Record(MetricCPUBusyRatio, base, 0.2); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(MetricCPUBusyRatio, base.Add(20*time.Minute), 0.4); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(MetricCPUBusyRatio, base.Add(2*time.Hour), 0.8); err != nil {
		t.Fatal(err)
	}
	res, err := s.Query([]string{MetricCPUBusyRatio}, base.Add(-time.Hour), base.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ser := seriesByName(res, MetricCPUBusyRatio)
	if len(ser.Points) != 2 {
		t.Fatalf("hourly must not fill missing hours: %+v", ser.Points)
	}
	if ser.Points[0].Value < 0.29 || ser.Points[0].Value > 0.31 {
		t.Fatalf("hour0 avg %v", ser.Points[0].Value)
	}
	if ser.Points[1].Value != 0.8 {
		t.Fatalf("hour2 %v", ser.Points[1].Value)
	}
}

func TestQueryWindowPadsKnownNames(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	res, err := s.QueryWindow(now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	cpu := seriesByName(res, MetricCPUBusyRatio)
	if cpu.Status != StatusCollecting || len(cpu.Points) != 0 {
		t.Fatalf("empty window must not fake zeros: %+v", cpu)
	}
}

func seriesByName(res QueryResult, name string) Series {
	for _, s := range res.Series {
		if s.Name == name {
			return s
		}
	}
	return Series{Name: name, Points: []Point{}}
}
