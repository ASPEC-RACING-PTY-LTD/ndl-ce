package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestZFSPoolImportNeverForce(t *testing.T) {
	h := &Handler{SkipHostCmds: true}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ZfsPool{ZfsPool: &agentv1.ZFSPool{
			Action: "import", Guid: "1234567890", Name: "tank", Force: true,
		}},
	}))
	if err == nil {
		t.Fatal("force")
	}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ZfsPool{ZfsPool: &agentv1.ZFSPool{
			Action: "import", Guid: "1234567890", Name: "tank",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetOk() {
		t.Fatal(res.Msg.GetMessage())
	}
	argv, err := storage.ZFSImportArgv("1234567890")
	if err != nil || strings.Contains(strings.Join(argv, " "), "-f") {
		t.Fatal(argv, err)
	}
}

func TestZFSPoolSkipHostCmdsDoesNotFakeMissingEngine(t *testing.T) {
	h := &Handler{SkipHostCmds: true, ZFS: &storage.ZFSEngine{SkipHostCmds: true}}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ZfsPool{ZfsPool: &agentv1.ZFSPool{
			Action: "observe", Name: "tank",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Msg.GetResultJson()), `"status":"available"`) && strings.Contains(string(res.Msg.GetResultJson()), "not installed") {
		t.Fatal(string(res.Msg.GetResultJson()))
	}
}
