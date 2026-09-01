package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestDatastoreSkipHostCmdsShareDownAndNoPasswordArgv(t *testing.T) {
	h := &Handler{SkipHostCmds: true}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Datastore{Datastore: &agentv1.Datastore{
			Action: "observe", Kind: "nfs", PoolId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			Locator: "nas.example:/export",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.Msg.GetResultJson())
	if strings.Contains(body, `"status":"available"`) {
		t.Fatal(body)
	}
	argv, err := storage.SMBMountArgv("files.example", "iso", storage.CredDir+"/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.cred", storage.SMBMountRoot+"/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil || strings.Contains(strings.Join(argv, " "), "password=") {
		t.Fatal(argv, err)
	}
}
