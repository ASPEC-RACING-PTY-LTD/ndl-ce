package edge

import (
	"strings"
	"testing"
)

const key = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY="

func originSpec() Spec {
	return Spec{Tunnels: []Tunnel{{
		ID: "t1", Role: RoleOrigin, Index: 1, Address: "10.66.0.6/30", Peer: "10.66.0.5", PeerKey: key,
		Endpoint: "45.38.149.183:51871", Egress: []string{"ndl-gs-abc", "ndl-gs-abc"},
		Forwards: []Forward{
			{ID: "jvs", Protocol: "UDP", Port: 27015},
			{ID: "mc", Protocol: "tcp", Port: 25565, Target: "192.168.2.31", Masquerade: true},
		},
	}}}
}

func TestNormalizeRefusesWhatCannotWork(t *testing.T) {
	cases := map[string]func(*Spec){
		"role must be":                  func(s *Spec) { s.Tunnels[0].Role = "relay" },
		"peer must be another":          func(s *Spec) { s.Tunnels[0].Peer = "10.66.1.1" },
		"endpoint must be":              func(s *Spec) { s.Tunnels[0].Endpoint = "vps.example:51871" },
		"peer key is invalid":           func(s *Spec) { s.Tunnels[0].PeerKey = "nope" },
		"protocol must be":              func(s *Spec) { s.Tunnels[0].Forwards[0].Protocol = "sctp" },
		"range keeps its ports":         func(s *Spec) { s.Tunnels[0].Forwards[0].PortEnd, s.Tunnels[0].Forwards[0].ToPort = 27020, 28000 },
		"not an address or a container": func(s *Spec) { s.Tunnels[0].Egress = []string{"rm -rf"} },
		"index must be":                 func(s *Spec) { s.Tunnels[0].Index = 0 },
	}
	for want, change := range cases {
		s := originSpec()
		change(&s)
		if _, err := Normalize(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	edge := Spec{Tunnels: []Tunnel{{ID: "t1", Role: RoleEdge, Index: 1, Address: "10.66.0.5/30", Peer: "10.66.0.6", ListenPort: 51871,
		Forwards: []Forward{{ID: "a", Protocol: "udp", Port: 27015}, {ID: "b", Protocol: "udp", Port: 27000, PortEnd: 27020}}}}}
	if _, err := Normalize(edge); err == nil || !strings.Contains(err.Error(), "both take UDP 27015") {
		t.Errorf("overlap: %v", err)
	}
	edge.Tunnels[0].Forwards = []Forward{{ID: "a", Protocol: "udp", Port: 51800, PortEnd: 51900}}
	if _, err := Normalize(edge); err == nil || !strings.Contains(err.Error(), "tunnel port 51871") {
		t.Errorf("tunnel port: %v", err)
	}
	edge.Tunnels[0].Forwards = []Forward{{ID: "a", Protocol: "tcp", Port: 9000, PortEnd: 10000}}
	if _, err := Normalize(edge); err == nil || !strings.Contains(err.Error(), "No-dal agent") {
		t.Errorf("agent port: %v", err)
	}
}

func TestRenderOrigin(t *testing.T) {
	s, err := Normalize(originSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tunnels[0].Egress) != 1 {
		t.Fatalf("egress not deduplicated: %v", s.Tunnels[0].Egress)
	}
	out := Render(s, Env{Egress: map[string][]string{"t1": {"172.17.0.2"}}})
	ifn := `"` + IfName("t1") + `"`
	for _, want := range []string{
		"delete table inet ndl_edge",
		// A LAN target gets the traffic, hidden behind the origin.
		"iifname " + ifn + " ip daddr 10.66.0.6 tcp dport 25565 counter name \"" + CounterName("t1", "mc") + "\" dnat ip to 192.168.2.31",
		"iifname " + ifn + " ip daddr 192.168.2.31 tcp dport 25565 masquerade",
		// Replies and the game server's own traffic go back through the tunnel.
		"iifname " + ifn + " ct state new ct mark set 0x6e640001",
		"ip saddr { 172.17.0.2 } ip daddr != @private4 ct state new ct mark set 0x6e640001",
		"udp sport { 27015 } notrack",
		"udp sport { 27015 } ip saddr set 10.66.0.6 meta mark set 0x6e640001",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "fib daddr type local") {
		t.Error("an origin must not DNAT public traffic")
	}
}

func TestRenderEdgeKeepsTheSource(t *testing.T) {
	s, err := Normalize(Spec{Tunnels: []Tunnel{{ID: "t1", Role: RoleEdge, Index: 1, Address: "10.66.0.5/30", Peer: "10.66.0.6", ListenPort: 51871,
		Forwards: []Forward{{ID: "jvs", Protocol: "udp", Port: 27015}, {ID: "alt", Protocol: "udp", Port: 7777, ToPort: 27016}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out := Render(s, Env{})
	ifn := `"` + IfName("t1") + `"`
	for _, want := range []string{
		"iifname != " + ifn + " fib daddr type local udp dport 27015 counter name \"" + CounterName("t1", "jvs") + "\" dnat ip to 10.66.0.6\n",
		"udp dport 7777 counter name \"" + CounterName("t1", "alt") + "\" dnat ip to 10.66.0.6:27016",
		"iifname " + ifn + " ip saddr 10.66.0.6 oifname != " + ifn + " masquerade",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// Player traffic is never source-NATed on the edge.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "masquerade") && !strings.Contains(line, "ip saddr 10.66.0.6") {
			t.Errorf("edge hides player addresses: %s", line)
		}
	}
	if Render(Spec{}, Env{}) != "table inet ndl_edge\ndelete table inet ndl_edge\n" {
		t.Error("an empty spec must only delete the table")
	}
}
