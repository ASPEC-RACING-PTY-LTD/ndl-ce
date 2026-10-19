package engine

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// Redacted replaces secret values in payloads, outputs and logs.
const Redacted = "[redacted]"

var secretKeyParts = []string{
	"authorization", "password", "passwd", "secret", "token", "api_key", "apikey", "api-key",
	"cookie", "private_key", "credential", "session", "bearer",
}

// SecretKey reports whether a map key looks like it holds a secret.
func SecretKey(k string) bool {
	l := strings.ToLower(k)
	for _, p := range secretKeyParts {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// Redact returns a copy of v with secret-looking keys replaced. Values
// larger than the payload limit become {"_truncated": true}.
func Redact(v any) any {
	out := redact(v, 0)
	raw, err := json.Marshal(out)
	if err == nil && len(raw) > flowdef.MaxPayloadBytes {
		return map[string]any{"_truncated": true, "bytes": len(raw)}
	}
	return out
}

func redact(v any, depth int) any {
	if depth > 32 {
		return "[too deep]"
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			if SecretKey(k) && item != nil && item != "" {
				out[k] = Redacted
				continue
			}
			out[k] = redact(item, depth+1)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, item := range x {
			if SecretKey(k) && item != "" {
				out[k] = Redacted
				continue
			}
			out[k] = RedactText(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redact(item, depth+1)
		}
		return out
	case string:
		return RedactText(x)
	}
	return v
}

var secretInText = regexp.MustCompile(`(?i)\b(authorization|password|passwd|secret|token|api[_-]?key|cookie)(\s*[:=]\s*)("[^"]*"|bearer\s+\S+|basic\s+\S+|\S+)`)
var bearerInText = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)

// RedactText masks secrets written inline, such as "token=abc" or a
// Bearer header copied into a message.
func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = secretInText.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = bearerInText.ReplaceAllString(s, "${1} "+Redacted)
	return s
}

// RedactJSON redacts and encodes v.
func RedactJSON(v any) json.RawMessage {
	raw, err := json.Marshal(Redact(v))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// LogLine redacts and size-limits one log line.
func LogLine(s string) string {
	s = RedactText(s)
	if len(s) > flowdef.MaxLogLineBytes {
		s = s[:flowdef.MaxLogLineBytes] + " [truncated]"
	}
	return s
}
