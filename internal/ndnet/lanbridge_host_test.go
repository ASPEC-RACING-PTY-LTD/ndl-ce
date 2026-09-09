package ndnet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDisableIfupdownIfaceKeepsOtherStanzas(t *testing.T) {
	in := `auto lo
iface lo inet loopback

allow-hotplug eth0
iface eth0 inet dhcp
    hostname box

auto eth1
iface eth1 inet static
    address 10.0.0.2/24
`
	out, changed := disableIfupdownIface(in, "eth0")
	if !changed {
		t.Fatal("expected eth0 stanza to be disabled")
	}
	if !strings.Contains(out, ifupdownMigratePrefix+"allow-hotplug eth0") {
		t.Fatalf("hotplug not migrated:\n%s", out)
	}
	if !strings.Contains(out, ifupdownMigratePrefix+"iface eth0 inet dhcp") {
		t.Fatalf("iface not migrated:\n%s", out)
	}
	if !strings.Contains(out, "iface lo inet loopback") || !strings.Contains(out, "iface eth1 inet static") {
		t.Fatalf("other stanzas must stay:\n%s", out)
	}
	if ifupdownMentions(out, "eth0") {
		t.Fatal("active eth0 stanza still present")
	}
}

func TestApplyLANBridgeMigratesIfupdownAndDhcpcd(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	if err := os.MkdirAll(e.etcPath("network"), 0755); err != nil {
		t.Fatal(err)
	}
	orig := "auto lo\niface lo inet loopback\n\nallow-hotplug eth0\niface eth0 inet dhcp\n"
	if err := os.WriteFile(e.etcPath("network", "interfaces"), []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.etcPath("dhcpcd.conf"), []byte("# dhcpcd\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.runPath("dhcpcd", "eth0.pid")), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.runPath("dhcpcd", "eth0.pid"), []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.etcPath("resolv.conf"), []byte("nameserver 1.1.1.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	}); err != nil {
		t.Fatal(err)
	}
	ifaces, _ := os.ReadFile(e.etcPath("network", "interfaces"))
	if ifupdownMentions(string(ifaces), "eth0") || !strings.Contains(string(ifaces), "iface lo inet loopback") {
		t.Fatalf("ifupdown migrate failed:\n%s", ifaces)
	}
	dhcpcd, _ := os.ReadFile(e.etcPath("dhcpcd.conf"))
	if !dhcpcdDenies(string(dhcpcd), "eth0") {
		t.Fatalf("dhcpcd deny missing:\n%s", dhcpcd)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "dhcpcd -k eth0") {
		t.Fatalf("dhcpcd release missing:\n%s", joined)
	}
	resolv, _ := os.ReadFile(e.etcPath("resolv.conf"))
	if !strings.Contains(string(resolv), "nameserver 1.1.1.1") {
		t.Fatalf("resolv.conf lost nameserver:\n%s", resolv)
	}
}

func TestApplyLANBridgeRefusesAdminNetworkdConflict(t *testing.T) {
	e := testEngine(t, testHost())
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.NetworkDir, "10-admin-eth0.network"), []byte("[Match]\nName=eth0\n\n[Network]\nDHCP=yes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err == nil || !strings.Contains(err.Error(), "10-admin-eth0.network") {
		t.Fatalf("expected admin networkd conflict, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(e.NetworkDir, "10-admin-eth0.network")); statErr != nil {
		t.Fatal("administrator networkd unit must not be deleted")
	}
}

func TestApplyLANBridgeRemovesStaleUplinkFiles(t *testing.T) {
	e := testEngine(t, testHost())
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := uuid.NewString()
	oldFiles := lanBridgeFiles(old, "ndlstale01", "eth0", testHost())
	for _, file := range oldFiles {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.NewString()
	if _, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.NetworkDir, persistName(old, "-uplink.network"))); !os.IsNotExist(err) {
		t.Fatal("stale uplink file must be removed")
	}
	if _, err := os.Stat(filepath.Join(e.NetworkDir, persistName(id, "-uplink.network"))); err != nil {
		t.Fatal("current uplink file must remain")
	}
}

