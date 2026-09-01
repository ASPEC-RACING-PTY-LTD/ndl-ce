package metrics

import "time"

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
	MetricCPUBusyRatio     = "cpu.busy_ratio"
	MetricMemoryUsedBytes  = "memory.used_bytes"
	MetricMemoryTotalBytes = "memory.total_bytes"
	MetricMemoryAvailBytes = "memory.available_bytes"
	MetricNetRxBytes       = "net.rx_bytes"
	MetricNetTxBytes       = "net.tx_bytes"
)

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

// KnownNames is the Phase 2 host scrape set.
var KnownNames = []string{
	MetricCPUBusyRatio,
	MetricMemoryUsedBytes,
	MetricMemoryTotalBytes,
	MetricMemoryAvailBytes,
	MetricNetRxBytes,
	MetricNetTxBytes,
}

func unitFor(name string) string {
	switch name {
	case MetricCPUBusyRatio:
		return "ratio"
	case MetricMemoryUsedBytes, MetricMemoryTotalBytes, MetricMemoryAvailBytes,
		MetricNetRxBytes, MetricNetTxBytes:
		return "bytes"
	default:
		return ""
	}
}

func knownName(name string) bool {
	switch name {
	case MetricCPUBusyRatio, MetricMemoryUsedBytes, MetricMemoryTotalBytes,
		MetricMemoryAvailBytes, MetricNetRxBytes, MetricNetTxBytes:
		return true
	default:
		return false
	}
}
