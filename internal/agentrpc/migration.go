package agentrpc

import (
	"context"
	"io"
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
	if err := migration.ValidateHostPath(src); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := migration.ValidateHostPath(dest); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	defer f.Close()
	r, err := migration.MaybeDecompress(f, src)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if c, ok := r.(io.Closer); ok && r != f {
		defer c.Close()
	}
	if err := migration.ExtractTar(r, dest, 0); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "extract"}), nil
}
