package gameserver

import (
	"fmt"
	"strings"
)

// ParserConsoleCfg marks a config file as a Source/Quake-style console
// script (srcds and FiveM server.cfg): one command per line, written
// `name value` or `name "value"`, with // comments.
const ParserConsoleCfg = "cfg"

// ConsoleCfg is a console script that keeps every line it does not change:
// comments, exec lines and unknown commands stay exactly as written.
type ConsoleCfg struct {
	Lines []cfgLine
}

type cfgLine struct {
	Raw     string
	Indent  string
	Key     string
	Value   string
	Gap     string // between the value and the comment
	Comment string // from "//" on, empty when none
}

// ParseConsoleCfg reads a console script. A line holding more than one
// command (a ; outside quotes) is kept but never read or changed.
func ParseConsoleCfg(raw string) ConsoleCfg {
	var f ConsoleCfg
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		f.Lines = append(f.Lines, parseCfgLine(line))
	}
	return f
}

func parseCfgLine(line string) cfgLine {
	l := cfgLine{Raw: line}
	body := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(body)]
	if body == "" || strings.HasPrefix(body, "//") {
		return l
	}
	// Split off a trailing comment, ignoring // inside quotes (URLs).
	code, comment := body, ""
	inQuote := false
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] == '"':
			inQuote = !inQuote
		case !inQuote && body[i] == ';':
			return l
		case !inQuote && strings.HasPrefix(body[i:], "//"):
			code, comment = body[:i], body[i:]
			i = len(body)
		}
	}
	trimmed := strings.TrimRight(code, " \t")
	gap := code[len(trimmed):]
	key, rest, _ := strings.Cut(trimmed, " ")
	if k2, r2, ok := strings.Cut(trimmed, "\t"); ok && len(k2) < len(key) {
		key, rest = k2, r2
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return l
		}
		rest = rest[1 : end+1]
	}
	l.Indent, l.Key, l.Value, l.Gap, l.Comment = indent, key, rest, gap, comment
	return l
}

// Get returns a command's value. Commands are case-insensitive and the last
// one wins, as when the server runs the file.
func (f ConsoleCfg) Get(key string) (string, bool) {
	for i := len(f.Lines) - 1; i >= 0; i-- {
		if f.Lines[i].Key != "" && strings.EqualFold(f.Lines[i].Key, key) {
			return f.Lines[i].Value, true
		}
	}
	return "", false
}

// Set changes the command that takes effect, keeping its comment, or
// appends it. quote writes the value in double quotes.
func (f *ConsoleCfg) Set(key, value string, quote bool) {
	v := value
	if quote {
		v = `"` + value + `"`
	}
	for i := len(f.Lines) - 1; i >= 0; i-- {
		l := &f.Lines[i]
		if l.Key != "" && strings.EqualFold(l.Key, key) {
			l.Value = value
			gap := l.Gap
			if l.Comment != "" && gap == "" {
				gap = " "
			}
			l.Raw = l.Indent + l.Key + " " + v + gap + l.Comment
			return
		}
	}
	f.Lines = append(f.Lines, cfgLine{Raw: key + " " + v, Key: key, Value: value})
}

func (f ConsoleCfg) Bytes() string {
	var b strings.Builder
	for _, l := range f.Lines {
		b.WriteString(l.Raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// checkConsoleValue refuses what would end the value early or start
// another command in a console script.
func checkConsoleValue(label, v string) error {
	if strings.ContainsAny(v, "\"\n\r") {
		return fmt.Errorf("%s cannot contain double quotes or line breaks", label)
	}
	return nil
}
