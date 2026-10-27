package shield

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// HardeningFile is the No-dal-owned sysctl drop-in, removed when hardening
// is turned off.
const HardeningFile = "50-ndl-shield.conf"

const hardeningOrigin = "shield.sysctl.origin"

// sysctlRule raises (or lowers) one kernel setting. Hardening only moves a
// setting toward safety: a host already stricter is left alone.
type sysctlRule struct {
	Key    string
	Want   int64
	Raise  bool // true: at least Want; false: at most Want
	Reason string
}

var hardening = []sysctlRule{
	{Key: "net.ipv4.tcp_syncookies", Want: 1, Raise: true, Reason: "answer SYN floods with cookies instead of filling the backlog"},
	{Key: "net.ipv4.tcp_max_syn_backlog", Want: 8192, Raise: true, Reason: "hold more half-open connections"},
	{Key: "net.ipv4.tcp_synack_retries", Want: 3, Raise: false, Reason: "give up on spoofed half-open connections sooner"},
	{Key: "net.netfilter.nf_conntrack_max", Want: 262144, Raise: true, Reason: "keep room in the connection table during a flood"},
}

// SysctlState is one hardening setting on the node.
type SysctlState struct {
	Key     string `json:"key"`
	Value   int64  `json:"value"`
	Want    int64  `json:"want"`
	Applied bool   `json:"applied"`
	Reason  string `json:"reason"`
}

func sysctlPath(key string) string {
	return "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
}

func (e *Engine) sysctlDir() string { return e.path("/etc/sysctl.d") }

func (r sysctlRule) target(cur int64) int64 {
	if r.Raise && cur < r.Want || !r.Raise && cur > r.Want {
		return r.Want
	}
	return cur
}

func (e *Engine) sysctlStates() []SysctlState {
	on := false
	if _, err := os.Stat(filepath.Join(e.sysctlDir(), HardeningFile)); err == nil {
		on = true
	}
	out := make([]SysctlState, 0, len(hardening))
	for _, r := range hardening {
		raw, err := os.ReadFile(e.path(sysctlPath(r.Key)))
		if err != nil {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		out = append(out, SysctlState{Key: r.Key, Value: v, Want: r.target(v), Applied: on, Reason: r.Reason})
	}
	return out
}

// harden applies or undoes the kernel settings. Origins are kept the first
// time so turning it off restores what the host had.
func (e *Engine) harden(on bool) error {
	originPath := filepath.Join(e.StateDir, hardeningOrigin)
	dropIn := filepath.Join(e.sysctlDir(), HardeningFile)
	if !on {
		raw, err := os.ReadFile(originPath)
		if err != nil {
			return nil
		}
		var origin map[string]int64
		if json.Unmarshal(raw, &origin) == nil {
			for key, v := range origin {
				_ = os.WriteFile(e.path(sysctlPath(key)), []byte(strconv.FormatInt(v, 10)+"\n"), 0o644)
			}
		}
		_ = os.Remove(dropIn)
		return os.Remove(originPath)
	}
	origin := map[string]int64{}
	if raw, err := os.ReadFile(originPath); err == nil {
		_ = json.Unmarshal(raw, &origin)
	}
	var lines []string
	for _, r := range hardening {
		p := e.path(sysctlPath(r.Key))
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		cur, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			continue
		}
		if _, kept := origin[r.Key]; !kept {
			origin[r.Key] = cur
		}
		want := r.target(cur)
		lines = append(lines, r.Key+" = "+strconv.FormatInt(want, 10))
		if want != cur {
			if err := os.WriteFile(p, []byte(strconv.FormatInt(want, 10)+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	if err := e.saveJSON(hardeningOrigin, origin); err != nil {
		return err
	}
	sort.Strings(lines)
	body := "# No-dal DDoS protection. Removed when kernel hardening is turned off.\n" + strings.Join(lines, "\n") + "\n"
	if err := os.MkdirAll(e.sysctlDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dropIn, []byte(body), 0o644)
}
