package placement

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
)

func TestAutomaticLandsOnGPUNode(t *testing.T) {
	control := Candidate{Node: appdb.Node{ID: "n-a", Name: "local", Role: "control"}, Inventory: &inventory.Inventory{GPUs: nil}}
	worker := Candidate{Node: appdb.Node{ID: "n-b", Name: "box-b", Role: "worker"}, Inventory: &inventory.Inventory{GPUs: []inventory.GPU{{ID: "0000:01:00.0"}}}}
	res, err := Place(Request{Mode: ModeAutomatic, RequireGPU: true}, []Candidate{control, worker})
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeID != "n-b" {
		t.Fatalf("wanted GPU node, got %s", res.NodeID)
	}
}

func TestMaintenanceIsSkipped(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, Maintaining: true}
	res, err := Place(Request{Mode: ModeAutomatic}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-a" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSpecificNodeRefusesIneligible(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	_, err := Place(Request{Mode: ModeNode, NodeID: "n-b"}, []Candidate{a})
	if err == nil {
		t.Fatal("must not place on a missing node")
	}
}

func TestSpecificNodeDoesNotFallThrough(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, Maintaining: true}
	_, err := Place(Request{Mode: ModeNode, NodeID: "n-b"}, []Candidate{a, b})
	if err == nil {
		t.Fatal("must not start a copy on the other node")
	}
}

func TestAntiAffinitySkipsPartner(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}}
	res, err := Place(Request{Mode: ModeAutomatic, AntiAffinityNodeID: "n-a"}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRevokedNodeIsNotEligible(t *testing.T) {
	now := time.Now().UTC()
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: uuid.NewString(), Role: "worker", RevokedAt: &now}}
	res, err := Place(Request{Mode: ModeAutomatic}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-a" {
		t.Fatalf("%+v %v", res, err)
	}
}
