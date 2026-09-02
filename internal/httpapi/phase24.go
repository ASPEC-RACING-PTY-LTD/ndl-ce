package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// VerifyRPC is the privileged agent surface for open-verify and file extract.
type VerifyRPC interface {
	VerifyBackup(ctx context.Context, src, expectedSHA string) (qemu.VerifyResult, error)
	ExtractBackup(ctx context.Context, src, guestPath, dest string) (qemu.ExtractResult, error)
}

type verifyUnavailable struct{}

func (verifyUnavailable) VerifyBackup(context.Context, string, string) (qemu.VerifyResult, error) {
	return qemu.VerifyResult{}, errUnavailable("backup verify agent is unavailable")
}

func (verifyUnavailable) ExtractBackup(context.Context, string, string, string) (qemu.ExtractResult, error) {
	return qemu.ExtractResult{}, errUnavailable("backup extract agent is unavailable")
}

func AdaptVerify(client any) VerifyRPC {
	if v, ok := client.(VerifyRPC); ok {
		return v
	}
	return verifyUnavailable{}
}

func (s *Server) verifyRPC() VerifyRPC {
	if s.Verify != nil {
		return s.Verify
	}
	return AdaptVerify(s.Agent)
}

func (s *Server) verifyBackupArtifact(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRestore)
	if err != nil {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	_ = readJSON(r, &req)
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "open"
	}
	if mode != "open" && mode != "throwaway" {
		writeErr(w, http.StatusBadRequest, "mode must be open or throwaway")
		return
	}
	art, err := s.Store.GetBackupArtifact(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || art == nil {
		writeErr(w, http.StatusNotFound, "backup artifact not found")
		return
	}
	src, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, art.WorkloadID)
	srcStatus := ""
	srcVolRef := ""
	if src != nil {
		srcStatus = src.Status
		if vol, _, _, locErr := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *src); locErr == nil && vol != nil {
			srcVolRef = vol.BackendRef
		}
	}
	if art.Format == "zfs" {
		art.VerifyStatus = appdb.BackupUnverified
		art.VerifyError = "ZFS send artifacts are checksum-catalogued; qemu-img check is not used on a ZFS stream"
		now := s.now()
		art.LastTestedAt = &now
		_ = s.Store.UpdateBackupArtifactVerify(r.Context(), *art)
		writeJSON(w, http.StatusUnprocessableEntity, backupArtifactJSON(*art))
		return
	}
	srcPath, cleanup, err := s.materializeArtifact(r.Context(), p.User.ClusterID, *art)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	defer cleanup()
	res, err := s.verifyRPC().VerifyBackup(r.Context(), srcPath, art.ChecksumSHA256)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	now := s.now()
	art.LastTestedAt = &now
	art.VerifyError = res.Reason
	switch res.Status {
	case qemu.VerifyVerified:
		art.VerifyStatus = appdb.BackupVerified
	case qemu.VerifyFailed:
		art.VerifyStatus = qemu.VerifyFailed
	default:
		art.VerifyStatus = appdb.BackupUnverified
		if art.VerifyError == "" {
			art.VerifyError = firstNonEmpty(res.Reason, "verify did not complete")
		}
	}
	if mode == "throwaway" && res.Reason != "checksum mismatch" && res.Status != qemu.VerifyFailed {
		if src == nil {
			writeErr(w, http.StatusUnprocessableEntity, "throwaway restore requires the original workload catalog row")
			return
		}
		tid, err := s.restoreNewVM(r.Context(), p.User.ClusterID, *src, *art, true, nil)
		if err != nil {
			art.VerifyStatus = appdb.BackupUnverified
			art.VerifyError = err.Error()
			_ = s.Store.UpdateBackupArtifactVerify(r.Context(), *art)
			writeErr(w, statusFor(err), err.Error())
			return
		}
		art.ThrowawayWorkloadID = tid
		art.VerifyStatus = appdb.BackupVerified
		art.VerifyError = ""
		if src2, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, src.ID); src2 != nil && src2.Status != srcStatus {
			art.VerifyStatus = appdb.BackupUnverified
			art.VerifyError = "restore test touched the source workload"
		}
		if vol, _, _, locErr := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *src); locErr == nil && vol != nil && vol.BackendRef != srcVolRef {
			art.VerifyStatus = appdb.BackupUnverified
			art.VerifyError = "restore test touched the source workload"
		}
	}
	if err := s.Store.UpdateBackupArtifactVerify(r.Context(), *art); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record backup verify")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.verify."+mode, art.VerifyStatus, art.ID)
	writeJSON(w, http.StatusAccepted, backupArtifactJSON(*art))
}

func (s *Server) restoreBackupFile(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRestore)
	if err != nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	art, err := s.Store.GetBackupArtifact(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || art == nil {
		writeErr(w, http.StatusNotFound, "backup artifact not found")
		return
	}
	if art.Format == "zfs" {
		writeErr(w, http.StatusUnprocessableEntity, "ZFS send artifacts do not support file restore")
		return
	}
	srcPath, cleanup, err := s.materializeArtifact(r.Context(), p.User.ClusterID, *art)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	defer cleanup()
	dir, err := os.MkdirTemp("", "ndl-restore-file-")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dest := filepath.Join(dir, "file")
	res, err := s.verifyRPC().ExtractBackup(r.Context(), srcPath, req.Path, dest)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if res.Status != qemu.VerifyVerified {
		writeErr(w, http.StatusUnprocessableEntity, firstNonEmpty(res.Reason, "file extract is unavailable"))
		return
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "extracted file is unreadable")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.restore-file", "ok", art.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": art.ID, "path": res.GuestPath, "size_bytes": res.Size,
		"sha256": res.SHA256, "content_base64": base64.StdEncoding.EncodeToString(raw),
	})
}

func (s *Server) isolatedNetworkID(ctx context.Context, clusterID string) (string, error) {
	nets, err := s.Store.ListNetworks(ctx, clusterID)
	if err != nil {
		return "", err
	}
	for _, n := range nets {
		if n.Kind == "isolated" || n.Kind == "isolated-nat" {
			return n.ID, nil
		}
	}
	return "", errUnprocessable("throwaway restore needs an isolated network")
}
