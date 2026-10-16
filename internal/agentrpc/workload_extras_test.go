package agentrpc

import (
	"testing"

	"google.golang.org/protobuf/proto"

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

// The CodeHold address must survive the trip to the agent: without it every
// CodeHold CLI install failed with "no CodeHold address is configured".
func TestCTLifecycleCarriesTheCodeholdSource(t *testing.T) {
	in := lxc.LifecycleRequest{
		WorkloadID: "w", Action: lxc.ActionGuestSetup, Extras: []string{"codehold-cli"},
		Codehold: &lxc.CodeholdSource{URL: "https://codehold.example.com"},
	}
	msg, err := ctLifecycleMsg(in)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back agentv1.CTLifecycle
	if err := proto.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	out, err := ctLifecycleRequest(&back)
	if err != nil {
		t.Fatal(err)
	}
	if out.Codehold == nil || out.Codehold.URL != "https://codehold.example.com" {
		t.Fatalf("the agent got no CodeHold source: %+v", out.Codehold)
	}
	if out.WorkloadID != "w" || len(out.Extras) != 1 {
		t.Fatalf("decoded %+v", out)
	}
	none, _ := ctLifecycleMsg(lxc.LifecycleRequest{WorkloadID: "w", Action: "start"})
	if r, _ := ctLifecycleRequest(none); r.Codehold != nil {
		t.Fatal("no source must stay no source")
	}
}
