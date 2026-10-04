package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/diskguard"
	"github.com/no-dal/ndl-ce/internal/objstore"
)

// diskGate refuses an Execute method that writes bulk data to a host
// filesystem that is critically full. Methods that read, delete, expire,
// upload to remote storage or report status are never refused: they are
// how an operator gets space back.
func (h *Handler) diskGate(m *agentv1.ExecuteRequest) error {
	if h.Disk == nil {
		return nil
	}
	op, dest, gated := diskGateFor(m, h.GameRoot)
	if !gated {
		return nil
	}
	if err := h.Disk.Allow(op, dest, 0); err != nil {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	return nil
}

func backingAction(raw []byte) string {
	var extra struct {
		Action string `json:"ndl_volume_action"`
	}
	_ = json.Unmarshal(raw, &extra)
	return extra.Action
}

// diskGateFor names the operation, its destination (empty when it writes
// to the data directory in general) and whether it is a bulk write.
func diskGateFor(m *agentv1.ExecuteRequest, gameRoot string) (string, string, bool) {
	switch {
	case m.GetCreateDirectoryVolume() != nil:
		v := m.GetCreateDirectoryVolume()
		if backingAction(v.GetBackingJson()) == "destroy" {
			return "", "", false
		}
		return "Creating or growing a volume", v.GetRootPath(), true
	case m.GetCtCreate() != nil:
		return "Creating a system container", "", true
	case m.GetVmSnapshot() != nil:
		v := m.GetVmSnapshot()
		if strings.TrimSpace(v.GetAction()) != "create" {
			return "", "", false
		}
		return "Creating a VM snapshot", v.GetOverlayPath(), true
	case m.GetBackupCopy() != nil:
		v := m.GetBackupCopy()
		switch v.GetAction() {
		case backuphost.ActionStatus, backuphost.ActionPreview, backuphost.ActionExpire:
			return "", "", false
		}
		return "Backup", v.GetDestPath(), true
	case m.GetBackupObject() != nil:
		v := m.GetBackupObject()
		switch v.GetAction() {
		case objstore.ActionGet, objstore.ActionGetPack:
			return "Downloading a backup", v.GetDestPath(), true
		}
		return "", "", false
	case m.GetBackupExtract() != nil:
		return "Restoring files", m.GetBackupExtract().GetDestPath(), true
	case m.GetDiskConvert() != nil:
		return "Converting a disk", m.GetDiskConvert().GetDestPath(), true
	case m.GetArchiveExtract() != nil:
		return "Extracting an archive", m.GetArchiveExtract().GetDestPath(), true
	case m.GetComputeMigrate() != nil:
		v := m.GetComputeMigrate()
		switch strings.TrimSpace(v.GetAction()) {
		case "prepare_incoming", "copy_volume", "pull_volume":
			return "Migration", v.GetDestPath(), true
		}
		return "", "", false
	case m.GetHostUpdate() != nil:
		switch strings.TrimSpace(m.GetHostUpdate().GetAction()) {
		case "checkpoint":
			return "Creating an update checkpoint", "", true
		case "apply", "rollback", "feature-install":
			// A package install that runs out of space can leave dpkg broken.
			return "Installing packages", "", true
		}
		return "", "", false
	case m.GetGameServer() != nil:
		switch strings.TrimSpace(m.GetGameServer().GetAction()) {
		case gsInstall, gsWriteFile, gsExtract, gsBackup, gsRestoreBackup:
			return "Game server " + m.GetGameServer().GetAction(), gameRoot, true
		}
		return "", "", false
	}
	return "", "", false
}

// diskStreamGate guards the streaming uploads (library images, files).
func (h *Handler) diskStreamGate(op, dest string) error {
	if h.Disk == nil {
		return nil
	}
	if err := h.Disk.Allow(op, dest, 0); err != nil {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	return nil
}

// HostDiskResult is the HostDisk result.
type HostDiskResult struct {
	Status diskguard.Status       `json:"status"`
	Usage  *diskguard.Usage       `json:"usage,omitempty"`
	Clean  *diskguard.CleanResult `json:"clean,omitempty"`
}

func (h *Handler) dataDir() string {
	if d := strings.TrimSpace(h.GameRoot); d != "" {
		return d
	}
	return "/var/lib/ndl"
}

func (h *Handler) execHostDisk(ctx context.Context, m *agentv1.HostDisk) (*connect.Response[agentv1.ExecuteResponse], error) {
	if h.Disk == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("disk protection is not running on this node"))
	}
	var out HostDiskResult
	switch strings.TrimSpace(m.GetAction()) {
	case "status", "":
		out.Status = h.Disk.Refresh()
	case "usage":
		u := h.Disk.Measure(ctx, h.dataDir(), 20*time.Second)
		out.Usage = &u
		out.Status = h.Disk.Status()
	case "cleanup":
		res, err := h.Disk.Clean(ctx, h.dataDir(), strings.TrimSpace(m.GetCategory()), m.GetProtect())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		out.Clean = &res
		out.Status = h.Disk.Status()
	case "release-reserve":
		st, err := h.Disk.ReleaseReserve()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		out.Status = st
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("disk action %q is not supported", m.GetAction()))
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(out)}), nil
}
