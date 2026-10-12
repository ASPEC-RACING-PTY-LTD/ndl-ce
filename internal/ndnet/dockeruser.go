package ndnet

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	IPTablesBin  = "/usr/sbin/iptables"
	IP6TablesBin = "/usr/sbin/ip6tables"

	// DockerUserChain is evaluated first from FORWARD and is never flushed by
	// Docker, so No-dal accept rules placed here survive Docker restarts.
	DockerUserChain = "DOCKER-USER"

	// forwardRuleComment marks the rules No-dal owns in DOCKER-USER. No spaces,
	// so `iptables -S` output splits cleanly on whitespace.
	forwardRuleComment = "ndl-ce-bridge"
)

// ForwardStatus reports what EnsureForwardAccept found and changed.
type ForwardStatus struct {
	Bridges  []string
	Inserted int
	Removed  int
	// Blocked is true when the host FORWARD policy drops traffic and there is
	// no DOCKER-USER chain to place No-dal accept rules in.
	Blocked bool
	Note    string
}

// EnsureForwardAccept keeps No-dal bridge traffic out of a host FORWARD
// drop policy. Host Docker sets `iptables -P FORWARD DROP`, and with
// br_netfilter loaded (bridge-nf-call-iptables=1) plain bridged frames on a
// LAN bridge pass through that chain too. An accept in No-dal's own nft
// table cannot help: a drop verdict in any base chain on the forward hook is
// final. The rules go in DOCKER-USER, which Docker evaluates first and never
// flushes. Idempotent; call it at startup and periodically so a Docker start
// or restart after the agent is covered.
func (e *Engine) EnsureForwardAccept(ctx context.Context) (ForwardStatus, error) {
	st := ForwardStatus{Bridges: e.ndlBridges()}
	nat := e.natBridges()
	for _, fam := range []struct {
		bin string
		v6  bool
	}{{IPTablesBin, false}, {IP6TablesBin, true}} {
		if !e.binUsable(fam.bin) {
			continue
		}
		if _, err := e.runOutput(ctx, fam.bin, "-w", "5", "-S", DockerUserChain); err != nil {
			if !fam.v6 && len(st.Bridges) > 0 && e.forwardDrops(ctx, fam.bin) && e.bridgeNFCallIPTables() {
				st.Blocked = true
				st.Note = "host FORWARD policy is DROP, br_netfilter sends bridged traffic through it, and there is no DOCKER-USER chain for No-dal accept rules"
			}
			continue
		}
		st.Removed += e.pruneForwardRules(ctx, fam.bin, st.Bridges)
		for _, br := range st.Bridges {
			for _, spec := range forwardRuleSpecs(br, nat[br] && !fam.v6) {
				if e.run(ctx, fam.bin, append([]string{"-w", "5", "-C", DockerUserChain}, spec...)...) == nil {
					continue
				}
				if err := e.run(ctx, fam.bin, append([]string{"-w", "5", "-I", DockerUserChain, "1"}, spec...)...); err != nil {
					return st, err
				}
				st.Inserted++
			}
		}
	}
	return st, nil
}

// forwardRuleSpecs returns the DOCKER-USER rule bodies for one bridge.
// Every bridge gets intra-bridge forwarding (bridged L2 under br_netfilter).
// An isolated-nat bridge also gets routed egress and the return path.
func forwardRuleSpecs(br string, nat bool) [][]string {
	tag := []string{"-m", "comment", "--comment", forwardRuleComment, "-j", "ACCEPT"}
	specs := [][]string{
		append([]string{"-i", br, "-o", br}, tag...),
	}
	if nat {
		specs = append(specs,
			append([]string{"-i", br, "!", "-o", br}, tag...),
			append([]string{"!", "-i", br, "-o", br, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED"}, tag...),
		)
	}
	return specs
}

// pruneForwardRules deletes No-dal rules for bridges that no longer exist.
func (e *Engine) pruneForwardRules(ctx context.Context, bin string, bridges []string) int {
	out, err := e.runOutput(ctx, bin, "-w", "5", "-S", DockerUserChain)
	if err != nil {
		return 0
	}
	live := map[string]bool{}
	for _, br := range bridges {
		live[br] = true
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "-A" || f[1] != DockerUserChain || !strings.Contains(line, forwardRuleComment) {
			continue
		}
		br := ruleBridge(f)
		if br == "" || live[br] {
			continue
		}
		args := append([]string{"-w", "5", "-D"}, f[1:]...)
		for i, a := range args {
			args[i] = strings.Trim(a, `"`)
		}
		if e.run(ctx, bin, args...) == nil {
			n++
		}
	}
	return n
}

func ruleBridge(fields []string) string {
	for i := 0; i+1 < len(fields); i++ {
		if (fields[i] == "-i" || fields[i] == "-o") && strings.HasPrefix(fields[i+1], "ndl") {
			return fields[i+1]
		}
	}
	return ""
}

// ndlBridges lists live Linux bridges that No-dal owns (ndl<8 hex>).
func (e *Engine) ndlBridges() []string {
	root := e.Root
	if root == "" {
		root = "/"
	}
	dir := filepath.Join(root, "sys/class/net")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		if !isNDLBridgeName(name) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, name, "bridge")); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func isNDLBridgeName(name string) bool {
	if len(name) != 11 || !strings.HasPrefix(name, "ndl") {
		return false
	}
	for _, r := range name[3:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// natBridges maps bridge names of persisted isolated-nat networks.
func (e *Engine) natBridges() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(e.stateDir(), "nft"))
	if err != nil {
		return out
	}
	for _, ent := range entries {
		if ent.IsDir() || !natPersistName(ent.Name()) {
			continue
		}
		if br, err := BridgeName(strings.TrimSuffix(ent.Name(), ".nft")); err == nil {
			out[br] = true
		}
	}
	return out
}

func (e *Engine) binUsable(bin string) bool {
	if e.Run != nil || e.Output != nil {
		return true
	}
	if e.SkipHostCmds {
		return false
	}
	_, err := os.Stat(bin)
	return err == nil
}

func (e *Engine) forwardDrops(ctx context.Context, bin string) bool {
	out, err := e.runOutput(ctx, bin, "-w", "5", "-S", "FORWARD")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "-P FORWARD DROP" {
			return true
		}
	}
	return false
}

func (e *Engine) bridgeNFCallIPTables() bool {
	root := e.Root
	if root == "" {
		root = "/"
	}
	b, err := os.ReadFile(filepath.Join(root, "proc/sys/net/bridge/bridge-nf-call-iptables"))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}
