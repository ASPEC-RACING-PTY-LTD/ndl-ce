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

// The CodeHold release must survive the trip to the agent: without it every
// CodeHold CLI install fails.
func TestCTLifecycleCarriesTheCodeholdSource(t *testing.T) {
	in := lxc.LifecycleRequest{
		WorkloadID: "w", Action: lxc.ActionGuestSetup, Extras: []string{"codehold-cli"},
		Codehold: &lxc.CodeholdSource{Release: "https://codehold.dev/downloads/1.0.0", Sums: map[string]string{"ch-linux-amd64": "ab"}},
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
	if out.Codehold == nil || out.Codehold.Release != "https://codehold.dev/downloads/1.0.0" || out.Codehold.Sums["ch-linux-amd64"] != "ab" {
		t.Fatalf("the agent got no CodeHold source: %+v", out.Codehold)
	}
	if out.WorkloadID != "w" || len(out.Extras) != 1 {
		t.Fatalf("decoded %+v", out)
	}
	none, _ := ctLifecycleMsg(lxc.LifecycleRequest{WorkloadID: "w", Action: "start"})
	if r, _ := ctLifecycleRequest(none); r.Codehold != nil {
		t.Fatal("no source must stay no source")
	}
	tun, _ := ctLifecycleMsg(lxc.LifecycleRequest{WorkloadID: "w", Action: lxc.ActionTUNSet, TUN: true})
	wire, _ = proto.Marshal(tun)
	var tunBack agentv1.CTLifecycle
	_ = proto.Unmarshal(wire, &tunBack)
	if r, _ := ctLifecycleRequest(&tunBack); !r.TUN || r.Action != lxc.ActionTUNSet {
		t.Fatalf("TUN lost on the way to the agent: %+v", r)
	}
}
