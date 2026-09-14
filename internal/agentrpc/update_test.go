package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/hostos"
)

func TestHostUpdateUnsupportedHost(t *testing.T) {
	h := &Handler{
		Lookup: func() (hostos.Platform, error) {
			return hostos.DetectFrom(strings.NewReader("ID=ubuntu\nVERSION_ID=24.04\nPRETTY_NAME=\"Ubuntu\"\n"), "x86_64")
		},
	}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostUpdate{HostUpdate: &agentv1.HostUpdate{Action: "check", DryRun: true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.Msg.GetResultJson())
	if !strings.Contains(body, `"supported":false`) {
		t.Fatalf("%s", body)
	}
	if strings.Contains(strings.ToLower(body), "apt-get") || strings.Contains(strings.ToLower(body), "dpkg") {
		t.Fatalf("leaked package manager verb: %s", body)
	}
}

func TestHostUpdateDebianSkipHostCmds(t *testing.T) {
	h := &Handler{
		SkipHostCmds: true,
		Lookup: func() (hostos.Platform, error) {
			return hostos.DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
		},
	}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostUpdate{HostUpdate: &agentv1.HostUpdate{Action: "apply", DryRun: true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.Msg.GetResultJson())
	if !strings.Contains(body, `"supported":true`) {
		t.Fatalf("%s", body)
	}
	if strings.Contains(strings.ToLower(body), "stop") {
		t.Fatalf("apply must not mention stopping guests: %s", body)
	}
}
