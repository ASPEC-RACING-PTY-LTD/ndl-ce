package ndnet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func testNATEngine(t *testing.T, host HostView, forward string) *Engine {
	t.Helper()
	e := testEngine(t, host)
	dir := filepath.Join(e.Root, "proc/sys/net/ipv4")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ip_forward"), []byte(forward+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return e
}

func natSpec(id string) Spec {
	return Spec{NetworkID: id, Name: "nat", Kind: KindIsolatedNAT, IPv4CIDR: "10.64.0.0/24"}
}

func TestIsolatedNATEnablesForwardingFromZero(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	id := uuid.NewString()
	res, err := e.Apply(context.Background(), natSpec(id))
	if err != nil {
		t.Fatal(err)
	}
	if !res.NAT || res.EgressIfName != "eth0" {
		t.Fatalf("%+v", res)
	}
	if e.readForwarding() != "1" {
		t.Fatal("ip_forward must become 1")
	}
	body, err := os.ReadFile(e.forwardSysctlPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "net.ipv4.ip_forward=1") {
		t.Fatalf("sysctl drop-in: %s", body)
	}
	entries, _ := os.ReadDir(e.NetworkDir)
	found := false
	for _, ent := range entries {
		b, _ := os.ReadFile(filepath.Join(e.NetworkDir, ent.Name()))
		if strings.Contains(string(b), "IPv4Forwarding=yes") && strings.Contains(string(b), "IPForward=yes") {
			found = true
		}
	}
	if !found {
		t.Fatal("networkd must persist IPv4 forwarding on the isolated bridge")
	}
}

func TestIsolatedNATKeepsExistingForwarding(t *testing.T) {
	e := testNATEngine(t, testHost(), "1")
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	if e.readForwarding() != "1" {
		t.Fatal("pre-existing ip_forward=1 must remain")
	}
	if _, err := os.Stat(e.forwardSysctlPath()); err == nil {
		t.Fatal("No-dal sysctl drop-in must be removed with the last isolated-nat")
	}
}

