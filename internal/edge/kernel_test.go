package edge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKernel builds a small internet in throwaway network namespaces and
// sends real traffic through an edge tunnel:
//
//	player 198.51.100.10 ─┐
//	                      router (NATs the origin's LAN, forwards nothing in)
//	edge 203.0.113.1 ─────┤
//	origin 192.168.50.2 ──┘  ── container 172.30.0.2 (published like Docker)
//
// It checks that players reach a host-network server, a published
// container and a TCP server through the edge, that the servers see the
// player's own address, and that the container's outbound traffic leaves
// with the edge's address. The test process itself runs in a new network
// namespace (unshare -n) and every sysctl or file the engine writes goes
// under a temp dir, so the host is never touched. It needs root, nft,
// python3 and the wireguard module: NDL_EDGE_KERNEL=1 go test.
func TestKernel(t *testing.T) {
	switch os.Getenv("NDL_EDGE_STAGE") {
	case "":
		if os.Getenv("NDL_EDGE_KERNEL") != "1" || os.Geteuid() != 0 {
			t.Skip("set NDL_EDGE_KERNEL=1 and run as root")
		}
		cmd := exec.Command("unshare", "-n", os.Args[0], "-test.run=^TestKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "NDL_EDGE_STAGE=origin")
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatal(err)
		}
	case "origin":
		kernelOrigin(t)
	case "edge":
		kernelEdge(t)
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

// in runs a shell script in the namespace of pid.
func in(t *testing.T, pid int, script string) string {
	t.Helper()
	return sh(t, "nsenter", "-t", strconv.Itoa(pid), "-n", "sh", "-ec", script)
}

func spawnNS(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("unshare", "-n", "sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	time.Sleep(150 * time.Millisecond)
	return cmd.Process.Pid
}

const echoUDP = `
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", int(sys.argv[1])))
while True:
    data, addr = s.recvfrom(2048)
    s.sendto(("%s:%d" % addr).encode(), addr)
`

const echoTCP = `
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", int(sys.argv[1])))
s.listen(8)
while True:
    c, addr = s.accept()
    c.sendall(("%s:%d" % addr).encode())
    c.close()
`

// ask sends one datagram (or opens one TCP connection) from a connected
// socket, so a reply from any other address is never received.
const ask = `
import socket, sys
proto, host, port, sport = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
kind = socket.SOCK_DGRAM if proto == "udp" else socket.SOCK_STREAM
s = socket.socket(socket.AF_INET, kind)
s.settimeout(3)
if sport:
    s.bind(("0.0.0.0", sport))
s.connect((host, port))
if proto == "udp":
    s.send(b"hi")
print(s.recv(2048).decode())
`

func kernelOrigin(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"echo_udp.py": echoUDP, "echo_tcp.py": echoTCP, "ask.py": ask} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	py := func(script string, args ...string) []string {
		return append([]string{"python3", filepath.Join(dir, script)}, args...)
	}
	router, edgeNS, player, ctr := spawnNS(t), spawnNS(t), spawnNS(t), spawnNS(t)
	loose := "sysctl -qw net.ipv4.conf.all.rp_filter=0 net.ipv4.conf.default.rp_filter=0"
	link := func(a, b string, pidA, pidB int) {
		sh(t, "ip", "link", "add", a, "type", "veth", "peer", "name", b)
		if pidA != 0 {
			sh(t, "ip", "link", "set", a, "netns", strconv.Itoa(pidA))
		}
		sh(t, "ip", "link", "set", b, "netns", strconv.Itoa(pidB))
	}
	link("o0", "r-o", 0, router)
	link("r-e", "e0", router, edgeNS)
	link("r-p", "p0", router, player)
	link("o-c", "c0", 0, ctr)

	in(t, router, loose+`
ip link set lo up
ip addr add 192.168.50.1/24 dev r-o; ip link set r-o up
ip addr add 203.0.113.254/24 dev r-e; ip link set r-e up
ip addr add 198.51.100.254/24 dev r-p; ip link set r-p up
sysctl -qw net.ipv4.ip_forward=1
nft 'table ip nat { chain post { type nat hook postrouting priority 100; ip saddr 192.168.50.0/24 oifname != "r-o" masquerade; }; }'`)
	in(t, edgeNS, loose+`
ip link set lo up
ip addr add 203.0.113.1/24 dev e0; ip link set e0 up
ip route add default via 203.0.113.254
sysctl -qw net.ipv4.ip_forward=1`)
	in(t, player, loose+`
ip link set lo up
ip addr add 198.51.100.10/24 dev p0; ip link set p0 up
ip route add default via 198.51.100.254`)
	in(t, ctr, loose+`
ip link set lo up
ip addr add 172.30.0.2/24 dev c0; ip link set c0 up
ip route add default via 172.30.0.1`)
	// This process is the origin. The last table stands in for Docker
	// publishing UDP 27016 from the container.
	sh(t, "sh", "-ec", loose+`
ip link set lo up
ip addr add 192.168.50.2/24 dev o0; ip link set o0 up
ip addr add 172.30.0.1/24 dev o-c; ip link set o-c up
ip route add default via 192.168.50.1
sysctl -qw net.ipv4.ip_forward=1
nft 'table ip dsim { chain pre { type nat hook prerouting priority -100; fib daddr type local udp dport 27016 dnat to 172.30.0.2; }; chain post { type nat hook postrouting priority 100; ip saddr 172.30.0.0/24 oifname != "o-c" masquerade; }; }'`)

	start := func(pid int, argv []string) {
		var cmd *exec.Cmd
		if pid == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command("nsenter", append([]string{"-t", strconv.Itoa(pid), "-n"}, argv...)...)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	}
	start(0, py("echo_udp.py", "27015"))
	start(0, py("echo_tcp.py", "27020"))
	start(ctr, py("echo_udp.py", "27016"))
	start(player, py("echo_udp.py", "27900")) // a master server

	fwds := []Forward{
		{ID: "gs:udp:27015", Protocol: "udp", Port: 27015},
		{ID: "gs:udp:27016", Protocol: "udp", Port: 27016},
		{ID: "gs:tcp:27020", Protocol: "tcp", Port: 27020},
	}
	origin := Spec{Tunnels: []Tunnel{{
		ID: "e1", Role: RoleOrigin, Index: 1, Address: "10.66.0.2/30", Peer: "10.66.0.1",
		Endpoint: "203.0.113.1:51871", Forwards: fwds, Egress: []string{"172.30.0.2"},
	}}}
	edgeSpec := Spec{Tunnels: []Tunnel{{
		ID: "e1", Role: RoleEdge, Index: 1, Address: "10.66.0.1/30", Peer: "10.66.0.2",
		ListenPort: 51871, Forwards: fwds,
	}}}
	eng := &Engine{StateDir: filepath.Join(dir, "origin"), SysRoot: filepath.Join(dir, "originroot")}
	ctx := context.Background()
	st, err := eng.Apply(ctx, origin)
	if err != nil {
		t.Fatal(err)
	}
	originKey := st.Tunnels["e1"].PublicKey
	edgeSpec.Tunnels[0].PeerKey = originKey
	edgeKey := applyEdge(t, edgeNS, dir, edgeSpec)
	origin.Tunnels[0].PeerKey = edgeKey
	if _, err := eng.Apply(ctx, origin); err != nil {
		t.Fatal(err)
	}
	// Applying again must not reset the peer.
	if err := eng.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		st, _ = eng.Status(ctx)
		if !st.Tunnels["e1"].Handshake.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no handshake: %+v\n%s", st, sh(t, "nft", "list", "ruleset"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	askFrom := func(pid int, proto, host, port, sport string) string {
		argv := py("ask.py", proto, host, port, sport)
		if pid != 0 {
			argv = append([]string{"nsenter", "-t", strconv.Itoa(pid), "-n"}, argv...)
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s:%s from ns %d: %v\n%s\norigin rules:\n%s\n%s\n%s\nedge rules:\n%s", proto, host, port, pid, err, out,
				sh(t, "nft", "list", "ruleset"), sh(t, "ip", "rule"), sh(t, "ip", "route", "show", "table", "all"), in(t, edgeNS, "nft list ruleset; ip -s link show; ip route"))
		}
		return strings.TrimSpace(string(out))
	}
	// The host-network server and the published container both answer
	// through the edge, and both see the player's own address.
	for _, port := range []string{"27015", "27016"} {
		if got := askFrom(player, "udp", "203.0.113.1", port, "40000"); got != "198.51.100.10:40000" {
			t.Fatalf("udp %s: server saw %q, want the player's address", port, got)
		}
	}
	if got := askFrom(player, "tcp", "203.0.113.1", "27020", "0"); !strings.HasPrefix(got, "198.51.100.10:") {
		t.Fatalf("tcp 27020: server saw %q, want the player's address", got)
	}
	// The container's own traffic leaves through the edge.
	if got := askFrom(ctr, "udp", "198.51.100.10", "27900", "27031"); !strings.HasPrefix(got, "203.0.113.1:") {
		t.Fatalf("egress: master server saw %q, want the edge's address", got)
	}
	// The origin's own traffic still uses its router.
	if got := askFrom(0, "udp", "198.51.100.10", "27900", "0"); !strings.HasPrefix(got, "198.51.100.254:") {
		t.Fatalf("host traffic: master server saw %q, want the router's address", got)
	}

	// Kill switch: with the tunnel gone the container's outbound traffic
	// is refused instead of leaving through the router.
	sh(t, "ip", "link", "set", IfName("e1"), "down")
	if out, err := exec.Command("nsenter", append([]string{"-t", strconv.Itoa(ctr), "-n"}, py("ask.py", "udp", "198.51.100.10", "27900", "27032")...)...).CombinedOutput(); err == nil {
		t.Fatalf("egress with the tunnel down reached the master server: %s", out)
	}
	if err := eng.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if got := askFrom(ctr, "udp", "198.51.100.10", "27900", "27033"); !strings.HasPrefix(got, "203.0.113.1:") {
		t.Fatalf("egress after Ensure: master server saw %q", got)
	}

	st, _ = eng.Status(ctx)
	for _, f := range fwds {
		if st.Forwards["e1/"+f.ID].Packets == 0 {
			t.Fatalf("forward %s counted nothing: %+v", f.ID, st.Forwards)
		}
	}

	if err := eng.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if out := sh(t, "ip", "-4", "rule", "show"); strings.Contains(out, "5201") {
		t.Fatalf("rule left behind:\n%s", out)
	}
	if out := sh(t, "ip", "link", "show"); strings.Contains(out, IfPrefix) {
		t.Fatalf("tunnel left behind:\n%s", out)
	}
	if _, err := exec.Command("nft", "list", "table", "inet", TableName).CombinedOutput(); err == nil {
		t.Fatal("table left behind")
	}
}

// applyEdge applies spec inside the edge namespace (a second copy of this
// test binary) and returns the edge's public key.
func applyEdge(t *testing.T, pid int, dir string, spec Spec) string {
	t.Helper()
	raw, _ := json.Marshal(spec)
	path := filepath.Join(dir, "edge-spec.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-n", os.Args[0], "-test.run=^TestKernel$", "-test.v")
	cmd.Env = append(os.Environ(), "NDL_EDGE_STAGE=edge", "NDL_EDGE_SPEC="+path, "NDL_EDGE_DIR="+filepath.Join(dir, "edge"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("edge: %v\n%s", err, out)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if k, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "EDGEKEY="); ok {
			return k
		}
	}
	t.Fatalf("edge printed no key:\n%s", out)
	return ""
}

func kernelEdge(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("NDL_EDGE_SPEC"))
	if err != nil {
		t.Fatal(err)
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("NDL_EDGE_DIR")
	eng := &Engine{StateDir: dir, SysRoot: dir + "-root"}
	st, err := eng.Apply(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("EDGEKEY=" + st.Tunnels[spec.Tunnels[0].ID].PublicKey)
}
