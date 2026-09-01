package ndnet

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIsolatedPlanConfiguresCarrierlessBridge(t *testing.T) {
	id := uuid.NewString()
	plan, err := BuildPlan(Spec{NetworkID: id, Name: "iso", Kind: KindIsolated}, testHost())
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, file := range plan.Files {
		joined += file.Body
	}
	for _, want := range []string{
		"ConfigureWithoutCarrier=yes",
		"IgnoreCarrierLoss=yes",
		"ActivationPolicy=always-up",
		"Address=10.64.0.1/24",
		"Name=" + plan.BridgeName,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if !strings.Contains(plan.Dnsmasq, "listen-address=10.64.0.1") {
		t.Fatalf("dnsmasq must bind the isolated gateway:\n%s", plan.Dnsmasq)
	}
	if !strings.Contains(plan.Dnsmasq, "interface="+plan.BridgeName) {
		t.Fatal("dnsmasq must name the isolated bridge")
	}
	if strings.Contains(plan.Dnsmasq, "eth0") {
		t.Fatal("dnsmasq must not mention the management NIC")
	}
}

func TestLANBridgePlanHasNoDHCP(t *testing.T) {
	id := uuid.NewString()
	plan, err := BuildPlan(Spec{
		NetworkID: id, Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth1",
	}, testHost())
	if err != nil {
		t.Fatal(err)
	}
	if plan.DHCP || plan.Dnsmasq != "" {
		t.Fatalf("LAN-bridge must not emit DHCP: %+v", plan)
	}
}
