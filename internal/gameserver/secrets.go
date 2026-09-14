package gameserver

import "strings"

const redacted = "[redacted]"

func redactMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if looksSecretName(k) || looksSecretValue(v) {
			if strings.TrimSpace(v) == "" {
				out[k] = ""
			} else {
				out[k] = redacted
			}
			continue
		}
		out[k] = v
	}
	return out
}

func looksSecretValue(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) >= 24 && !strings.Contains(v, " ") {
		return true
	}
	return false
}

func redactText(s string, secrets []string) string {
	out := s
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" || secret == "true" || secret == "false" || len(secret) < 4 {
			continue
		}
		out = strings.ReplaceAll(out, secret, redacted)
	}
	return out
}

func secretValues(env map[string]string) []string {
	var out []string
	for k, v := range env {
		if looksSecretName(k) && strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
