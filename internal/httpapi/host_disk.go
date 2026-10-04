package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Host disk protection. The agent guards the host filesystems (the root
// disk, the No-dal data directory and PostgreSQL); these endpoints show
// that state and expose the two safe actions: cleaning up disposable
// files and releasing the emergency reserve.

const (
	cleanupDiskConfirm    = "clean-up"
	releaseReserveConfirm = "release-reserve"
)

// HostDiskRPC is the agent's disk protection surface.
type HostDiskRPC interface {
	HostDisk(ctx context.Context, action, category string, protect []string) (agentrpc.HostDiskResult, error)
}

func (s *Server) hostDisk(w http.ResponseWriter) (HostDiskRPC, bool) {
	if s.Disk == nil {
		writeErr(w, http.StatusBadGateway, "disk protection is unavailable")
		return nil, false
	}
	return s.Disk, true
}

func (s *Server) getHostDisk(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.StorageRead); err != nil {
		return
	}
	d, ok := s.hostDisk(w)
	if !ok {
		return
	}
	res, err := d.HostDisk(r.Context(), "status", "", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getHostDiskUsage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.StorageRead); err != nil {
		return
	}
	d, ok := s.hostDisk(w)
	if !ok {
		return
	}
	res, err := d.HostDisk(r.Context(), "usage", "", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// activeJobIDs names staging entries a running operation may still use.
func (s *Server) activeJobIDs(ctx context.Context, clusterID string) []string {
	var out []string
	if runs, err := s.Store.ListBackupRuns(ctx, clusterID); err == nil {
		for _, run := range runs {
			if run.FinishedAt == nil {
				out = append(out, run.ID)
			}
		}
	}
	if jobs, err := s.Store.ListMigrationJobs(ctx, clusterID, 500); err == nil {
		for _, j := range jobs {
			switch strings.ToLower(j.State) {
			case "succeeded", "completed", "failed", "canceled", "cancelled":
			default:
				out = append(out, j.ID)
			}
		}
	}
	return out
}

func (s *Server) cleanupHostDisk(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != cleanupDiskConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "cleanup requires X-Nodal-Confirm: clean-up")
		return
	}
	d, ok := s.hostDisk(w)
	if !ok {
		return
	}
	var req struct {
		Category string `json:"category"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Category) == "" {
		writeErr(w, http.StatusBadRequest, "category is required")
		return
	}
	res, err := d.HostDisk(r.Context(), "cleanup", req.Category, s.activeJobIDs(r.Context(), p.User.ClusterID))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		s.audit(r, p.User.ClusterID, p.User.ID, "host.disk.cleanup", "denied", req.Category)
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "host.disk.cleanup", "ok", req.Category)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) releaseHostDiskReserve(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != releaseReserveConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "releasing the reserve requires X-Nodal-Confirm: release-reserve")
		return
	}
	d, ok := s.hostDisk(w)
	if !ok {
		return
	}
	res, err := d.HostDisk(r.Context(), "release-reserve", "", nil)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "host.disk.release_reserve", "ok", "")
	writeJSON(w, http.StatusOK, res)
}