func TestIsolatedNATDoesNotFlushAdminNftables(t *testing.T) {
	host := testHost()
	e := testNATEngine(t, host, "0")
	admin := filepath.Join(e.Root, "etc/nftables.conf")
	if err := os.MkdirAll(filepath.Dir(admin), 0755); err != nil {
		t.Fatal(err)
	}
	adminBody := "#!/usr/sbin/nft -f\nflush ruleset\ntable inet filter {\n  chain forward { type filter hook forward priority 0; policy accept; }\n}\n"
	if err := os.WriteFile(admin, []byte(adminBody), 0644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, "flush ruleset") {
		t.Fatalf("must not flush administrator nftables:\n%s", joined)
	}
	if strings.Contains(joined, "delete table inet ndl ") || strings.HasSuffix(joined, "delete table inet ndl") {
		t.Fatalf("must not replace a shared inet ndl table:\n%s", joined)
	}
	got, err := os.ReadFile(admin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != adminBody {
		t.Fatal("administrator nftables.conf must be unchanged")
	}
	if !strings.Contains(joined, NFTBin+" -f ") {
		t.Fatalf("must load No-dal NAT rules:\n%s", joined)
	}
}

func TestIsolatedNATReapplyIsIdempotent(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	origin, _ := os.ReadFile(e.forwardOriginPath())
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	origin2, _ := os.ReadFile(e.forwardOriginPath())
	if string(origin) != string(origin2) {
		t.Fatal("forwarding origin must not be rewritten on reapply")
	}
	if e.readForwarding() != "1" {
		t.Fatal("reapply must keep forwarding enabled")
	}
	nft, err := os.ReadFile(filepath.Join(e.stateDir(), "nft", id+".nft"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nft), "masquerade") {
		t.Fatal(string(nft))
	}
	joined := strings.Join(calls, "\n")
	if strings.Count(joined, NFTBin+" -f ") < 2 {
		t.Fatalf("reapply must load nftables again:\n%s", joined)
	}
	if !strings.Contains(joined, "systemctl enable ndl-nat@"+id+".service") {
		t.Fatalf("NAT unit must be enabled across reboot:\n%s", joined)
	}
}

func TestIsolatedNATCleanupRemovesOwnedOnly(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	other := filepath.Join(e.sysctlDir(), "99-admin.conf")
	if err := os.MkdirAll(e.sysctlDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("net.ipv4.conf.all.log_martians=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	if e.readForwarding() != "0" {
		t.Fatal("ip_forward must restore to 0 when No-dal enabled it")
	}
	if _, err := os.Stat(e.forwardSysctlPath()); err == nil {
		t.Fatal("owned sysctl drop-in must be removed")
	}
	if _, err := os.Stat(filepath.Join(e.stateDir(), "nft", id+".nft")); err == nil {
		t.Fatal("owned nft persist file must be removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("administrator sysctl drop-in must remain")
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "ndl-nat@"+id+".service") {
		t.Fatalf("cleanup must stop the NAT unit:\n%s", joined)
	}
	if strings.Contains(joined, "flush ruleset") {
		t.Fatal("cleanup must not flush ruleset")
	}
	if strings.Contains(joined, "Host.Exec") {
		t.Fatal("must not use Host.Exec")
	}
}

func TestIsolatedNATDiscoversEgressFromDefaultRoute(t *testing.T) {
	for _, uplink := range []string{"enp3s0", "wg0", "vlan100"} {
		host := testHost()
		host.DefaultRouteIf = uplink
		host.ManagementIfName = uplink
		host.Ifaces = append(host.Ifaces, Iface{Name: uplink, IfIndex: 8, Kind: "device", Up: true, Addresses: []string{"203.0.113.10/24"}})
		e := testNATEngine(t, host, "0")
		id := uuid.NewString()
		res, err := e.Apply(context.Background(), natSpec(id))
		if err != nil {
			t.Fatal(err)
		}
		if res.EgressIfName != uplink {
			t.Fatalf("egress=%q want %q", res.EgressIfName, uplink)
		}
		nft, err := os.ReadFile(filepath.Join(e.stateDir(), "nft", id+".nft"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(nft)
		if !strings.Contains(text, `oifname "`+uplink+`"`) {
			t.Fatalf("NAT must masquerade out %s:\n%s", uplink, text)
		}
		if strings.Contains(text, "ens18") {
			t.Fatalf("must not hardcode ens18 when egress is %s:\n%s", uplink, text)
		}
		if !strings.Contains(text, "ip saddr 10.64.0.0/24") {
			t.Fatal(text)
		}
		if !strings.Contains(text, `iifname "`+res.BridgeName+`"`) {
			t.Fatal(text)
		}
		if !strings.Contains(text, "destroy table inet "+NATTableName(res.BridgeName)) {
			t.Fatal(text)
		}
	}
}

func TestIsolatedNATRequiresDefaultRoute(t *testing.T) {
	host := testHost()
	host.DefaultRouteIf = ""
	e := testNATEngine(t, host, "0")
	_, err := e.Apply(context.Background(), natSpec(uuid.NewString()))
	if err == nil || !strings.Contains(err.Error(), "default IPv4 route") {
		t.Fatalf("expected egress discovery error, got %v", err)
	}
	if _, err := os.Stat(e.forwardSysctlPath()); err == nil {
		t.Fatal("failed plan must not write sysctl")
	}
}

func TestIsolatedNATPartialApplyRollsBackOwned(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	e.Run = func(_ context.Context, name string, args ...string) error {
		if name == NFTBin && len(args) > 0 && args[0] == "-f" {
			return os.ErrInvalid
		}
		return nil
	}
	id := uuid.NewString()
	_, err := e.Apply(context.Background(), natSpec(id))
	if err == nil {
		t.Fatal("expected nft apply failure")
	}
	if e.readForwarding() != "0" {
		t.Fatal("failed apply must restore ip_forward")
	}
	if _, err := os.Stat(e.forwardSysctlPath()); err == nil {
		t.Fatal("failed apply must remove owned sysctl")
	}
	if _, err := os.Stat(filepath.Join(e.stateDir(), "nft", id+".nft")); err == nil {
		t.Fatal("failed apply must remove owned nft persist")
	}
}

func TestIsolatedNATSecondNetworkKeepsForwarding(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	a, b := uuid.NewString(), uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(a)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), natSpec(b)); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(context.Background(), natSpec(a)); err != nil {
		t.Fatal(err)
	}
	if e.readForwarding() != "1" {
		t.Fatal("forwarding must remain while another isolated-nat exists")
	}
	if _, err := os.Stat(e.forwardSysctlPath()); err != nil {
		t.Fatal("sysctl drop-in must remain while another isolated-nat exists")
	}
	if _, err := os.Stat(filepath.Join(e.stateDir(), "nft", b+".nft")); err != nil {
		t.Fatal("remaining NAT rules must stay")
	}
	if err := e.Delete(context.Background(), natSpec(b)); err != nil {
		t.Fatal(err)
	}
	if e.readForwarding() != "0" {
		t.Fatal("last isolated-nat must restore ip_forward")
	}
}

func TestIsolatedDoesNotEnableForwardingOrNAT(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	res, err := e.Apply(context.Background(), Spec{NetworkID: id, Name: "iso", Kind: KindIsolated})
	if err != nil {
		t.Fatal(err)
	}
	if res.NAT {
		t.Fatal("isolated must not enable NAT")
	}
	if e.readForwarding() != "0" {
		t.Fatal("isolated must not enable ip_forward")
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, NFTBin) || strings.Contains(joined, "ndl-nat@") {
		t.Fatalf("isolated must not apply NAT:\n%s", joined)
	}
}

func TestRestoreNATReappliesForwardingAndRules(t *testing.T) {
	e := testNATEngine(t, testHost(), "0")
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), natSpec(id)); err != nil {
		t.Fatal(err)
	}
	if err := e.writeForwarding("0"); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := e.RestoreNAT(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.readForwarding() != "1" {
		t.Fatal("restore must enable ip_forward")
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, NFTBin+" -f ") {
		t.Fatalf("restore must load persisted NAT rules:\n%s", joined)
	}
}

func TestRenderNFTDoesNotHardcodeEns18(t *testing.T) {
	raw, err := os.ReadFile("nft.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ens18") {
		t.Fatal("nft renderer must not hardcode ens18")
	}
}

func TestIsolatedNATPlanUsesHostDefaultRoute(t *testing.T) {
	host := testHost()
	host.DefaultRouteIf = "enp1s0"
	id := uuid.NewString()
	plan, err := BuildPlan(natSpec(id), host)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EgressIfName != "enp1s0" {
		t.Fatalf("egress=%q", plan.EgressIfName)
	}
	if !strings.Contains(plan.NFT, `oifname "enp1s0"`) || strings.Contains(plan.NFT, "ens18") {
		t.Fatal(plan.NFT)
	}
	joined := ""
	for _, file := range plan.Files {
		joined += file.Body
	}
	if !strings.Contains(joined, "IPv4Forwarding=yes") {
		t.Fatal(joined)
	}
}

// An isolated-nat network reaches the internet, not the host's services or
// private ranges, while DHCP, DNS and replies keep working.
func TestIsolatedNATKeepsContainersOffTheLAN(t *testing.T) {
	out := renderNFT(Plan{NAT: true, IPv4CIDR: "10.77.0.0/24", BridgeName: "ndlce8c0536", EgressIfName: "ndl685ff937"})
	for _, want := range []string{
		`iifname "ndlce8c0536" ip daddr != 10.77.0.0/24 ip daddr { 10.0.0.0/8, 100.64.0.0/10, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16 } ct state new counter drop`,
		`iifname "ndlce8c0536" udp dport { 53, 67 } accept`,
		`iifname "ndlce8c0536" ct state established,related accept`,
		`iifname "ndlce8c0536" counter drop`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "counter drop\n  }\n  chain forward") < 0 {
		t.Errorf("the host drop must end the input chain:\n%s", out)
	}
	if _, err := exec.LookPath("nft"); err != nil || os.Geteuid() != 0 {
		return
	}
	// The script loads, in a throwaway network namespace.
	cmd := exec.Command("unshare", "-n", "sh", "-c", "ip link add ndlce8c0536 type dummy; ip link add ndl685ff937 type dummy; nft -f -")
	cmd.Stdin = strings.NewReader(out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft refused the rules: %v\n%s", err, b)
	}
}

// A table saved by an older version is re-rendered at agent start, so
// existing networks get the LAN isolation too.
func TestRestoreNATUpgradesOldTables(t *testing.T) {
	old := `destroy table inet ndl_nat_ce8c0536
table inet ndl_nat_ce8c0536 {
  chain forward {
    type filter hook forward priority -10; policy accept;
    iifname "ndlce8c0536" oifname "ndl685ff937" ip saddr 10.77.0.0/24 accept
    iifname "ndl685ff937" oifname "ndlce8c0536" ip daddr 10.77.0.0/24 ct state established,related accept
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr 10.77.0.0/24 oifname "ndl685ff937" masquerade
  }
}
`
	path := filepath.Join(t.TempDir(), "n.nft")
	_ = os.WriteFile(path, []byte(old), 0o600)
	e := &Engine{}
	e.upgradeNATFile(path)
	got, _ := os.ReadFile(path)
	want := renderNFT(Plan{NAT: true, IPv4CIDR: "10.77.0.0/24", BridgeName: "ndlce8c0536", EgressIfName: "ndl685ff937"})
	if string(got) != want || !strings.Contains(want, "ct state new counter drop") {
		t.Fatalf("not upgraded:\n%s", got)
	}
	e.upgradeNATFile(path)
	if again, _ := os.ReadFile(path); string(again) != want {
		t.Fatal("upgrade is not stable")
	}
	_ = os.WriteFile(path, []byte("table inet other {}\n"), 0o600)
	e.upgradeNATFile(path)
	if kept, _ := os.ReadFile(path); string(kept) != "table inet other {}\n" {
		t.Fatal("an unknown file was rewritten")
	}
}
