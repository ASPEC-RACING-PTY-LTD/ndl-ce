package ndnet

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVLANAccessPortVID20(t *testing.T) {
	id := uuid.NewString()
	netID := uuid.NewString()
	bridge, err := BridgeName(netID)
	if err != nil {
		t.Fatal(err)
	}
	e := testEngine(t, testHost())
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionVLANAdd, ObjectID: id, NetworkID: netID, VID: 20,
		ParentIfName: bridge, BridgeName: bridge, Mode: VLANAccess, AccessIfName: "eth1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.VID != 20 || res.Status != StatusAvailable || res.RollbackArmed {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(res.Argv, " ")
	if !strings.Contains(joined, "vid 20") || !strings.Contains(joined, "pvid") || strings.Contains(joined, "bash") {
		t.Fatal(joined)
	}
	body := ""
	for _, f := range res.Files {
		body += f.Body
	}
	if !strings.Contains(body, "Id=20") || !strings.Contains(body, "Kind=vlan") {
		t.Fatal(body)
	}
}

func TestVLANOnManagementRequiresConfirmAndWatchdogWins(t *testing.T) {
	e := testEngine(t, testHost())
	e.Probe = func() error { return os.ErrInvalid }
	id := uuid.NewString()
	_, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionVLANAdd, ObjectID: id, VID: 20, ParentIfName: "eth0", AccessIfName: "eth0",
	})
	if err == nil || !strings.Contains(err.Error(), "typed interface") {
		t.Fatalf("expected confirm, got %v", err)
	}
	before, _ := os.ReadDir(e.networkDir())
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionVLANAdd, ObjectID: id, VID: 20, ParentIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err != nil && res.RolledBack {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !res.RollbackArmed {
		t.Fatal("watchdog must arm when VLAN touches management")
	}
	_ = before
}

func TestBondActiveBackupShown(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionBondAdd, ObjectID: id, Name: "uplink", Mode: BondActiveBackup, Members: []string{"eth1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Locator == "" || res.Mode != BondActiveBackup || res.Status != StatusAvailable {
		t.Fatalf("%+v", res)
	}
	body := ""
	for _, f := range res.Files {
		body += f.Body
	}
	if !strings.Contains(body, "Kind=bond") || !strings.Contains(body, "Mode=active-backup") {
		t.Fatal(body)
	}
	if res.RollbackArmed {
		t.Fatal("bonding extra NIC must not arm management rollback")
	}
}

func TestPolicyDeniesPairAndRefusesManagementINPUT(t *testing.T) {
	src := "02:00:00:00:00:01"
	dst := "02:00:00:00:00:02"
	rules, err := RenderBridgePolicy(uuid.NewString(), "deny", src, dst, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "table bridge") || !strings.Contains(rules, "drop") || !strings.Contains(rules, src) {
		t.Fatal(rules)
	}
	if strings.Contains(strings.ToLower(rules), "hook input") {
		t.Fatal(rules)
	}
	if err := RefuseManagementINPUT("table inet x { chain input { type filter hook input priority 0; policy drop; } }", "eth0"); err == nil {
		t.Fatal("management INPUT")
	}
	e := testEngine(t, testHost())
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionPolicyApply, ObjectID: uuid.NewString(), PolicyAction: "deny", SrcMAC: src, DstMAC: dst,
	})
	if err != nil || res.Status != StatusAvailable {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPolicyApplyRendersFullSetInOneTable(t *testing.T) {
	src := "02:00:00:00:00:01"
	mid := "02:00:00:00:00:02"
	dst := "02:00:00:00:00:03"
	idA := uuid.NewString()
	idB := uuid.NewString()
	e := testEngine(t, testHost())
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionPolicyApply, ObjectID: idA, PolicyAction: "deny", SrcMAC: src, DstMAC: mid,
		Policies: []PolicyRule{
			{ID: idA, Action: "deny", SrcMAC: src, DstMAC: mid},
			{ID: idB, Action: "deny", SrcMAC: src, DstMAC: dst},
		},
	})
	if err != nil || res.Status != StatusAvailable {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Count(res.NFT, "table bridge") != 1 {
		t.Fatalf("one table replace, got %s", res.NFT)
	}
	if !strings.Contains(res.NFT, src) || !strings.Contains(res.NFT, mid) || !strings.Contains(res.NFT, dst) {
		t.Fatalf("full set missing MACs: %s", res.NFT)
	}
	if !strings.Contains(res.NFT, "ndl-policy-"+idA) || !strings.Contains(res.NFT, "ndl-policy-"+idB) {
		t.Fatalf("full set missing policy comments: %s", res.NFT)
	}
}

func TestOverlayPrepRefusesInvalidVNI(t *testing.T) {
	e := testEngine(t, testHost())
	_, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionOverlayPrep, ObjectID: uuid.NewString(), OverlayVNI: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "overlay vni is invalid") {
		t.Fatalf("vni 0: %v", err)
	}
}

func TestOverlayIsPrepNotClusterFabric(t *testing.T) {
	e := testEngine(t, testHost())
	res, err := e.ApplyAdvanced(context.Background(), AdvancedOp{
		Action: ActionOverlayPrep, ObjectID: uuid.NewString(), OverlayVNI: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Reason, "Phase 30") {
		t.Fatal(res.Reason)
	}
}

func TestLANBridgeWatchdogStillWins(t *testing.T) {
	host := testHost()
	e := testEngine(t, host)
	e.Probe = func() error { return os.ErrInvalid }
	before, _ := os.ReadDir(e.networkDir())
	_, err := e.Apply(context.Background(), Spec{
		NetworkID: uuid.NewString(), Name: "lan", Kind: KindLANBridge, UplinkIfName: "eth0", ConfirmIfName: "eth0",
	})
	if err == nil {
		t.Fatal("expected probe failure")
	}
	after, _ := os.ReadDir(e.networkDir())
	if len(after) != len(before) {
		t.Fatalf("watchdog must restore: before=%d after=%d", len(before), len(after))
	}
}
