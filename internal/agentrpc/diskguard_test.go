package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/diskguard"
)

// criticalGuard watches dir with a policy that always reads critical.
func criticalGuard(dir string) *diskguard.Guard {
	g := diskguard.New("", diskguard.Watch{Path: dir, Role: "data"})
	g.Policy = diskguard.Policy{WarnPercent: 0, CriticalPercent: 0, EmergencyPercent: 100}
	return g
}

func TestDiskGateRefusesBulkWritesButNeverCleanup(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{GameRoot: dir, Disk: criticalGuard(dir)}
	ctx := context.Background()

	refused := []*agentv1.ExecuteRequest{
		{Method: &agentv1.ExecuteRequest_ArchiveExtract{ArchiveExtract: &agentv1.ArchiveExtract{DestPath: dir + "/x"}}},
		{Method: &agentv1.ExecuteRequest_DiskConvert{DiskConvert: &agentv1.DiskConvert{DestPath: dir + "/d.qcow2"}}},
		{Method: &agentv1.ExecuteRequest_BackupCopy{BackupCopy: &agentv1.BackupCopy{Action: "v2-capture", DestPath: dir}}},
		{Method: &agentv1.ExecuteRequest_HostUpdate{HostUpdate: &agentv1.HostUpdate{Action: "checkpoint"}}},
		{Method: &agentv1.ExecuteRequest_GameServer{GameServer: &agentv1.GameServer{Action: gsInstall, ServerId: "s1"}}},
	}
	for _, req := range refused {
		_, err := h.Execute(ctx, connect.NewRequest(req))
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeResourceExhausted {
			t.Fatalf("%T must be refused on a full disk, got %v", req.Method, err)
		}
	}

	allowed := []*agentv1.ExecuteRequest{
		{Method: &agentv1.ExecuteRequest_BackupCopy{BackupCopy: &agentv1.BackupCopy{Action: "v2-expire"}}},
		{Method: &agentv1.ExecuteRequest_BackupObject{BackupObject: &agentv1.BackupObject{Action: "put"}}},
		{Method: &agentv1.ExecuteRequest_VmSnapshot{VmSnapshot: &agentv1.VMSnapshot{Action: "delete"}}},
		{Method: &agentv1.ExecuteRequest_ComputeMigrate{ComputeMigrate: &agentv1.ComputeMigrate{Action: "cancel"}}},
		{Method: &agentv1.ExecuteRequest_HostUpdate{HostUpdate: &agentv1.HostUpdate{Action: "status"}}},
		{Method: &agentv1.ExecuteRequest_GameServer{GameServer: &agentv1.GameServer{Action: gsRemove, ServerId: "s1"}}},
	}
	for _, req := range allowed {
		if _, _, gated := diskGateFor(req, dir); gated {
			t.Fatalf("%T must never be refused: it reads or frees space", req.Method)
		}
	}

	res, err := h.Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostDisk{HostDisk: &agentv1.HostDisk{Action: "status"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var st HostDiskResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &st); err != nil || st.Status.Level != diskguard.LevelCritical {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := h.Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostDisk{HostDisk: &agentv1.HostDisk{Action: "cleanup", Category: "storage"}},
	})); err == nil {
		t.Fatal("user data categories must not be cleanable")
	}
}

func TestDiskGateOffWithoutGuard(t *testing.T) {
	h := &Handler{}
	if err := h.diskGate(&agentv1.ExecuteRequest{Method: &agentv1.ExecuteRequest_ArchiveExtract{ArchiveExtract: &agentv1.ArchiveExtract{DestPath: "/x"}}}); err != nil {
		t.Fatal(err)
	}
}
