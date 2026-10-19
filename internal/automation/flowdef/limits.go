package flowdef

import "time"

// Fixed engine-wide limits. Per-flow limits below are clamped to these.
const (
	MaxNodes             = 200
	MaxRetries           = 8
	MaxPayloadBytes      = 1 << 20
	MaxLogLineBytes      = 32 << 10
	MaxWebhookBodyBytes  = 1 << 20
	KeepWebhookDelivery  = 100
	IdempotencyWindow    = 24 * time.Hour
	MaxRunningFlows      = 8
	MaxParallelSteps     = 8
	MaxChainDepth        = 3
	MaxConditionDepth    = 3
	DestructivePerTarget = 3
	DestructiveWindow    = 15 * time.Minute
)

// Limits are the per-flow safety limits, editable in the flow's settings.
type Limits struct {
	MaxLoopItems     int `json:"max_loop_items"`
	MaxSteps         int `json:"max_steps"`
	StepTimeoutSec   int `json:"step_timeout_sec"`
	RunTimeoutSec    int `json:"run_timeout_sec"`
	Concurrency      int `json:"concurrency"`
	QueueDepth       int `json:"queue_depth"`
	PerMinute        int `json:"per_minute"`
	PerHour          int `json:"per_hour"`
	DebounceSec      int `json:"debounce_sec"`
	MaxParallel      int `json:"max_parallel"`
	BreakerThreshold int `json:"breaker_threshold"`
}

type bound struct{ def, min, max int }

var limitBounds = map[string]bound{
	"max_loop_items":    {100, 1, 1000},
	"max_steps":         {500, 1, 5000},
	"step_timeout_sec":  {300, 1, 3600},
	"run_timeout_sec":   {3600, 10, 86400},
	"concurrency":       {1, 1, 8},
	"queue_depth":       {10, 0, 100},
	"per_minute":        {6, 1, 60},
	"per_hour":          {60, 1, 600},
	"debounce_sec":      {30, 0, 3600},
	"max_parallel":      {MaxParallelSteps, 1, MaxParallelSteps},
	"breaker_threshold": {5, 1, 50},
}

// LimitCaps describes every limit's default and hard cap for the editor.
func LimitCaps() map[string]map[string]int {
	out := map[string]map[string]int{}
	for k, b := range limitBounds {
		out[k] = map[string]int{"default": b.def, "min": b.min, "max": b.max}
	}
	return out
}

func clamp(v int, b bound) int {
	if v == 0 {
		return b.def
	}
	if v < b.min {
		return b.min
	}
	if v > b.max {
		return b.max
	}
	return v
}

// Normalized fills defaults and clamps every limit to its hard cap.
func (l Limits) Normalized() Limits {
	l.MaxLoopItems = clamp(l.MaxLoopItems, limitBounds["max_loop_items"])
	l.MaxSteps = clamp(l.MaxSteps, limitBounds["max_steps"])
	l.StepTimeoutSec = clamp(l.StepTimeoutSec, limitBounds["step_timeout_sec"])
	l.RunTimeoutSec = clamp(l.RunTimeoutSec, limitBounds["run_timeout_sec"])
	l.Concurrency = clamp(l.Concurrency, limitBounds["concurrency"])
	if l.QueueDepth < 0 {
		l.QueueDepth = 0
	} else {
		l.QueueDepth = clamp(l.QueueDepth, limitBounds["queue_depth"])
	}
	l.PerMinute = clamp(l.PerMinute, limitBounds["per_minute"])
	l.PerHour = clamp(l.PerHour, limitBounds["per_hour"])
	if l.DebounceSec < 0 {
		l.DebounceSec = 0
	} else {
		l.DebounceSec = clamp(l.DebounceSec, limitBounds["debounce_sec"])
	}
	l.MaxParallel = clamp(l.MaxParallel, limitBounds["max_parallel"])
	l.BreakerThreshold = clamp(l.BreakerThreshold, limitBounds["breaker_threshold"])
	return l
}

// DefaultLimits are the limits of a new flow.
func DefaultLimits() Limits { return Limits{}.Normalized() }