func TestObserveSweepsOrphanUplinkFiles(t *testing.T) {
	keepID := uuid.NewString()
	orphan := uuid.NewString()
	e := testEngine(t, testHost())
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range lanBridgeFiles(orphan, "ndlorphan", "eth0", testHost()) {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range lanBridgeFiles(keepID, "ndlkeep01", "eth1", testHost()) {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.Observe(context.Background(), []Hint{{NetworkID: keepID, Kind: KindLANBridge, UplinkIfName: "eth1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.NetworkDir, persistName(orphan, "-uplink.network"))); !os.IsNotExist(err) {
		t.Fatal("orphan uplink files must be swept")
	}
	if _, err := os.Stat(filepath.Join(e.NetworkDir, persistName(keepID, "-uplink.network"))); err != nil {
		t.Fatal("cataloged uplink files must stay")
	}
}

func TestApplyLANBridgeIdempotentSkipsReload(t *testing.T) {
	id := uuid.NewString()
	bridge, err := BridgeName(id)
	if err != nil {
		t.Fatal(err)
	}
	host := testHost()
	host.Ifaces = append(host.Ifaces, Iface{Name: bridge, IfIndex: 9, Kind: "bridge", Addresses: []string{"192.168.1.10/24"}, HardwareAddr: "34:5a:60:6a:03:1f", Up: true})
	host.Ifaces[1].Master = bridge
	host.DefaultRouteIf = bridge
	host.ManagementIfName = bridge
	host.ManagementIfIndex = 9
	e := testEngine(t, host)
	plan, err := BuildPlan(Spec{NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0"}, host)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range plan.Files {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	res, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyApplied || res.Reason != "already applied" {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, "networkctl reload") {
		t.Fatalf("already-applied must not reload networkd:\n%s", joined)
	}
	if strings.Contains(joined, "systemctl start --no-block "+rollbackUnit) {
		t.Fatal("already-applied must not re-arm the watchdog")
	}
}

func TestDryRunReportsHostManagersWithoutWriting(t *testing.T) {
	e := testEngine(t, testHost())
	if err := os.MkdirAll(e.etcPath("network"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.etcPath("network", "interfaces"), []byte("allow-hotplug eth0\niface eth0 inet dhcp\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prev, err := e.DryRun(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !prev.DryRun || len(prev.HostManagers) == 0 {
		t.Fatalf("%+v", prev)
	}
	ifaces, _ := os.ReadFile(e.etcPath("network", "interfaces"))
	if !ifupdownMentions(string(ifaces), "eth0") {
		t.Fatal("dry-run must not migrate ifupdown")
	}
}

func TestFailedProbeRestoresIfupdown(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	e.Probe = func() error { return os.ErrInvalid }
	if err := os.MkdirAll(e.etcPath("network"), 0755); err != nil {
		t.Fatal(err)
	}
	orig := "allow-hotplug eth0\niface eth0 inet dhcp\n"
	if err := os.WriteFile(e.etcPath("network", "interfaces"), []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err == nil {
		t.Fatal("expected probe failure")
	}
	got, _ := os.ReadFile(e.etcPath("network", "interfaces"))
	if string(got) != orig {
		t.Fatalf("rollback must restore ifupdown:\n%s", got)
	}
}

func TestObserveHealsLANBridgeMACAndStaticAddress(t *testing.T) {
	id := uuid.NewString()
	bridge, err := BridgeName(id)
	if err != nil {
		t.Fatal(err)
	}
	host := testHost()
	host.Ifaces = append(host.Ifaces, Iface{
		Name: bridge, IfIndex: 9, Kind: "bridge", Addresses: []string{"192.168.1.10/24"},
		HardwareAddr: "f2:67:74:3b:4e:a1", Up: true,
	})
	host.Ifaces[1].Master = bridge
	host.DefaultRouteIf = bridge
	host.ManagementIfName = bridge
	host.ManagementIfIndex = 9
	e := testEngine(t, host)
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := "[NetDev]\nName=" + bridge + "\nKind=bridge\n"
	br := "[Match]\nName=" + bridge + "\n\n[Network]\nDHCP=yes\nKeepConfiguration=yes\n"
	up := "[Match]\nName=eth0\n\n[Network]\nBridge=" + bridge + "\n"
	for _, file := range []File{
		{RelPath: persistName(id, ".netdev"), Body: old},
		{RelPath: persistName(id, ".network"), Body: br},
		{RelPath: persistName(id, "-uplink.network"), Body: up},
	} {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	obs, err := e.Observe(context.Background(), []Hint{{NetworkID: id, Kind: KindLANBridge, UplinkIfName: "eth0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Networks) != 1 {
		t.Fatalf("%+v", obs)
	}
	netdev, _ := os.ReadFile(filepath.Join(e.NetworkDir, persistName(id, ".netdev")))
	network, _ := os.ReadFile(filepath.Join(e.NetworkDir, persistName(id, ".network")))
	uplink, _ := os.ReadFile(filepath.Join(e.NetworkDir, persistName(id, "-uplink.network")))
	if !strings.Contains(string(netdev), "MACAddress=34:5a:60:6a:03:1f") {
		t.Fatalf("heal must persist the uplink MAC:\n%s", netdev)
	}
	if !strings.Contains(string(network), "Address=192.168.1.10/24") || !strings.Contains(string(network), "Gateway=192.168.1.1") || strings.Contains(string(network), "DHCP=yes") {
		t.Fatalf("heal must persist the host address and disable bridge DHCP:\n%s", network)
	}
	if strings.Contains(string(uplink), "\nAddress=") || strings.Contains(string(uplink), "DHCP=") {
		t.Fatalf("uplink must stay unnumbered:\n%s", uplink)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "link set dev "+bridge+" address 34:5a:60:6a:03:1f") {
		t.Fatalf("heal must set the live bridge MAC:\n%s", joined)
	}
}

func TestApplyLANBridgeSetsLiveMACWhenAlreadyApplied(t *testing.T) {
	id := uuid.NewString()
	bridge, err := BridgeName(id)
	if err != nil {
		t.Fatal(err)
	}
	host := testHost()
	host.Ifaces = append(host.Ifaces, Iface{
		Name: bridge, IfIndex: 9, Kind: "bridge", Addresses: []string{"192.168.1.10/24"},
		HardwareAddr: "aa:bb:cc:dd:ee:ff", Up: true,
	})
	host.Ifaces[1].Master = bridge
	host.DefaultRouteIf = bridge
	host.ManagementIfName = bridge
	host.ManagementIfIndex = 9
	e := testEngine(t, host)
	plan, err := BuildPlan(Spec{NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0"}, host)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.NetworkDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range plan.Files {
		if err := os.WriteFile(filepath.Join(e.NetworkDir, file.RelPath), []byte(file.Body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	res, err := e.Apply(context.Background(), Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyApplied {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, "networkctl reload") {
		t.Fatalf("already-applied must not reload networkd:\n%s", joined)
	}
	if !strings.Contains(joined, "link set dev "+bridge+" address 34:5a:60:6a:03:1f") {
		t.Fatalf("already-applied must still enforce the uplink MAC:\n%s", joined)
	}
}
