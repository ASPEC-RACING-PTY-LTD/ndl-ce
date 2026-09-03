package ndnet

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func testHost() HostView {
	return HostView{
		Ifaces: []Iface{
			{Name: "lo", IfIndex: 1, Kind: "loopback"},
			{Name: "eth0", IfIndex: 2, Kind: "device", Addresses: []string{"192.168.1.10/24"}, Up: true},
			{Name: "eth1", IfIndex: 3, Kind: "device", Up: true},
		},
		DefaultRouteIf:      "eth0",
		ManagementIfIndex:   2,
		ManagementIfName:    "eth0",
		ManagementAddresses: []string{"192.168.1.10/24"},
	}
}

func TestClassifyIsolatedIsSafe(t *testing.T) {
	c := Classify(Spec{Kind: KindIsolated, NetworkID: uuid.NewString()}, testHost())
	if c.Danger != DangerSafe || c.RequiresConfirm {
		t.Fatalf("%+v", c)
	}
	c = Classify(Spec{Kind: KindIsolatedNAT, NetworkID: uuid.NewString()}, testHost())
	if c.Danger != DangerSafe {
		t.Fatalf("nat: %+v", c)
	}
}

func TestClassifyLANBridgeManagementIsDangerous(t *testing.T) {
	c := Classify(Spec{Kind: KindLANBridge, UplinkIfName: "eth0"}, testHost())
	if c.Danger != DangerDangerous || !c.RequiresConfirm || c.TypedIfName != "eth0" {
		t.Fatalf("%+v", c)
	}
}

func TestClassifyLANBridgeOtherNICIsWarning(t *testing.T) {
	c := Classify(Spec{Kind: KindLANBridge, UplinkIfName: "eth1"}, testHost())
	if c.Danger != DangerWarning || c.RequiresConfirm {
		t.Fatalf("%+v", c)
	}
}

func TestClassifySingleNICEnslaveIsDangerous(t *testing.T) {
	host := HostView{
		Ifaces:            []Iface{{Name: "enp1s0", IfIndex: 2, Kind: "device"}},
		DefaultRouteIf:    "enp1s0",
		ManagementIfIndex: 2,
		ManagementIfName:  "enp1s0",
	}
	c := Classify(Spec{Kind: KindLANBridge, UplinkIfName: "enp1s0"}, host)
	if c.Danger != DangerDangerous || !c.SingleNIC {
		t.Fatalf("%+v", c)
	}
}

func TestBuildPlanIsolatedHasDHCPAndNoUplink(t *testing.T) {
	id := uuid.NewString()
	plan, err := BuildPlan(Spec{NetworkID: id, Name: "guests", Kind: KindIsolated}, testHost())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DHCP || plan.UplinkIfName != "" || !strings.Contains(plan.Dnsmasq, "interface="+plan.BridgeName) {
		t.Fatalf("%+v", plan)
	}
	if strings.Contains(plan.Dnsmasq, "eth0") {
		t.Fatal("dnsmasq must not bind the management NIC")
	}
}

func TestBuildPlanLANBridgeNeverDHCP(t *testing.T) {
	id := uuid.NewString()
	plan, err := BuildPlan(Spec{NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth1"}, testHost())
	if err != nil {
		t.Fatal(err)
	}
	if plan.DHCP || plan.Dnsmasq != "" {
		t.Fatalf("LAN-bridge must not run DHCP: %+v", plan)
	}
	joined := ""
	for _, f := range plan.Files {
		joined += f.Body
	}
	if !strings.Contains(joined, "Bridge="+plan.BridgeName) {
		t.Fatal("uplink must join the bridge")
	}
}

func TestBuildPlanRejectsManagementOverlap(t *testing.T) {
	_, err := BuildPlan(Spec{
		NetworkID: uuid.NewString(), Kind: KindIsolated, IPv4CIDR: "192.168.1.0/24",
	}, testHost())
	if err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestBuildPlanRejectsReservationOutsideSubnet(t *testing.T) {
	id := uuid.NewString()
	_, err := BuildPlan(Spec{
		NetworkID: id, Kind: KindIsolated, IPv4CIDR: "10.64.0.0/24",
		Reservations: []Reservation{{MAC: "02:00:00:00:00:01", IPv4: "8.8.8.8"}},
	}, testHost())
	if err == nil || !strings.Contains(err.Error(), "reservation ipv4 is outside the isolated subnet") {
		t.Fatalf("outside: %v", err)
	}
	_, err = BuildPlan(Spec{
		NetworkID: id, Kind: KindIsolated, IPv4CIDR: "10.64.0.0/24",
		Reservations: []Reservation{{MAC: "02:00:00:00:00:01", IPv4: "10.64.0.1"}},
	}, testHost())
	if err == nil || !strings.Contains(err.Error(), "reservation ipv4 is reserved") {
		t.Fatalf("gateway: %v", err)
	}
}
