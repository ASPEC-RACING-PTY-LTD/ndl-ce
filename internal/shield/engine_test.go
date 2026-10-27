package shield

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, p, body string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeHost is a node with enp6s0 enslaved to a LAN bridge next to guest veths.
func fakeHost(t *testing.T) string {
	root := t.TempDir()
	writeFile(t, root, "/proc/net/route", "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\n"+
		"ndl685ff937\t00000000\t0102A8C0\t0003\t0\t0\t0\t00000000\n"+
		"ndl685ff937\t0002A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\n")
	writeFile(t, root, "/sys/class/net/ndl685ff937/bridge/stp_state", "0")
	writeFile(t, root, "/sys/class/net/ndl685ff937/brif/enp6s0/port_no", "1")
	writeFile(t, root, "/sys/class/net/ndl685ff937/brif/vethOMWw15/port_no", "2")
	writeFile(t, root, "/sys/class/net/enp6s0/device/vendor", "0x8086")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_syncookies", "1\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_max_syn_backlog", "4096\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_synack_retries", "5\n")
	writeFile(t, root, "/proc/sys/net/netfilter/nf_conntrack_max", "524288\n")
	writeFile(t, root, "/proc/sys/net/netfilter/nf_conntrack_count", "371\n")
	return root
}

func TestDetectUplinksUsesTheBridgesPhysicalPort(t *testing.T) {
	e := &Engine{SysRoot: fakeHost(t)}
	got := e.DetectUplinks()
	if len(got) != 1 || got[0] != "enp6s0" {
		t.Fatalf("uplinks = %v, want [enp6s0]", got)
	}
}

func TestHardeningOnlyTightensAndRestores(t *testing.T) {
	root := fakeHost(t)
	e := &Engine{SysRoot: root, StateDir: t.TempDir()}
	read := func(p string) string {
		raw, _ := os.ReadFile(filepath.Join(root, p))
		return strings.TrimSpace(string(raw))
	}
	if err := e.harden(true); err != nil {
		t.Fatal(err)
	}
	if read("/proc/sys/net/ipv4/tcp_max_syn_backlog") != "8192" || read("/proc/sys/net/ipv4/tcp_synack_retries") != "3" {
		t.Fatal("hardening did not apply")
	}
	if read("/proc/sys/net/netfilter/nf_conntrack_max") != "524288" {
		t.Fatal("hardening lowered a larger conntrack table")
	}
	if !strings.Contains(read("/etc/sysctl.d/"+HardeningFile), "net.ipv4.tcp_synack_retries = 3") {
		t.Fatal("drop-in missing")
	}
	// A second apply must keep the host's original values, not ours.
	if err := e.harden(true); err != nil {
		t.Fatal(err)
	}
	if err := e.harden(false); err != nil {
		t.Fatal(err)
	}
	if read("/proc/sys/net/ipv4/tcp_max_syn_backlog") != "4096" || read("/proc/sys/net/ipv4/tcp_synack_retries") != "5" {
		t.Fatal("turning hardening off did not restore the host")
	}
	if _, err := os.Stat(filepath.Join(root, "/etc/sysctl.d", HardeningFile)); err == nil {
		t.Fatal("drop-in left behind")
	}
}

const listing = `{"nftables": [{"metainfo": {"version": "1.1.3"}}, {"table": {"family": "netdev", "name": "ndl_shield"}},
{"counter": {"family": "netdev", "name": "r0_in", "table": "ndl_shield", "packets": 5000, "bytes": 400000}},
{"counter": {"family": "netdev", "name": "r0_flood", "table": "ndl_shield", "packets": 3000, "bytes": 240000}},
{"counter": {"family": "netdev", "name": "r0_query", "table": "ndl_shield", "packets": 40, "bytes": 2000}},
{"counter": {"family": "netdev", "name": "r0_amp", "table": "ndl_shield", "packets": 0, "bytes": 0}},
{"counter": {"family": "netdev", "name": "banned", "table": "ndl_shield", "packets": 7, "bytes": 700}},
{"set": {"family": "netdev", "name": "ban4", "table": "ndl_shield", "type": "ipv4_addr", "elem": [{"elem": {"val": "198.51.100.9", "timeout": 120, "expires": 100}}]}},
{"set": {"family": "netdev", "name": "r0_ban4", "table": "ndl_shield", "type": "ipv4_addr", "elem": [{"elem": {"val": "203.0.113.5", "expires": 250}}]}},
{"set": {"family": "netdev", "name": "trusted4", "table": "ndl_shield", "type": "ipv4_addr", "elem": ["192.168.2.0/24"]}}]}`

type fakeNFT struct {
	calls  []string
	stdin  []string
	tables bool
	fail   string
}

func (f *fakeNFT) run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if stdin != nil {
		f.stdin = append(f.stdin, string(stdin))
	}
	if f.fail != "" && strings.Contains(call, f.fail) {
		return []byte("Error: syntax error"), errors.New("Error: syntax error")
	}
	if strings.Contains(call, "-j list table netdev") {
		if !f.tables {
			return nil, errors.New("No such file or directory")
		}
		return []byte(listing), nil
	}
	if strings.Contains(call, "-j list table inet") {
		return nil, errors.New("No such file or directory")
	}
	if strings.HasSuffix(call, "-f "+"-") || strings.Contains(call, " -f ") && !strings.Contains(call, " -c ") {
		f.tables = true
	}
	return nil, nil
}

