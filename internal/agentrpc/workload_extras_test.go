package agentrpc

import (
	"testing"

	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func TestCTLifecycleForwardsGuestSetupExtras(t *testing.T) {
	req := lxc.LifecycleRequest{WorkloadID: "w", Action: lxc.ActionGuestSetup, Extras: []string{"docker", "git"}}
	msg := &agentv1.CTLifecycle{
		WorkloadId: req.WorkloadID, Action: req.Action, Extras: append([]string{}, req.Extras...),
	}
	if got := msg.GetExtras(); len(got) != 2 || got[0] != "docker" || got[1] != "git" {
		t.Fatalf("proto extras %v", got)
	}
	decoded := lxc.LifecycleRequest{
		WorkloadID: msg.GetWorkloadId(), Action: msg.GetAction(), Extras: append([]string{}, msg.GetExtras()...),
	}
	if decoded.Action != lxc.ActionGuestSetup || len(decoded.Extras) != 2 {
		t.Fatalf("decoded %+v", decoded)
	}
}
