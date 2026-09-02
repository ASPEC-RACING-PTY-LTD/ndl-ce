package ai

import (
	"regexp"
	"strings"
)

var (
	bearerRe = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]+`)
	skRe     = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`)
	keyEqRe  = regexp.MustCompile(`(?i)(api[_-]?key|password|token|secret|cephx(?:_key)?)\s*[:=]\s*\S+`)
	pemRe    = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
)

const redacted = "[redacted]"

// Redact removes credential-shaped material from prompts, logs, and citations.
func Redact(s string) string {
	if s == "" {
		return s
	}
	out := pemRe.ReplaceAllString(s, redacted)
	out = bearerRe.ReplaceAllString(out, redacted)
	out = skRe.ReplaceAllString(out, redacted)
	out = keyEqRe.ReplaceAllString(out, redacted)
	return out
}

// ContainsSecret reports leftover credential-shaped text after redact.
func ContainsSecret(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "sk-") && skRe.MatchString(s) {
		return true
	}
	if strings.Contains(lower, "begin ") && strings.Contains(lower, "private key") {
		return true
	}
	return false
}