func jvsSpec() Spec {
	src, _ := LookupProfile(ProfileSource)
	return Spec{Mode: ModeEnforce, Hardening: true, Rules: []Rule{{ID: "gs:jvs", Name: "JvS", Host: true, Protocol: ProtoBoth, Ports: []string{"27015"}, Limits: src.Limits, Action: ActionBan}}}
}

func TestApplyChecksLoadsAndReportsStatus(t *testing.T) {
	f := &fakeNFT{}
	e := &Engine{SysRoot: fakeHost(t), StateDir: t.TempDir(), Run: f.run, Now: func() time.Time { return time.Unix(1000, 0) }}
	st, err := e.Apply(context.Background(), jvsSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) < 3 || !strings.Contains(f.calls[1], "-c -f") || !strings.Contains(f.calls[2], NFTBin+" -f") {
		t.Fatalf("calls = %v; want a check before the load", f.calls)
	}
	if !st.Active || st.Devices[0] != "enp6s0" || st.SpecHash != jvsSpec().Hash() {
		t.Fatalf("status = %+v", st)
	}
	rs := st.Rules["gs:jvs"]
	if rs.In.Packets != 5000 || rs.Dropped.Packets != 3040 || rs.Reasons["flood"].Packets != 3000 || rs.Bans != 1 {
		t.Fatalf("rule status = %+v", rs)
	}
	if _, ok := rs.Reasons["amp"]; ok {
		t.Error("an empty reason was reported")
	}
	if st.Banned.Packets != 7 || len(st.Bans) != 2 {
		t.Fatalf("bans = %+v", st.Bans)
	}
	// The automatic ban is carried into the next apply.
	f.calls = nil
	if _, err := e.Apply(context.Background(), jvsSpec()); err != nil {
		t.Fatal(err)
	}
	rules, _ := os.ReadFile(filepath.Join(e.StateDir, rulesFile))
	if !strings.Contains(string(rules), "add element netdev ndl_shield r0_ban4 { 203.0.113.5 timeout 250s }") {
		t.Fatalf("ban not carried:\n%s", rules)
	}
	// Ensure leaves loaded, unchanged rules alone.
	f.calls = nil
	if err := e.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, " -f ") {
			t.Fatalf("Ensure re-applied unchanged rules: %v", f.calls)
		}
	}
}

func TestApplyKeepsOldRulesWhenTheCheckFails(t *testing.T) {
	f := &fakeNFT{fail: "-c -f"}
	e := &Engine{SysRoot: fakeHost(t), StateDir: t.TempDir(), Run: f.run}
	if _, err := e.Apply(context.Background(), jvsSpec()); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if strings.HasSuffix(c, rulesFile) && !strings.Contains(c, "-c") {
			t.Fatal("rules were loaded after a failed check")
		}
	}
	if _, ok := e.LoadSpec(); ok {
		t.Fatal("a spec that failed to load was kept")
	}
}

func TestEnsureReloadsMissingTables(t *testing.T) {
	f := &fakeNFT{}
	e := &Engine{SysRoot: fakeHost(t), StateDir: t.TempDir(), Run: f.run}
	if _, err := e.Apply(context.Background(), jvsSpec()); err != nil {
		t.Fatal(err)
	}
	f.tables = false // someone ran nft flush ruleset, or the node rebooted
	f.calls = nil
	if err := e.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.tables {
		t.Fatalf("Ensure did not reload: %v", f.calls)
	}
}

func TestBanUnbanAndDisable(t *testing.T) {
	f := &fakeNFT{}
	e := &Engine{SysRoot: fakeHost(t), StateDir: t.TempDir(), Run: f.run, Now: func() time.Time { return time.Unix(1000, 0) }}
	if _, err := e.Apply(context.Background(), jvsSpec()); err != nil {
		t.Fatal(err)
	}
	if err := e.Ban(context.Background(), "198.51.100.9", 1600); err != nil {
		t.Fatal(err)
	}
	if got := f.stdin[len(f.stdin)-1]; !strings.Contains(got, "ban4 { 198.51.100.9 timeout 600s }") {
		t.Fatalf("ban = %q", got)
	}
	if spec, _ := e.LoadSpec(); spec.Bans["198.51.100.9"] != 1600 {
		t.Fatal("ban not kept for the next apply")
	}
	f.calls = nil
	if err := e.Unban(context.Background(), "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "delete element netdev ndl_shield ban4 { 198.51.100.9 }") || !strings.Contains(joined, "r0_ban4 { 198.51.100.9 }") {
		t.Fatalf("unban calls = %v", f.calls)
	}
	if spec, _ := e.LoadSpec(); len(spec.Bans) != 0 {
		t.Fatal("ban still kept")
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stdin[len(f.stdin)-1], "destroy table inet ndl_shield") {
		t.Fatal("disable did not remove the tables")
	}
	if _, ok := e.LoadSpec(); ok {
		t.Fatal("spec kept after disable")
	}
}
