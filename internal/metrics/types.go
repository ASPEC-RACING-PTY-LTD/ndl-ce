package metrics

import (
	"strings"
	"time"
)

// Status is an honest series state. Never invent a zero sample.
type Status string

const (
	StatusAvailable   Status = "available"
	StatusCollecting  Status = "collecting"
	StatusUnavailable Status = "unavailable"
	StatusStale       Status = "stale"
)

// DefaultPath is the agent-side SQLite file on a supported host.
const DefaultPath = "/var/lib/ndl/agent/metrics.db"

const (
	// Retention is how long raw samples are kept.
	Retention = 14 * 24 * time.Hour
	// DefaultMaxRows keeps 14 days of 15s scrapes for the Phase 2 series set.
	DefaultMaxRows = 500000
	// StaleAfter marks a live query stale when the last point is older.
	StaleAfter = 2 * time.Minute
)

const (
	MetricCPUBusyRatio       = "cpu.busy_ratio"
	MetricMemoryUsedBytes    = "memory.used_bytes"
	MetricMemoryTotalBytes   = "memory.total_bytes"
	MetricMemoryAvailBytes   = "memory.available_bytes"
	MetricNetRxBytes         = "net.rx_bytes"
	MetricNetTxBytes         = "net.tx_bytes"
	MetricIOReadBytes        = "io.read_bytes"
	MetricIOWriteBytes       = "io.write_bytes"
	MetricDiskReadLatencyMS  = "disk.read_latency_ms"
	MetricDiskWriteLatencyMS = "disk.write_latency_ms"
	MetricStorageAvailBytes  = "storage.avail_bytes"
)

const (
	// DownsampleAfter switches Query to hourly averages for longer windows.
	DownsampleAfter = 6 * time.Hour
	// HourlyRetention is how long downsampled buckets are kept.
	HourlyRetention = 90 * 24 * time.Hour
)

const netIfacePrefix = "net.iface."

// Point is one real sample. Queries never synthesize extra points.
type Point struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

// Series is one named metric over a query window.
type Series struct {
	Name   string  `json:"name"`
	Status Status  `json:"status"`
	Unit   string  `json:"unit,omitempty"`
	Points []Point `json:"points"`
}

// QueryResult is the store response for a time range.
type QueryResult struct {
	Status Status   `json:"status"`
	Series []Series `json:"series"`
}

// KnownNames is the host scrape set. Per-interface series are extra names.
var KnownNames = []string{
	MetricCPUBusyRatio,
	MetricMemoryUsedBytes,
	MetricMemoryTotalBytes,
	MetricMemoryAvailBytes,
	MetricNetRxBytes,
	MetricNetTxBytes,
	MetricIOReadBytes,
	MetricIOWriteBytes,
	MetricDiskReadLatencyMS,
	MetricDiskWriteLatencyMS,
	MetricStorageAvailBytes,
}

func unitFor(name string) string {
	switch name {
	case MetricCPUBusyRatio:
		return "ratio"
	case MetricDiskReadLatencyMS, MetricDiskWriteLatencyMS:
		return "milliseconds"
	case MetricMemoryUsedBytes, MetricMemoryTotalBytes, MetricMemoryAvailBytes,
		MetricNetRxBytes, MetricNetTxBytes, MetricIOReadBytes, MetricIOWriteBytes,
		MetricStorageAvailBytes:
		return "bytes"
	default:
		if strings.HasPrefix(name, netIfacePrefix) {
			return "bytes"
		}
		return ""
	}
}

func Alertable(name string) bool {
	for _, n := range KnownNames {
		if n == name {
			return true
		}
	}
	return false
}

func knownName(name string) bool {
	switch name {
	case MetricCPUBusyRatio, MetricMemoryUsedBytes, MetricMemoryTotalBytes,
		MetricMemoryAvailBytes, MetricNetRxBytes, MetricNetTxBytes,
		MetricIOReadBytes, MetricIOWriteBytes, MetricDiskReadLatencyMS,
		MetricDiskWriteLatencyMS, MetricStorageAvailBytes:
		return true
	default:
		return strings.HasPrefix(name, netIfacePrefix)
	}
}
