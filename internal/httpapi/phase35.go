package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	enableK8sConfirm      = features.EnableK8s
	disableFeatureConfirm = features.DisableConfirm
)

func (s *Server) listFeatures(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.featuresJSON(r, p.User.ClusterID))
}

func (s *Server) enableFeature(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	mod, ok := features.Lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "feature is unknown")
		return
	}
	if mod.Core {
		writeJSON(w, http.StatusOK, s.featureJSON(r, p.User.ClusterID, mod))
		return
	}
	tiny := s.featureTinyNode(r, p.User.ClusterID)
	if mod.RequiresK8sAck && tiny && strings.TrimSpace(r.Header.Get(confirmHeader)) != enableK8sConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "enabling Kubernetes on a node at or below 8 GiB RAM requires X-Nodal-Confirm: enable-k8s; kubelet is not started")
		return
	}
	row := s.featureRow(r, p.User.ClusterID, mod)
	row.Enabled = true
	row.RuntimeStatus = appdb.FeatureNotStarted
	row.Reason = mod.DefaultReason
	if mod.Package != "" {
		res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
			Action: hostos.UpdateFeatureInstall, PackageName: mod.Package, Channel: hostos.ChannelStable,
		})
		if uerr != nil {
			writeErr(w, http.StatusBadGateway, uerr.Error())
			return
		}
		row.PackageStatus = featurePackageStatus(res)
		if res.Reason != "" && row.PackageStatus == appdb.FeatureUnavailable {
			row.Reason = res.Reason + " " + mod.DefaultReason
		}
		if looksLikeRuntimeStart(res.Reason) {
			writeErr(w, http.StatusInternalServerError, "feature install must not start Kubernetes")
			return
		}
	}
	row.UpdatedAt = s.now()
	if err := s.Store.UpsertFeature(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "feature.enable", "ok", mod.ID)
	writeJSON(w, http.StatusOK, s.featureJSON(r, p.User.ClusterID, mod))
}

func (s *Server) disableFeature(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	mod, ok := features.Lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "feature is unknown")
		return
	}
	if mod.Core {
		writeErr(w, http.StatusUnprocessableEntity, "core features stay installed with the nodal metapackage")
		return
	}
	count := s.featureWorkloadCount(r, p.User.ClusterID, mod.ID)
	if count > 0 && strings.TrimSpace(r.Header.Get(confirmHeader)) != disableFeatureConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "disable does not delete workloads; send X-Nodal-Confirm: disable-feature to turn the module off and leave workloads running")
		return
	}
	row := s.featureRow(r, p.User.ClusterID, mod)
	row.Enabled = false
	row.RuntimeStatus = appdb.FeatureNotStarted
	row.Reason = "disabled; workloads were not deleted"
	if mod.Package != "" {
		res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
			Action: hostos.UpdateFeatureRemove, PackageName: mod.Package, Channel: hostos.ChannelStable,
		})
		if uerr != nil {
			writeErr(w, http.StatusBadGateway, uerr.Error())
			return
		}
		if res.Supported && res.Status != "failed" {
			row.PackageStatus = appdb.FeatureRemoved
		} else {
			row.PackageStatus = featurePackageStatus(res)
			if res.Reason != "" {
				row.Reason = res.Reason + "; workloads were not deleted"
			}
		}
	}
	row.UpdatedAt = s.now()
	if err := s.Store.UpsertFeature(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "feature.disable", "ok", mod.ID)
	writeJSON(w, http.StatusOK, s.featureJSON(r, p.User.ClusterID, mod))
}

func (s *Server) featuresJSON(r *http.Request, clusterID string) map[string]any {
	items := make([]map[string]any, 0, len(features.Catalog()))
	for _, mod := range features.Catalog() {
		items = append(items, s.featureJSON(r, clusterID, mod))
	}
	return map[string]any{
		"items":        items,
		"base_install": "light",
		"gpu_optional": true,
		"reason":       features.LightBaseReason,
	}
}

func (s *Server) featureJSON(r *http.Request, clusterID string, mod features.Module) map[string]any {
	row := s.featureRow(r, clusterID, mod)
	out := map[string]any{
		"id":              mod.ID,
		"title":           mod.Title,
		"enabled":         row.Enabled,
		"core":            mod.Core,
		"package_status":  row.PackageStatus,
		"runtime_status":  row.RuntimeStatus,
		"starts_runtime":  mod.StartsRuntime,
		"kubelet_started": false,
		"workload_count":  s.featureWorkloadCount(r, clusterID, mod.ID),
		"reason":          row.Reason,
	}
	if mod.Package != "" {
		out["package"] = mod.Package
	}
	if mod.RequiresK8sAck {
		out["tiny_node"] = s.featureTinyNode(r, clusterID)
		out["confirm"] = enableK8sConfirm
	}
	return out
}

func (s *Server) featureRow(r *http.Request, clusterID string, mod features.Module) appdb.Feature {
	stored, _ := s.Store.GetFeature(r.Context(), clusterID, mod.ID)
	if stored != nil {
		return *stored
	}
	row := appdb.Feature{
		ClusterID: clusterID, ID: mod.ID, Enabled: mod.Core,
		PackageStatus: appdb.FeatureNotConfigured, RuntimeStatus: appdb.FeatureNotStarted,
		Reason: mod.DefaultReason, UpdatedAt: time.Time{},
	}
	if mod.Core {
		row.PackageStatus = appdb.FeatureInstalled
		row.RuntimeStatus = appdb.FeatureInstalled
	}
	return row
}

func (s *Server) featureTinyNode(r *http.Request, clusterID string) bool {
	node, _ := s.Store.GetNode(r.Context(), clusterID)
	if node == nil {
		return true
	}
	inv, _ := s.Store.GetInventory(r.Context(), node.ID)
	parsed, ok := decodeInv(inv)
	if !ok {
		return true
	}
	return features.TinyNode(parsed.Memory.TotalBytes)
}

func (s *Server) featureWorkloadCount(r *http.Request, clusterID, id string) int {
	switch id {
	case features.IDOCI:
		wls, _ := s.Store.ListWorkloads(r.Context(), clusterID)
		n := 0
		for _, w := range wls {
			if w.Kind == "oci" {
				n++
			}
		}
		return n
	case features.IDGPU:
		as, _ := s.Store.ListGPUAssignments(r.Context(), clusterID)
		return len(as)
	default:
		return 0
	}
}

func featurePackageStatus(res hostos.UpdateResult) string {
	if !res.Supported {
		return appdb.FeatureUnavailable
	}
	if res.Status == "failed" {
		return appdb.FeatureUnavailable
	}
	return appdb.FeatureInstalled
}

func looksLikeRuntimeStart(reason string) bool {
	l := strings.ToLower(reason)
	return strings.Contains(l, "kubelet") || strings.Contains(l, "kubeadm") || strings.Contains(l, "k3s started")
}
