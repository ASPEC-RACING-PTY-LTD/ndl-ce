package agentrpc

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/hostos"
)

func (h *Handler) execHostUpdate(ctx context.Context, m *agentv1.HostUpdate) (*connect.Response[agentv1.ExecuteResponse], error) {
	p, err := h.lookupPlatform()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	req := hostos.UpdateRequest{
		Action:       strings.TrimSpace(m.GetAction()),
		Channel:      strings.TrimSpace(m.GetChannel()),
		PackageName:  strings.TrimSpace(m.GetPackageName()),
		Version:      strings.TrimSpace(m.GetVersion()),
		DryRun:       m.GetDryRun(),
		CheckpointID: strings.TrimSpace(m.GetCheckpointId()),
	}
	var run hostos.ExecFunc
	if !h.SkipHostCmds {
		run = runTypedArgv
	}
	res, err := hostos.RunUpdate(ctx, p, req, run)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: req.Action, ResultJson: mustJSON(res)}), nil
}

func (h *Handler) lookupPlatform() (hostos.Platform, error) {
	p, err := h.platform()
	if err == nil {
		return p, nil
	}
	var he hostos.Error
	if errors.As(err, &he) {
		return he.Platform, nil
	}
	return p, err
}

func runTypedArgv(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 || !strings.HasPrefix(argv[0], "/") {
		return "", errors.New("argv is not a typed absolute path")
	}
	if strings.Contains(argv[0], "bash") {
		return "", errors.New("shell is not a typed update action")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
