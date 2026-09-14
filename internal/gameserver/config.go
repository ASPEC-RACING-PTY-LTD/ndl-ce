package gameserver

import (
	"fmt"
	"sort"
	"strings"
)

// PropertiesFile is a lossless key=value document. Unknown lines stay intact.
type PropertiesFile struct {
	Lines []propLine
}

type propLine struct {
	Raw   string
	Key   string
	Value string
	Blank bool
	Note  bool
}

func ParseProperties(raw string) PropertiesFile {
	var f PropertiesFile
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			f.Lines = append(f.Lines, propLine{Raw: line, Blank: true})
			continue
		}
		if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "!") {
			f.Lines = append(f.Lines, propLine{Raw: line, Note: true})
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			f.Lines = append(f.Lines, propLine{Raw: line})
			continue
		}
		f.Lines = append(f.Lines, propLine{Raw: line, Key: strings.TrimSpace(key), Value: val})
	}
	return f
}

func (f PropertiesFile) Get(key string) (string, bool) {
	for _, line := range f.Lines {
		if line.Key == key {
			return line.Value, true
		}
	}
	return "", false
}

func (f *PropertiesFile) Set(key, value string) {
	for i, line := range f.Lines {
		if line.Key == key {
			f.Lines[i].Value = value
			f.Lines[i].Raw = key + "=" + value
			return
		}
	}
	f.Lines = append(f.Lines, propLine{Key: key, Value: value, Raw: key + "=" + value})
}

func (f PropertiesFile) Bytes() string {
	var b strings.Builder
	for i, line := range f.Lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if line.Key != "" {
			b.WriteString(line.Key)
			b.WriteByte('=')
			b.WriteString(line.Value)
			continue
		}
		b.WriteString(line.Raw)
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

type ConfigChange struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	File    string `json:"file,omitempty"`
	Key     string `json:"key,omitempty"`
	Env     string `json:"env,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Restart bool   `json:"restart"`
}

func diffSettings(settings []Setting, before, after map[string]string) []ConfigChange {
	var out []ConfigChange
	seen := map[string]struct{}{}
	for _, s := range settings {
		old := before[s.ID]
		next := after[s.ID]
		if old == next {
			continue
		}
		out = append(out, ConfigChange{ID: s.ID, Label: s.Label, File: s.File, Key: s.Key, Env: s.Env, From: old, To: next, Restart: s.Restart})
		seen[s.ID] = struct{}{}
	}
	keys := make([]string, 0, len(after))
	for k := range after {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}
		if before[k] == after[k] {
			continue
		}
		out = append(out, ConfigChange{ID: k, Label: k, From: before[k], To: after[k]})
	}
	return out
}

func applyFriendly(settings []Setting, values map[string]string, files map[string]string, env map[string]string) (map[string]string, map[string]string, error) {
	nextFiles := map[string]string{}
	for k, v := range files {
		nextFiles[k] = v
	}
	nextEnv := map[string]string{}
	for k, v := range env {
		nextEnv[k] = v
	}
	for _, s := range settings {
		val, ok := values[s.ID]
		if !ok {
			continue
		}
		if s.Kind == "number" && val != "" {
			for _, r := range val {
				if r < '0' || r > '9' {
					if r != '-' {
						return nil, nil, fmt.Errorf("%s must be a number", s.Label)
					}
				}
			}
		}
		if s.File != "" && s.Key != "" {
			cur := ParseProperties(nextFiles[s.File])
			cur.Set(s.Key, val)
			nextFiles[s.File] = cur.Bytes()
		}
		if s.Env != "" {
			nextEnv[s.Env] = val
		}
	}
	return nextFiles, nextEnv, nil
}

func readFriendly(settings []Setting, files map[string]string, env map[string]string) map[string]string {
	out := map[string]string{}
	for _, s := range settings {
		if s.File != "" && s.Key != "" {
			if raw, ok := files[s.File]; ok {
				if v, ok := ParseProperties(raw).Get(s.Key); ok {
					out[s.ID] = v
					continue
				}
			}
		}
		if s.Env != "" {
			if v, ok := env[s.Env]; ok {
				out[s.ID] = v
				continue
			}
		}
		if s.Default != "" {
			out[s.ID] = s.Default
		}
	}
	return out
}
