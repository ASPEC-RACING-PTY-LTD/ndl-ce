package agentrpc

import (
	"context"
	"os"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func (h *Handler) execDiskConvert(ctx context.Context, m *agentv1.DiskConvert) (*connect.Response[agentv1.ExecuteResponse], error) {
	err := h.qemu().ConvertImport(ctx, qemu.ConvertRequest{
		SourcePath: m.GetSourcePath(), DestPath: m.GetDestPath(),
		SourceFormat: m.GetSourceFormat(), DestFormat: m.GetDestFormat(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "convert", ResultJson: mustJSON(map[string]string{"dest": m.GetDestPath()})}), nil
}

func (h *Handler) execArchiveExtract(_ context.Context, m *agentv1.ArchiveExtract) (*connect.Response[agentv1.ExecuteResponse], error) {
	src := m.GetSourcePath()
	dest := m.GetDestPath()
	if destInfo, err := os.Lstat(dest); err != nil && migration.LooksLikeArchiveDest(dest) {
		if err := migration.CopyHostArchive(src, dest); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "copy-archive"}), nil
	} else if err == nil && !destInfo.IsDir() && migration.LooksLikeArchiveDest(dest) {
		if err := migration.CopyHostArchive(src, dest); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "copy-archive"}), nil
	}
	if err := migration.ValidateHostPath(dest); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	info, err := os.Lstat(src)
	if err == nil && (info.IsDir() || info.Mode()&os.ModeDevice != 0) {
		if err := migration.CopyLocalRootfs(src, dest); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "copy"}), nil
	}
	if err := migration.ValidateHostPath(src); err != nil && !migration.AllowedBackupSource(src) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := migration.ExtractVzdumpOrRootfs(src, dest); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "extract"}), nil
}
