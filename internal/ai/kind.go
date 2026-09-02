package ai

import (
	"fmt"
	"strings"
)

const (
	KindOpenAI     = "openai"
	KindAnthropic  = "anthropic"
	KindGemini     = "gemini"
	KindOllama     = "ollama"
	KindLocal      = "local"
	KindCompatible = "openai_compatible"
	KindPrivate    = "private"
	ModeAsk        = "ask"
	ModeOperate    = "operate"
	GrantEvents    = "events.read"
	GrantMetrics   = "metrics.read"
	ApproveConfirm = "approve-plan"
	ActorTypeAI    = "ai"
)

var kinds = []string{KindOpenAI, KindAnthropic, KindGemini, KindOllama, KindLocal, KindCompatible, KindPrivate}

// NormalizeKind accepts BYO provider names. Unknown kinds fail closed.
func NormalizeKind(kind string) (string, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		k = KindLocal
	}
	for _, known := range kinds {
		if k == known {
			return k, nil
		}
	}
	return "", fmt.Errorf("provider kind is unsupported")
}

// NormalizeMode is Ask-only in this phase. Plan/Operate are Phase 42.
func NormalizeMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = ModeAsk
	}
	if m != ModeAsk && m != ModeOperate {
		return "", fmt.Errorf("profile mode is unsupported")
	}
	return m, nil
}

// DefaultAskGrants is the read-only Ask profile.
func DefaultAskGrants() []string {
	return []string{GrantEvents, GrantMetrics}
}

// CanQuery is true only when the profile may retrieve events and metrics.
func CanQuery(grants []string) bool {
	haveEvents, haveMetrics := false, false
	for _, g := range grants {
		switch strings.TrimSpace(g) {
		case GrantEvents:
			haveEvents = true
		case GrantMetrics:
			haveMetrics = true
		}
	}
	return haveEvents && haveMetrics
}

// ForbidsMutatePlans is true for Ask profiles. Operate may create mutate plans.
func ForbidsMutatePlans(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), ModeAsk)
}

func KnownKind(kind string) bool {
	_, err := NormalizeKind(kind)
	return err == nil
}
