package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func (h *Handler) execBackupVerify(ctx context.Context, m *agentv1.BackupVerify) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.qemu().CheckOffline(ctx, m.GetSourcePath(), m.GetExpectedSha256())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "verify", ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execBackupExtract(ctx context.Context, m *agentv1.BackupExtract) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.qemu().ExtractOffline(ctx, m.GetSourcePath(), m.GetGuestPath(), m.GetDestPath())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "extract", ResultJson: mustJSON(res)}), nil
}

func (c Client) VerifyBackup(ctx context.Context, src, expectedSHA string) (qemu.VerifyResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_BackupVerify{BackupVerify: &agentv1.BackupVerify{
			SourcePath: src, ExpectedSha256: expectedSHA,
		}},
	}))
	if err != nil {
		return qemu.VerifyResult{}, err
	}
	var out qemu.VerifyResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.VerifyResult{}, err
	}
	return out, nil
}

func (c Client) ExtractBackup(ctx context.Context, src, guestPath, dest string) (qemu.ExtractResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_BackupExtract{BackupExtract: &agentv1.BackupExtract{
			SourcePath: src, GuestPath: guestPath, DestPath: dest,
		}},
	}))
	if err != nil {
		return qemu.ExtractResult{}, err
	}
	var out qemu.ExtractResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.ExtractResult{}, err
	}
	return out, nil
}
