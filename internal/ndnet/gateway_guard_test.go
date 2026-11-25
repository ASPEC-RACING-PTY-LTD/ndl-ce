package ndnet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guardNetID = "685ff937-0d43-41d0-baa8-c516e64015df"

// bridgedHost is a host whose uplink enp6s0 is enslaved to the management
// LAN-bridge; route is the interface the default route was read on.
func bridgedHost(route string) HostView {
	h := HostView{
		Ifaces: []Iface{
			{Name: "lo", IfIndex: 1, Kind: "loopback"},
			{Name: "enp6s0", IfIndex: 2, Kind: "device", Master: "ndl685ff937", HardwareAddr: "34:5a:60:6a:03:1f", Up: true},
			{Name: "ndl685ff937", IfIndex: 3, Kind: "bridge", Addresses: []string{"192.168.2.82/24"}, HardwareAddr: "34:5a:60:6a:03:1f", Up: true},
			{Name: "wlp7s0", IfIndex: 4, Kind: "device", Addresses: []string{"10.9.0.5/24"}, HardwareAddr: "02:00:00:00:00:44", Up: true},
		},
		Nameservers: []string{"192.168.2.1"},
	}
	h.DefaultRouteIf = route
	switch route {
	case "ndl685ff937":
		h.DefaultGateway = "192.168.2.1"
	case "eth0":
		h.DefaultGateway = "10.78.0.1"
	case "wlp7s0":
		h.DefaultGateway = "10.9.0.1"
	}
	return attachManagement(h)
}

func guardHint() Hint {
	return Hint{NetworkID: guardNetID, Kind: KindLANBridge, BridgeName: "ndl685ff937", UplinkIfName: "enp6s0"}
}

func bridgeNetwork(t *testing.T, e *Engine) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.NetworkDir, persistName(guardNetID, ".network")))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// healedEngine is an engine whose persisted files were written while the
// host was healthy, and whose host view is then switched to route.
func healedEngine(t *testing.T, route string) (*Engine, *[]string) {
	t.Helper()
	host := bridgedHost("ndl685ff937")
	e := testEngine(t, host)
	var calls []string
	e.Run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if _, err := e.healLANBridge(guardHint()); err != nil {
		t.Fatal(err)
	}
	if got := bridgeNetwork(t, e); !strings.Contains(got, "Gateway=192.168.2.1") {
		t.Fatalf("healthy heal must persist the gateway:\n%s", got)
	}
	calls = nil
	next := bridgedHost(route)
	e.Host = func() (HostView, error) { return next, nil }
	return e, &calls
}

// The outage: the agent read a container's routing table (default via
// eth0, which the host does not have) and rebuilt the bridge without
// Gateway=; the networkd reload then deleted the host's default route.
func TestHealKeepsGatewayWhenRouteIsReadFromAnotherNamespace(t *testing.T) {
	e, calls := healedEngine(t, "eth0")
	if _, err := e.healLANBridge(guardHint()); err != nil {
		t.Fatal(err)
	}
	if got := bridgeNetwork(t, e); !strings.Contains(got, "Gateway=192.168.2.1") || !strings.Contains(got, "DNS=192.168.2.1") {
		t.Fatalf("gateway was dropped:\n%s", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing changed, so networkd must not be reloaded: %v", *calls)
	}
}

func TestHealKeepsGatewayWhenDefaultRouteIsMissing(t *testing.T) {
	e, calls := healedEngine(t, "")
	if _, err := e.healLANBridge(guardHint()); err != nil {
		t.Fatal(err)
	}
	if got := bridgeNetwork(t, e); !strings.Contains(got, "Gateway=192.168.2.1") {
		t.Fatalf("gateway was dropped:\n%s", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("networkd must not be reloaded: %v", *calls)
	}
}

func TestApplyKeepsGatewayWhenDefaultRouteIsMissing(t *testing.T) {
	e, _ := healedEngine(t, "")
	res, err := e.Apply(context.Background(), Spec{NetworkID: guardNetID, Name: "lan", Kind: KindLANBridge, UplinkIfName: "enp6s0", ConfirmIfName: "enp6s0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := bridgeNetwork(t, e); !strings.Contains(got, "Gateway=192.168.2.1") {
		t.Fatalf("apply dropped the gateway:\n%s", got)
	}
	_ = res
}

// Moving the default route to another interface the host really has is
// the operator's choice; the bridge then stops claiming the gateway.
func TestHealDropsGatewayWhenRouteMovedToAnotherHostInterface(t *testing.T) {
	e, _ := healedEngine(t, "wlp7s0")
	if _, err := e.healLANBridge(guardHint()); err != nil {
		t.Fatal(err)
	}
	if got := bridgeNetwork(t, e); strings.Contains(got, "Gateway=") {
		t.Fatalf("gateway must follow the moved route:\n%s", got)
	}
}

func TestObserveWarnsWhenManagementDefaultRouteIsMissing(t *testing.T) {
	e, _ := healedEngine(t, "")
	obs, err := e.Observe(context.Background(), []Hint{guardHint()})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Networks) != 1 || obs.Networks[0].Status != StatusWarning || !strings.Contains(strings.Join(obs.Networks[0].Warnings, "\n"), "no default route") {
		t.Fatalf("expected a missing-route warning: %+v", obs.Networks)
	}
}

func TestDefaultIPv4RouteNeedsZeroMask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "route")
	body := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wg0\t00000000\t00000000\t0001\t0\t0\t0\t00000080\t0\t0\t0\n" +
		"ndl685ff937\t00000000\t0102A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	dev, gw := defaultIPv4Route(path)
	if dev != "ndl685ff937" || gw != "192.168.2.1" {
		t.Fatalf("got %s via %s", dev, gw)
	}
}
