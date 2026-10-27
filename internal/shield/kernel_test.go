package shield

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestKernel loads rendered rules into the kernel and floods them, inside
// throwaway network namespaces so the host's own networking is never
// touched. It needs root, nft and unshare: NDL_SHIELD_KERNEL=1 go test.
func TestKernel(t *testing.T) {
	switch os.Getenv("NDL_SHIELD_STAGE") {
	case "":
		if os.Getenv("NDL_SHIELD_KERNEL") != "1" || os.Geteuid() != 0 {
			t.Skip("set NDL_SHIELD_KERNEL=1 and run as root")
		}
		cmd := exec.Command("unshare", "-n", os.Args[0], "-test.run=^TestKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "NDL_SHIELD_STAGE=ns")
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatal(err)
		}
	case "ns":
		kernelInNamespace(t)
	case "send":
		kernelSend(t)
	}
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func kernelInNamespace(t *testing.T) {
	sh(t, "ip", "link", "add", "veth0", "type", "veth", "peer", "name", "veth1")
	peer := exec.Command("unshare", "-n", "sleep", "120")
	if err := peer.Start(); err != nil {
		t.Fatal(err)
	}
	defer peer.Process.Kill()
	pid := strconv.Itoa(peer.Process.Pid)
	time.Sleep(200 * time.Millisecond)
	sh(t, "ip", "link", "set", "veth0", "netns", pid)
	sh(t, "ip", "addr", "add", "10.9.0.2/24", "dev", "veth1")
	sh(t, "ip", "link", "set", "veth1", "up")
	sh(t, "ip", "link", "set", "lo", "up")
	sh(t, "nsenter", "-t", pid, "-n", "sh", "-c", "ip addr add 10.9.0.1/24 dev veth0 && ip link set veth0 up && ip link set lo up")

	src, _ := LookupProfile(ProfileSource)
	src.Limits.PerSourcePPS, src.Limits.PerSourceBurst = 200, 200
	spec := Spec{
		Mode: ModeEnforce, DropInvalidTCP: true, Trusted: []string{"127.0.0.0/8"},
		Rules: []Rule{
			{ID: "gs:jvs", Name: "JvS", Host: true, Protocol: ProtoUDP, Ports: []string{"27015"}, Limits: src.Limits, Action: ActionBan},
			{ID: "mc", Name: "MC", Host: true, Protocol: ProtoTCP, Ports: []string{"25565"}, Limits: Limits{ConnsPerSource: 3, SYNPerSource: 100}, Action: ActionDrop},
		},
	}
	script, err := Render(spec, Env{Devices: []string{"veth1"}, Host: []netip.Addr{netip.MustParseAddr("10.9.0.2")}, Now: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shield.nft")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	sh(t, "nft", "-c", "-f", path)
	sh(t, "nft", "-f", path)

	var got atomic.Int64
	pc, err := net.ListenPacket("udp4", "10.9.0.2:27015")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
			got.Add(1)
		}
	}()
	ln, err := net.Listen("tcp4", "10.9.0.2:25565")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	send := func(mode string) string {
		cmd := exec.Command("nsenter", "-t", pid, "-n", os.Args[0], "-test.run=^TestKernel$")
		cmd.Env = append(os.Environ(), "NDL_SHIELD_STAGE=send", "NDL_SHIELD_MODE="+mode)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("send %s: %v\n%s", mode, err, out)
		}
		time.Sleep(300 * time.Millisecond)
		return string(out)
	}
	counters := func() map[string]uint64 {
		var doc struct {
			Nftables []struct {
				Counter *struct {
					Name    string `json:"name"`
					Packets uint64 `json:"packets"`
				} `json:"counter"`
			} `json:"nftables"`
		}
		_ = json.Unmarshal([]byte(sh(t, "nft", "-j", "list", "counters")), &doc)
		out := map[string]uint64{}
		for _, it := range doc.Nftables {
			if it.Counter != nil {
				out[it.Counter.Name] += it.Counter.Packets
			}
		}
		return out
	}

	send("query")
	c := counters()
	if c["r0_query"] != 40 || got.Load() != 10 {
		t.Fatalf("query flood: %d dropped, %d delivered, want 40 and 10 (%v)", c["r0_query"], got.Load(), c)
	}
	send("amp")
	if c = counters(); c["r0_amp"] != 20 {
		t.Fatalf("reflection: %d dropped, want 20", c["r0_amp"])
	}
	send("tiny")
	if c = counters(); c["r0_bad"] != 20 {
		t.Fatalf("empty packets: %d dropped, want 20", c["r0_bad"])
	}
	before := got.Load()
	send("flood")
	c = counters()
	passed := got.Load() - before
	if c["r0_flood"] == 0 || c["r0_ban"] == 0 || passed > 260 {
		t.Fatalf("flood: %d over limit, %d banned, %d delivered (%v)", c["r0_flood"], c["r0_ban"], passed, c)
	}
	bans := sh(t, "nft", "list", "set", "netdev", TableName, "r0_ban4")
	if !strings.Contains(bans, "10.9.0.1") {
		t.Fatalf("source was not banned:\n%s", bans)
	}
	if out := send("conns"); !strings.Contains(out, "open=3") {
		t.Fatalf("connection limit: %s (%v)", out, counters())
	}
	if c = counters(); c["r1_conns"] == 0 {
		t.Fatalf("connection limit not counted: %v", c)
	}
	sh(t, "nft", "delete", "table", "netdev", TableName)
	sh(t, "nft", "delete", "table", "inet", TableName)
}

func kernelSend(t *testing.T) {
	mode := os.Getenv("NDL_SHIELD_MODE")
	if mode == "conns" {
		open := 0
		var conns []net.Conn
		for i := 0; i < 6; i++ {
			c, err := net.DialTimeout("tcp4", "10.9.0.2:25565", 700*time.Millisecond)
			if err == nil {
				open++
				conns = append(conns, c)
			}
		}
		fmt.Printf("open=%d\n", open)
		for _, c := range conns {
			c.Close()
		}
		return
	}
	laddr := &net.UDPAddr{IP: net.ParseIP("10.9.0.1")}
	if mode == "amp" {
		laddr.Port = 123
	}
	c, err := net.DialUDP("udp4", laddr, &net.UDPAddr{IP: net.ParseIP("10.9.0.2"), Port: 27015})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload, n := []byte("x"), 20
	switch mode {
	case "query":
		payload, n = []byte("\xff\xff\xff\xffTSource Engine Query\x00"), 50
	case "amp":
		payload = []byte("ntp response")
	case "flood":
		payload, n = []byte(strings.Repeat("x", 64)), 3000
	}
	for i := 0; i < n; i++ {
		_, _ = c.Write(payload)
	}
}
