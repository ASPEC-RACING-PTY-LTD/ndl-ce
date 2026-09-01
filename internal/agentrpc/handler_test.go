package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/peercred"
)

func TestEnrollUnsupportedHost(t *testing.T) {
	h := &Handler{
		Ident: identity.Files{Dir: t.TempDir()},
		Lookup: func() (hostos.Platform, error) {
			return hostos.DetectFrom(strings.NewReader("ID=fedora\nVERSION_ID=42\n"), "x86_64")
		},
	}
	_, err := h.Enroll(context.Background(), connect.NewRequest(&agentv1.EnrollRequest{ClusterId: "c1"}))
	if err == nil {
		t.Fatal("fedora must not enroll")
	}
}

func TestEnrollStableNodeID(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{
		Ident: identity.Files{Dir: dir},
		Lookup: func() (hostos.Platform, error) {
			return hostos.DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
		},
	}
	first, err := h.Enroll(context.Background(), connect.NewRequest(&agentv1.EnrollRequest{ClusterId: "cluster-a"}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Enroll(context.Background(), connect.NewRequest(&agentv1.EnrollRequest{ClusterId: "cluster-a"}))
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.GetNodeId() != second.Msg.GetNodeId() {
		t.Fatal("node_id must survive re-enroll")
	}
}

func TestUnauthorizedPeer(t *testing.T) {
	h := &Handler{
		AllowedUID: 1000,
		Peer: func(context.Context) (peercred.Creds, error) {
			return peercred.Creds{UID: 7}, nil
		},
	}
	_, err := h.Hello(context.Background(), connect.NewRequest(&agentv1.HelloRequest{}))
	if err == nil {
		t.Fatal("expected deny")
	}
}

func TestHelloObservePing(t *testing.T) {
	h := &Handler{
		Lookup: func() (hostos.Platform, error) {
			return hostos.DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
		},
	}
	hello, err := h.Hello(context.Background(), connect.NewRequest(&agentv1.HelloRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if hello.Msg.GetHostPlatform().GetId() != "debian" {
		t.Fatal(hello.Msg.GetHostPlatform())
	}
	obs, err := h.Observe(context.Background(), connect.NewRequest(&agentv1.ObserveRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Msg.GetInventoryJson()) == 0 {
		t.Fatal("observe must return inventory json")
	}
	if _, err := h.GetInventory(context.Background(), connect.NewRequest(&agentv1.GetInventoryRequest{})); err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetMetrics(context.Background(), connect.NewRequest(&agentv1.GetMetricsRequest{})); err != nil {
		t.Fatal(err)
	}
	h.SkipHostCmds = true
	logs, err := h.GetLogs(context.Background(), connect.NewRequest(&agentv1.GetLogsRequest{Unit: "ndl-agent.service"}))
	if err != nil {
		t.Fatal(err)
	}
	if logs.Msg.GetStatus() != "unavailable" || len(logs.Msg.GetLines()) != 0 {
		t.Fatalf("skip must not invent logs: %+v", logs.Msg)
	}
	if _, err := h.GetLogs(context.Background(), connect.NewRequest(&agentv1.GetLogsRequest{Unit: "syslog.service"})); err == nil {
		t.Fatal("host syslog must be refused")
	}
	ping, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Ping{Ping: &agentv1.Ping{}},
	}))
	if err != nil || !ping.Msg.GetOk() {
		t.Fatal(err)
	}
}

func TestExecuteUnknownMethod(t *testing.T) {
	h := &Handler{}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{}))
	if err == nil {
		t.Fatal("empty execute must fail")
	}
}
