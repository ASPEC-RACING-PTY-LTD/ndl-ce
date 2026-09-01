package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestLVMPoolNeverExportsAndSkipHostCmdsStaysUnavailableWhenMissing(t *testing.T) {
	h := &Handler{SkipHostCmds: true}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_LvmPool{LvmPool: &agentv1.LVMPool{
			Action: "send", Name: "ndlvg",
		}},
	}))
	if err == nil {
		t.Fatal("send")
	}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_LvmPool{LvmPool: &agentv1.LVMPool{
			Action: "observe", Name: "ndlvg",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.Msg.GetResultJson())
	if strings.Contains(body, `"status":"available"`) {
		t.Fatal(body)
	}
	if strings.Contains(body, "vgexport") {
		t.Fatal(body)
	}
	argv, err := storage.VGCreateArgv("ndlvg", []string{"/dev/disk/by-id/wwn-0x5000"})
	if err != nil || strings.Contains(strings.Join(argv, " "), "vgexport") {
		t.Fatal(argv, err)
	}
}
