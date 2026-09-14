package ndnet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testEngine(t *testing.T, host HostView) *Engine {
	t.Helper()
	root := t.TempDir()
	return &Engine{
		Root:         root,
		NetworkDir:   filepath.Join(root, "etc/systemd/network"),
		StateDir:     filepath.Join(root, "var/lib/ndl/net"),
		Host:         func() (HostView, error) { return host, nil },
		SkipHostCmds: true,
	}
}

func TestApplyIsolatedLeavesManagementIfindex(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	id := uuid.NewString()
	res, err := e.Apply(context.Background(), Spec{NetworkID: id, Name: "iso", Kind: KindIsolated})
	if err != nil {
		t.Fatal(err)
	}
	if res.ManagementIfIndex != 2 {
		t.Fatalf("management ifindex=%d", res.ManagementIfIndex)
	}
	if !res.DHCP {
		t.Fatal("isolated must run DHCP")
	}
	entries, _ := os.ReadDir(e.NetworkDir)
	if len(entries) == 0 {
		t.Fatal("expected networkd files")
	}
	for _, ent := range entries {
		if !strings.HasPrefix(ent.Name(), "50-ndl-") {
			t.Fatalf("unexpected file %s", ent.Name())
		}
	}
	obs, err := e.Observe(context.Background(), []Hint{{NetworkID: id, Kind: KindIsolated, BridgeName: res.BridgeName}})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ManagementIfIndex != 2 {
		t.Fatalf("observe ifindex=%d", obs.ManagementIfIndex)
	}
}

func TestApplyLANBridgeRequiresTypedConfirm(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	_, err := e.Apply(context.Background(), Spec{NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0"})
	if err == nil || !strings.Contains(err.Error(), "typed interface") {
		t.Fatalf("expected typed confirm, got %v", err)
	}
	res, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DHCP {
		t.Fatal("LAN-bridge must not enable DHCP")
	}
}

func TestFailedProbeRollsBack(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	e.Probe = func() error { return os.ErrInvalid }
	id := uuid.NewString()
	before, _ := os.ReadDir(e.networkDir())
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err == nil {
		t.Fatal("expected probe failure")
	}
	after, _ := os.ReadDir(e.networkDir())
	if len(after) != len(before) {
		t.Fatalf("rollback left files: before=%d after=%d", len(before), len(after))
	}
}

func TestObserveIsolatedDownBridgeIsWarning(t *testing.T) {
	id := uuid.NewString()
	bridge, err := BridgeName(id)
	if err != nil {
		t.Fatal(err)
	}
	host := testHost()
	host.Ifaces = append(host.Ifaces, Iface{Name: bridge, IfIndex: 9, Kind: "bridge", Up: false})
	e := testEngine(t, host)
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.NetworkDir, persistName(id, ".netdev")), []byte("[NetDev]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	obs, err := e.Observe(context.Background(), []Hint{{NetworkID: id, Kind: KindIsolated, BridgeName: bridge}})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Networks[0].Status != StatusWarning {
		t.Fatalf("down isolated bridge must be warning: %+v", obs.Networks[0])
	}
}

func TestObserveMissingIsUnavailableNotDeleted(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	obs, err := e.Observe(context.Background(), []Hint{{NetworkID: id, Kind: KindIsolated}})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Networks) != 1 || obs.Networks[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	prev, err := e.DryRun(context.Background(), Spec{NetworkID: id, Name: "iso", Kind: KindIsolated})
	if err != nil {
		t.Fatal(err)
	}
	if !prev.DryRun || !prev.DHCP {
		t.Fatalf("%+v", prev)
	}
	if entries, _ := os.ReadDir(e.networkDir()); len(entries) != 0 {
		t.Fatal("dry-run wrote files")
	}
}

func TestWatchdogRemainsArmedAfterLANBridge(t *testing.T) {
	var calls []string
	e := testEngine(t, testHost())
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "systemctl start --no-block "+rollbackUnit) {
		t.Fatalf("watchdog must start:\n%s", joined)
	}
	if strings.Contains(joined, "systemctl stop "+rollbackUnit) {
		t.Fatal("apply must not stop the 120s watchdog")
	}
	if _, err := os.Stat(e.okPath()); err == nil {
		t.Fatal("apply must not write active.ok before the probe window ends")
	}
}

func TestIsolatedEnablesDnsmasqUnit(t *testing.T) {
	var calls []string
	e := testEngine(t, testHost())
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), Spec{NetworkID: id, Name: "iso", Kind: KindIsolated}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "systemctl enable ndl-dnsmasq@"+id+".service") {
		t.Fatalf("isolated DHCP must be enabled across reboot:\n%s", joined)
	}
	if !strings.Contains(joined, "systemctl start ndl-dnsmasq@"+id+".service") {
		t.Fatalf("isolated DHCP must start:\n%s", joined)
	}
}

func TestProbeAcceptsAddressesMovedOntoBridge(t *testing.T) {
	host := testHost()
	host.Ifaces = []Iface{
		{Name: "lo", IfIndex: 1, Kind: "loopback"},
		{Name: "eth0", IfIndex: 2, Kind: "device", Master: "ndl40a3c2d5", Up: true},
		{Name: "ndl40a3c2d5", IfIndex: 9, Kind: "bridge", Addresses: []string{"192.168.1.10/24"}, Up: true},
	}
	host.DefaultRouteIf = "ndl40a3c2d5"
	host.ManagementAddresses = []string{"192.168.1.10/24"}
	if err := ProbeManagement(host, "eth0", 2, "192.168.1.10/24"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverStaleRestartsWatchdogBeforeDeadline(t *testing.T) {
	var calls []string
	e := testEngine(t, testHost())
	e.Now = func() time.Time { return time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC) }
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if _, err := e.armRollback(Plan{
		NetworkID: uuid.NewString(), ManagementIfName: "eth0", ManagementIfIndex: 2,
	}, testHost()); err != nil {
		t.Fatal(err)
	}
	if err := e.RecoverStale(e.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "systemctl start --no-block "+rollbackUnit) {
		t.Fatalf("open probe window must restart watchdog:\n%s", strings.Join(calls, "\n"))
	}
}

func TestWatchdogStartsWithoutBlocking(t *testing.T) {
	var calls []string
	e := testEngine(t, testHost())
	e.SkipHostCmds = false
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "systemctl start --no-block "+rollbackUnit) {
		t.Fatalf("watchdog start must be non-blocking:\n%s", joined)
	}
	if strings.Contains(joined, "/usr/sbin/nft") {
		t.Fatal("LAN-bridge must not apply nftables")
	}
}

func TestDefaultProbeRollsBackDangerousApply(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	e.Host = func() (HostView, error) {
		lost := host
		lost.Ifaces = []Iface{{Name: "lo", IfIndex: 1, Kind: "loopback"}}
		lost.ManagementIfName = "eth0"
		lost.ManagementIfIndex = 2
		lost.ManagementAddresses = nil
		return lost, nil
	}
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err == nil {
		t.Fatal("expected default probe failure")
	}
}

func TestConfirmTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	tok := ConfirmToken("secret", "user", KindLANBridge, "eth0", now)
	if !ValidConfirm("secret", "user", KindLANBridge, "eth0", tok, now) {
		t.Fatal("token rejected")
	}
	if ValidConfirm("secret", "user", KindLANBridge, "eth1", tok, now) {
		t.Fatal("token accepted for wrong ifname")
	}
}
