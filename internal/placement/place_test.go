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

func TestAffinityPrefersNamedNode(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}, Inventory: &inventory.Inventory{GPUs: []inventory.GPU{{ID: "0000:01:00.0"}}}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}}
	res, err := Place(Request{Mode: ModeAutomatic, AffinityNodeID: "n-b"}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-b" || res.Reason != "affinity" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestStorageClassSkipsOtherBackend(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}}
	res, err := Place(Request{
		Mode:                ModeAutomatic,
		RequireStorageClass: "zfs",
		Pools: []appdb.StoragePool{
			{NodeID: "n-a", BackendType: "dir", Name: "local"},
			{NodeID: "n-b", BackendType: "zfs", Name: "tank"},
		},
	}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCPUFreeSkipsNodeThatCannotFit(t *testing.T) {
	tight := Candidate{Node: appdb.Node{ID: "n-a", Role: "worker"}, CPUFree: 1}
	wide := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, CPUFree: 8}
	res, err := Place(Request{Mode: ModeAutomatic, CPUs: 4}, []Candidate{tight, wide})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestMemoryFreeSkipsNodeThatCannotFit(t *testing.T) {
	tight := Candidate{Node: appdb.Node{ID: "n-a", Role: "worker"}, MemoryFree: 64 << 20}
	wide := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, MemoryFree: 4 << 30}
	res, err := Place(Request{Mode: ModeAutomatic, MemoryBytes: 1 << 30}, []Candidate{tight, wide})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUnknownFreeCapacityStaysEligible(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	res, err := Place(Request{Mode: ModeAutomatic, CPUs: 2, MemoryBytes: 1 << 30}, []Candidate{a})
	if err != nil || res.NodeID != "n-a" {
		t.Fatalf("unset CPUFree/MemoryFree must stay eligible: %+v %v", res, err)
	}
}

func TestPriorityTieBreakPrefersLowerNumber(t *testing.T) {
	late := Candidate{Node: appdb.Node{ID: "n-a", Role: "worker"}, Priority: 20}
	early := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, Priority: 1}
	res, err := Place(Request{Mode: ModeAutomatic}, []Candidate{late, early})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRequestPriorityTieBreakPrefersLowerNumber(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "worker"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}}
	res, err := Place(Request{Mode: ModeAutomatic, Priority: 3}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-a" {
		t.Fatalf("equal request priority keeps first: %+v %v", res, err)
	}
	res, err = Place(Request{Mode: ModeAutomatic, Priority: 3}, []Candidate{
		{Node: appdb.Node{ID: "n-a", Role: "worker"}, Priority: 9},
		{Node: appdb.Node{ID: "n-b", Role: "worker"}},
	})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("unset candidate priority falls back to request: %+v %v", res, err)
	}
}

func TestUnhealthyCandidateIsSkipped(t *testing.T) {
	sick := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}, HealthOK: false}
	ok := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, HealthOK: true}
	res, err := Place(Request{Mode: ModeAutomatic}, []Candidate{sick, ok})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUnknownHealthStaysEligible(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}}
	res, err := Place(Request{Mode: ModeAutomatic}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-a" {
		t.Fatalf("unknown health must not skip: %+v %v", res, err)
	}
}

func TestNetworkIDSkipsNodeWithoutNetwork(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}, Networks: []string{"net-other"}}
	b := Candidate{Node: appdb.Node{ID: "n-b", Role: "worker"}, Networks: []string{"net-guest", "net-other"}}
	res, err := Place(Request{Mode: ModeAutomatic, NetworkID: "net-guest"}, []Candidate{a, b})
	if err != nil || res.NodeID != "n-b" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNetworkIDRefusesWhenNoCandidateHasIt(t *testing.T) {
	a := Candidate{Node: appdb.Node{ID: "n-a", Role: "control"}}
	_, err := Place(Request{Mode: ModeAutomatic, NetworkID: "net-guest"}, []Candidate{a})
	if err == nil {
		t.Fatal("must not place when NetworkID is missing from candidate Networks")
	}
}
