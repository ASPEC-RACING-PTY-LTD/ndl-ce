package deploy

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// markerByte starts a line the script prints for the agent. It never
// reaches the log.
const markerByte = 0x1e

// Wrapper is the argv tail that runs a script read from stdin. The script
// is read whole first so the commands get /dev/null, not the rest of the
// script, as their input, and no secret is ever on a command line.
var Wrapper = []string{"/bin/bash", "-c", `ndl_script=$(cat); exec </dev/null; eval "$ndl_script"`}

// Script is the bash program for a run. Each command runs in its own bash,
// so a syntax error, exit or cd in one stays in that command.
func Script(s Spec) string {
	var b strings.Builder
	b.WriteString("export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	b.WriteString(`ndl_me=$(id -un 2>/dev/null || echo root)` + "\n")
	b.WriteString(`[ -n "$HOME" ] || HOME=$(getent passwd "$ndl_me" 2>/dev/null | cut -d: -f6)` + "\n")
	b.WriteString(`export HOME="${HOME:-/}" USER="$ndl_me" LOGNAME="$ndl_me"` + "\n")
	// Nothing may stop to ask for a password: there is no one to answer.
	b.WriteString("export GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never DEBIAN_FRONTEND=noninteractive\n")
	b.WriteString(`export BUILDKIT_PROGRESS="${BUILDKIT_PROGRESS:-plain}"` + "\n")
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, Quote(s.Env[k]))
	}
	b.WriteString(`printf '\036NDL:pid:%d\n' "$$"` + "\n")
	fmt.Fprintf(&b, "cd -- %s || { printf '\\036NDL:exit:%%d\\n' 2; exit 2; }\n", Quote(s.Dir))
	b.WriteString("ndl_rc=0\n")
	for i, c := range s.Commands {
		n := i + 1
		fmt.Fprintf(&b, "printf '\\036NDL:step:%d:start\\n'\n", n)
		fmt.Fprintf(&b, "/bin/bash -c %s </dev/null\n", Quote(c.Run))
		b.WriteString("ndl_s=$?\n")
		fmt.Fprintf(&b, "printf '\\036NDL:step:%d:end:%%d\\n' \"$ndl_s\"\n", n)
		if !c.ContinueOnError {
			b.WriteString(`if [ "$ndl_s" -ne 0 ]; then printf '\036NDL:exit:%d\n' "$ndl_s"; exit "$ndl_s"; fi` + "\n")
		}
	}
	b.WriteString(`printf '\036NDL:exit:%d\n' "$ndl_rc"` + "\n")
	b.WriteString(`exit "$ndl_rc"` + "\n")
	return b.String()
}

// Marker is one line the script printed for the agent.
type Marker struct {
	Kind  string // pid, step, exit
	Step  int
	Phase string // start or end, for step
	Code  int
}

// ParseMarker reads "NDL:..." without its leading byte or newline.
func ParseMarker(line string) (Marker, bool) {
	f := strings.Split(strings.TrimSpace(line), ":")
	if len(f) < 3 || f[0] != "NDL" {
		return Marker{}, false
	}
	num := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	switch f[1] {
	case "pid", "exit":
		n, ok := num(f[2])
		return Marker{Kind: f[1], Code: n}, ok
	case "step":
		n, ok := num(f[2])
		if !ok || len(f) < 4 {
			return Marker{}, false
		}
		m := Marker{Kind: "step", Step: n, Phase: f[3]}
		if f[3] == "end" {
			if len(f) < 5 {
				return Marker{}, false
			}
			if m.Code, ok = num(f[4]); !ok {
				return Marker{}, false
			}
		}
		return m, f[3] == "start" || f[3] == "end"
	}
	return Marker{}, false
}

// markerWriter passes output to out and hands marker lines to on.
type markerWriter struct {
	out      io.Writer
	on       func(Marker)
	inMarker bool
	marker   []byte
}

func (w *markerWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if w.inMarker {
			i := bytes.IndexByte(p, '\n')
			if i < 0 {
				if len(w.marker) < 256 {
					w.marker = append(w.marker, p...)
				}
				return n, nil
			}
			w.marker = append(w.marker, p[:i]...)
			if m, ok := ParseMarker(string(w.marker)); ok && w.on != nil {
				w.on(m)
			}
			w.marker, w.inMarker, p = w.marker[:0], false, p[i+1:]
			continue
		}
		i := bytes.IndexByte(p, markerByte)
		if i < 0 {
			if _, err := w.out.Write(p); err != nil {
				return n, err
			}
			return n, nil
		}
		if i > 0 {
			if _, err := w.out.Write(p[:i]); err != nil {
				return n, err
			}
		}
		w.inMarker, p = true, p[i+1:]
	}
	return n, nil
}
