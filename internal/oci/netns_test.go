package oci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const netTestID = "3f2a1c4e-0000-4000-8000-00000000abcd"

func TestNetnsSetupWiresAFixedAddress(t *testing.T) {
	spec := Spec{WorkloadID: netTestID, Name: "web", ImagePin: "nginx:1", NetworkMode: NetworkBridge,
		BridgeName: "br0", IPv4Address: "192.168.1.50/24", IPv4Gateway: "192.168.1.1"}
	cmds, err := NetnsSetupArgv(spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(cmds))
	for i, c := range cmds {
		joined[i] = strings.Join(c, " ")
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"netns add " + NetnsName(netTestID),
		"link set " + HostVeth(netTestID) + " master br0",
		"addr add 192.168.1.50/24 dev eth0",
		"route add default via 192.168.1.1",
		"address " + ContainerMAC(netTestID),
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in\n%s", want, all)
		}
	}
	if len(HostVeth(netTestID)) > 15 {
		t.Fatalf("interface name too long: %s", HostVeth(netTestID))
	}
}

func TestOldContainersKeepNoNetwork(t *testing.T) {
	spec := Spec{WorkloadID: netTestID, Name: "old", ImagePin: "nginx:1", BridgeName: "br0"}
	if EffectiveNetworkMode(spec) != NetworkNone {
		t.Fatal("a container made before network modes must not gain a network on restart")
	}
	argv, err := TaskStartArgv("nodal", spec)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(argv, " ")
	if strings.Contains(line, "--net-host") || strings.Contains(line, "--with-ns") {
		t.Fatalf("argv changed for an old container: %s", line)
	}
}

func TestRunJoinsTheNamespaceOrHostNetwork(t *testing.T) {
	spec := Spec{WorkloadID: netTestID, Name: "web", ImagePin: "nginx:1", NetworkMode: NetworkBridge, BridgeName: "br0",
		NetnsPath: "/var/run/netns/" + NetnsName(netTestID), ResolvPath: "/etc/netns/x/resolv.conf"}
	argv, err := TaskStartArgv("nodal", spec)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(argv, " ")
	if !strings.Contains(line, "--with-ns network:/var/run/netns/"+NetnsName(netTestID)) || !strings.Contains(line, "dst=/etc/resolv.conf") {
		t.Fatalf("bridge argv %s", line)
	}
	spec = Spec{WorkloadID: netTestID, Name: "web", ImagePin: "nginx:1", NetworkMode: NetworkHost}
	argv, _ = TaskStartArgv("nodal", spec)
	if !strings.Contains(strings.Join(argv, " "), "--net-host") {
		t.Fatal("host mode must use the host network")
	}
}

func TestValidateNetworkRefusesBadInput(t *testing.T) {
	base := Spec{WorkloadID: netTestID, Name: "web", ImagePin: "nginx:1"}
	cases := []Spec{
		{NetworkMode: "weird"},
		{NetworkMode: NetworkBridge},
		{NetworkMode: NetworkBridge, BridgeName: "br0", IPv4Address: "192.168.1.50"},
		{NetworkMode: NetworkHost, IPv4Address: "192.168.1.50/24"},
		{NetworkMode: NetworkHost, Ports: []Port{{ContainerPort: 80, HostPort: 8080}}},
		{NetworkMode: NetworkBridge, BridgeName: "br0", Ports: []Port{{ContainerPort: 80, HostPort: 8080}, {ContainerPort: 81, HostPort: 8080}}},
		{NetworkMode: NetworkBridge, BridgeName: "br0", DNS: []string{"dns.example"}},
	}
	for i, c := range cases {
		s := base
		s.NetworkMode, s.BridgeName, s.IPv4Address, s.Ports, s.DNS = c.NetworkMode, c.BridgeName, c.IPv4Address, c.Ports, c.DNS
		if err := ValidateSpec(s); err == nil {
			t.Fatalf("case %d accepted: %+v", i, c)
		}
	}
}

func TestPortRulesPublishToTheContainer(t *testing.T) {
	rules := PortRules(netTestID, "192.168.1.50", []Port{{ContainerPort: 80, HostPort: 8080}, {ContainerPort: 53, Protocol: "udp"}})
	for _, want := range []string{
		"table ip " + nftTable(netTestID),
		"tcp dport 8080 dnat to 192.168.1.50:80",
		"udp dport 53 dnat to 192.168.1.50:53",
		"masquerade",
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("missing %q in\n%s", want, rules)
		}
	}
}

func TestPrepareAndTeardownBridgeNetwork(t *testing.T) {
	var ran []string
	dir := t.TempDir()
	e := &Engine{EtcDir: filepath.Join(dir, "etc"), RuntimeDir: filepath.Join(dir, "run"), Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "addr show dev eth0") {
			return []byte("2: eth0    inet 10.0.0.9/24 brd 10.0.0.255 scope global eth0"), nil
		}
		return nil, nil
	}}
	spec := Spec{WorkloadID: netTestID, Name: "web", ImagePin: "nginx:1", NetworkMode: NetworkBridge, BridgeName: "br0",
		Ports: []Port{{ContainerPort: 80, HostPort: 8080}}}
	out, err := e.PrepareNetwork(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	log := strings.Join(ran, "\n")
	if !strings.Contains(log, BinDHClient) {
		t.Fatalf("DHCP was not requested:\n%s", log)
	}
	if !strings.Contains(log, BinNFT+" -f") {
		t.Fatalf("ports were not published:\n%s", log)
	}
	raw, _ := os.ReadFile(filepath.Join(e.runDir(netTestID), "ports.nft"))
	if !strings.Contains(string(raw), "dnat to 10.0.0.9:80") {
		t.Fatalf("ports must go to the leased address: %s", raw)
	}
	if out.NetnsPath != "/var/run/netns/"+NetnsName(netTestID) || out.ResolvPath == "" {
		t.Fatalf("run paths %+v", out)
	}
	ran = nil
	e.TeardownNetwork(context.Background(), netTestID)
	log = strings.Join(ran, "\n")
	if !strings.Contains(log, "netns del "+NetnsName(netTestID)) || !strings.Contains(log, "delete table ip "+nftTable(netTestID)) {
		t.Fatalf("teardown:\n%s", log)
	}
	if _, err := os.Stat(e.runDir(netTestID)); !os.IsNotExist(err) {
		t.Fatal("runtime dir must be removed")
	}
}
