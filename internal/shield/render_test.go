package shield

import (
	"net/netip"
	"strings"
	"testing"
)

func sampleSpec() Spec {
	src, _ := LookupProfile(ProfileSource)
	mc, _ := LookupProfile(ProfileMinecraft)
	return Spec{
		Mode:           ModeEnforce,
		Trusted:        []string{"192.168.2.0/24", "127.0.0.1"},
		Blocked:        []string{"203.0.113.0/24"},
		DropInvalidTCP: true,
		Rules: []Rule{
			{ID: "gs:jvs", Name: "JvS", Host: true, Protocol: ProtoBoth, Ports: []string{"27015", "27016"}, Limits: src.Limits, Action: src.Action},
			{ID: "wl:mc", Name: "Minecraft", Dest: []string{"192.168.2.31"}, Protocol: ProtoTCP, Ports: []string{"25565"}, Limits: mc.Limits, Action: mc.Action, Allow: []string{"198.51.100.7"}},
			{ID: "custom:web", Name: "Web", Dest: []string{"2001:db8::/64"}, Protocol: ProtoAny, Limits: Limits{TotalPPS: 10000}, Action: ActionDrop},
		},
		Bans: map[string]int64{"198.51.100.9": 1000 + 120, "198.51.100.10": 900},
	}
}

func sampleEnv() Env {
	return Env{
		Devices: []string{"enp6s0"},
		Host:    []netip.Addr{netip.MustParseAddr("192.168.2.82"), netip.MustParseAddr("fe80::1")},
		Now:     1000,
		Carry:   map[string][]Ban{"gs:jvs": {{Addr: "198.51.100.20", RuleID: "gs:jvs", Seconds: 50}}},
	}
}

func TestRenderCoversEveryProtection(t *testing.T) {
	out, err := Render(sampleSpec(), sampleEnv())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"destroy table netdev ndl_shield",
		`type filter hook ingress devices = { "enp6s0" } priority -500; policy accept;`,
		"ip saddr @trusted4 accept",
		"elements = { 127.0.0.0/8, 192.168.2.0/24 }",
		`ip daddr @host4 meta l4proto { tcp, udp } th dport { 27015, 27016 } jump r0 comment "gs:jvs"`,
		`ip daddr @r1_d4 tcp dport { 25565 } jump r1`,
		"udp sport @amp_ports",
		"ip frag-off & 0x2000 != 0",
		"@th,64,32 0xffffffff @th,96,8 { 0x54, 0x55, 0x56, 0x57, 0x69, 0x71 } update @r0_q4 { ip saddr limit rate over 5/second burst 10 packets }",
		"update @r0_pps4 { ip saddr limit rate over 400/second burst 1200 packets } update @r0_ban4 { ip saddr } counter name \"r0_flood\" drop",
		"tcp flags & (syn|ack) == syn update @r1_syn4",
		"limit rate over 10000/second burst 2000 packets counter name \"r2_cap\" drop",
		"table inet ndl_shield",
		"type filter hook prerouting priority -150; policy accept;",
		"add @r1_ct4 { ip saddr ct count over 6 }",
		"add element netdev ndl_shield ban4 { 198.51.100.9 timeout 120s }",
		"add element netdev ndl_shield r0_ban4 { 198.51.100.20 timeout 50s }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "198.51.100.10") {
		t.Error("an expired manual ban was rendered")
	}
	if strings.Contains(out, "hook input") {
		t.Error("the shield must never hook input")
	}
}

func TestMonitorNeverDropsOrBans(t *testing.T) {
	spec := sampleSpec()
	spec.Mode = ModeMonitor
	out, err := Render(spec, sampleEnv())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, " drop") {
		t.Errorf("monitor mode drops:\n%s", out)
	}
	if strings.Contains(out, "update @r0_ban4") || strings.Contains(out, "r0_ban4 { 198") {
		t.Error("monitor mode bans")
	}
}

func TestRenderSkipsInetTableWithoutConnectionLimits(t *testing.T) {
	spec := Spec{Rules: []Rule{{ID: "a", Host: true, Protocol: ProtoUDP, Ports: []string{"27015"}, Limits: Limits{PerSourcePPS: 100}}}}
	out, err := Render(spec, sampleEnv())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "table inet ndl_shield {") {
		t.Error("inet table rendered without a connection limit")
	}
}

func TestRenderRefusesBadInput(t *testing.T) {
	for name, spec := range map[string]Spec{
		"no target":    {Rules: []Rule{{ID: "a", Protocol: ProtoUDP}}},
		"bad port":     {Rules: []Rule{{ID: "a", Host: true, Protocol: ProtoUDP, Ports: []string{"70000"}}}},
		"bad cidr":     {Trusted: []string{"10.0.0.0/33"}},
		"injected id":  {Rules: []Rule{{ID: `a" drop`, Host: true, Protocol: ProtoUDP}}},
		"ports on any": {Rules: []Rule{{ID: "a", Host: true, Protocol: ProtoAny, Ports: []string{"80"}}}},
		"duplicate":    {Rules: []Rule{{ID: "a", Host: true}, {ID: "a", Host: true}}},
		"bad mode":     {Mode: "panic"},
	} {
		if _, err := Render(spec, sampleEnv()); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
	if _, err := Render(Spec{}, Env{Devices: []string{"eth0; flush ruleset"}}); err == nil {
		t.Error("bad device rendered")
	}
}

func TestHashIgnoresBans(t *testing.T) {
	a := sampleSpec()
	b := sampleSpec()
	b.Bans = nil
	if a.Hash() != b.Hash() {
		t.Error("bans changed the hash")
	}
	b.Mode = ModeMonitor
	if a.Hash() == b.Hash() {
		t.Error("mode did not change the hash")
	}
}

func TestParsePort(t *testing.T) {
	for in, want := range map[string]string{"27015": "27015", " 27015-27020 ": "27015-27020", "80-80": "80"} {
		if got, err := ParsePort(in); err != nil || got != want {
			t.Errorf("ParsePort(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"0", "65536", "20-10", "a", "1,2"} {
		if _, err := ParsePort(bad); err == nil {
			t.Errorf("ParsePort(%q) passed", bad)
		}
	}
}

func TestProfileForGame(t *testing.T) {
	if ProfileForGame("source", false) != ProfileSource || ProfileForGame("minecraft-bedrock", false) != ProfileMinecraft ||
		ProfileForGame("steam", false) != ProfileGameUDP || ProfileForGame("openra", true) != ProfileGameTCP {
		t.Error("wrong profile")
	}
	for _, p := range Profiles() {
		if err := p.Limits.Validate(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}

func TestTrustLocalAddsTheNodesNetworks(t *testing.T) {
	spec := Spec{TrustLocal: true}
	env := sampleEnv()
	env.Local = []netip.Prefix{netip.MustParsePrefix("10.20.0.5/16")}
	out, err := Render(spec, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10.20.0.0/16, 127.0.0.0/8") {
		t.Fatalf("local network not trusted:\n%s", out)
	}
	spec.TrustLocal = false
	out, _ = Render(spec, env)
	if strings.Contains(out, "10.20.0.0/16") {
		t.Fatal("local network trusted without TrustLocal")
	}
}
