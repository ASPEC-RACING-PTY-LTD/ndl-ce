package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func (h *Handler) execComputeMigrate(ctx context.Context, m *agentv1.ComputeMigrate) (*connect.Response[agentv1.ExecuteResponse], error) {
	id := strings.TrimSpace(m.GetWorkloadId())
	action := strings.TrimSpace(m.GetAction())
	switch action {
	case "pull_volume", "copy_volume":
		// Object/directory pulls are locator copies. They do not address a guest.
	default:
		if err := requireWorkloadUUID(id); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	switch action {
	case "prepare_incoming":
		spec := qemu.Spec{
			WorkloadID: id, VolumeID: m.GetVolumeId(), DiskPath: m.GetDiskPath(),
			DiskFormat: m.GetDiskFormat(), CPUs: int(m.GetCpus()), MemoryBytes: m.GetMemoryBytes(),
			Machine: m.GetMachine(), Accel: m.GetAccel(), IncomingDefer: true,
		}
		res, err := h.qemu().PrepareIncoming(spec)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	case "copy_volume":
		res, err := h.qemu().CopyOffline(ctx, qemu.BackupCopy, m.GetSourcePath(), m.GetDestPath())
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	case "pull_volume":
		src := strings.TrimSpace(m.GetUri())
		if src == "" {
			src = m.GetSourcePath()
		}
		if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") && !strings.HasPrefix(src, "s3://") {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pull_volume requires http(s) or s3 source"))
		}
		dest := m.GetDestPath()
		localSrc := src
		cleanup := func() {}
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			tmp, derr := downloadDestPull(ctx, src)
			if derr != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, derr)
			}
			localSrc = tmp
			cleanup = func() { _ = os.Remove(tmp) }
		}
		defer cleanup()
		act := destPullAction(src, dest)
		res, err := h.qemu().CopyOffline(ctx, act, localSrc, dest)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	case "live_migrate":
		if err := h.qemu().LiveMigrate(ctx, id, m.GetUri()); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	case "live_cancel":
		if err := h.qemu().CancelLiveMigrate(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
	case "abort_incoming":
		if err := h.qemu().AbortIncoming(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
	case "stop_source":
		if err := h.qemu().Stop(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	case "start_offline":
		if err := h.qemu().Start(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown compute.migrate action"))
	}
}

func (c Client) ComputeMigrate(ctx context.Context, m *agentv1.ComputeMigrate) (json.RawMessage, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ComputeMigrate{ComputeMigrate: m},
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetResultJson(), nil
}

// PrepareDest prepares incoming on the local unix agent. Dest on a worker
// must not be started here; the control plane refuses that path first.
func (c Client) PrepareDest(ctx context.Context, req migrate.Request) error {
	m := &agentv1.ComputeMigrate{
		Action: "prepare_incoming", WorkloadId: req.WorkloadID,
		Cpus: int32(req.CPUs), MemoryBytes: req.MemoryBytes,
		Machine: req.Machine, Accel: req.Accel,
	}
	if len(req.Disks) > 0 {
		m.VolumeId = req.Disks[0].VolumeID
		m.DiskPath = req.Disks[0].DestPath
		if m.DiskPath == "" {
			m.DiskPath = req.Disks[0].SourcePath
		}
	}
	_, err := c.ComputeMigrate(ctx, m)
	return err
}

func (c Client) CopyVolume(ctx context.Context, vol migrate.VolumeCopy) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{
		Action: "copy_volume", VolumeId: vol.VolumeID,
		SourcePath: vol.SourcePath, DestPath: vol.DestPath,
	})
	return err
}

func (c Client) PullVolume(ctx context.Context, vol migrate.VolumeCopy) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{
		Action: "pull_volume", VolumeId: vol.VolumeID,
		SourcePath: vol.SourcePath, DestPath: vol.DestPath, Uri: vol.SourcePath,
	})
	return err
}

func (c Client) StopSource(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "stop_source", WorkloadId: id})
	return err
}

func (c Client) StartDest(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "start_offline", WorkloadId: id})
	return err
}

func (c Client) LiveMigrate(ctx context.Context, id string) error {
	uri := (&qemu.Engine{}).IncomingURI(id)
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{
		Action: "live_migrate", WorkloadId: id, Uri: uri,
	})
	return err
}

func (c Client) AbortDest(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "abort_incoming", WorkloadId: id})
	return err
}

func destPullAction(src, dest string) string {
	if looksLikeArchive(src) || looksLikeArchive(dest) || isDirectoryDest(dest) {
		return qemu.BackupExtractRoot
	}
	return qemu.BackupCopy
}

func looksLikeArchive(p string) bool {
	p = strings.ToLower(p)
	return strings.Contains(p, ".tar") || strings.HasSuffix(p, ".zst") || strings.HasSuffix(p, ".gz")
}

func isDirectoryDest(p string) bool {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return false
	}
	if strings.Contains(p, ".qcow2") || strings.HasSuffix(p, ".img") || strings.HasSuffix(p, ".raw") {
		return false
	}
	return true
}

func downloadDestPull(ctx context.Context, src string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("dest pull http %d", res.StatusCode)
	}
	dir := filepath.Join(os.TempDir(), "ndl-dest-pull")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "pack-*")
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(f, res.Body)
	name := f.Name()
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		_ = os.Remove(name)
		return "", copyErr
	}
	named, nerr := namePulledArchive(name)
	if nerr != nil {
		_ = os.Remove(name)
		return "", nerr
	}
	return named, nil
}

func namePulledArchive(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	_ = f.Close()
	ext := ".tar"
	if n >= 2 && head[0] == 0x1f && head[1] == 0x8b {
		ext = ".tar.gz"
	} else if n >= 4 && head[0] == 0x28 && head[1] == 0xb5 && head[2] == 0x2f && head[3] == 0xfd {
		ext = ".tar.zst"
	}
	named := path + ext
	if err := os.Rename(path, named); err != nil {
		return "", err
	}
	return named, nil
}

func (c Client) SourceRunning(ctx context.Context, id string) bool {
	obs, err := c.StatusQemuProto(ctx, id)
	if err != nil {
		return true
	}
	return obs.UnitActive || obs.Status == qemu.StatusRunning
}
