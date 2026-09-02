package httpapi

import (
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/k8s"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) getKubernetes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.kubernetesJSON(r, p.User.ClusterID))
}

func (s *Server) startKubernetes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != k8s.StartConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "starting kubelet requires X-Nodal-Confirm: start-kubelet")
		return
	}
	row := s.featureRow(r, p.User.ClusterID, k8sModule())
	if !row.Enabled {
		writeErr(w, http.StatusUnprocessableEntity, "enable the Kubernetes feature before starting kubelet")
		return
	}
	res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
		Action: hostos.UpdateK8sRuntimeStart, Channel: hostos.ChannelStable,
	})
	if uerr != nil {
		writeErr(w, http.StatusBadGateway, uerr.Error())
		return
	}
	// Enable is not start. A supported updater result is not a kube process.
	// This host leaves kubelet unstarted unless Observe already sees one.
	obs := k8s.Observe(s.K8sProcs)
	if res.Supported && res.Status != "failed" && obs.KubeProcess {
		row.RuntimeStatus = appdb.FeatureRunning
		row.Reason = res.Reason
	} else {
		row.RuntimeStatus = appdb.FeatureNotStarted
		if res.Reason != "" {
			row.Reason = res.Reason
		} else {
			row.Reason = "kubelet was not started. This host cannot run the Debian kubelet unit."
		}
	}
	row.UpdatedAt = s.now()
	_ = s.Store.UpsertFeature(r.Context(), row)
	s.audit(r, p.User.ClusterID, p.User.ID, "k8s.start", "ok", features.IDK8s)
	writeJSON(w, http.StatusOK, s.kubernetesJSON(r, p.User.ClusterID))
}

func (s *Server) stopKubernetes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	row := s.featureRow(r, p.User.ClusterID, k8sModule())
	res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
		Action: hostos.UpdateK8sRuntimeStop, Channel: hostos.ChannelStable,
	})
	if uerr != nil {
		writeErr(w, http.StatusBadGateway, uerr.Error())
		return
	}
	row.RuntimeStatus = appdb.FeatureNotStarted
	row.Reason = "kubelet stop requested. Virtual machines and system containers were not stopped."
	if res.Reason != "" {
		row.Reason = res.Reason + " Virtual machines and system containers were not stopped."
	}
	row.UpdatedAt = s.now()
	_ = s.Store.UpsertFeature(r.Context(), row)
	s.audit(r, p.User.ClusterID, p.User.ID, "k8s.stop", "ok", features.IDK8s)
	writeJSON(w, http.StatusOK, s.kubernetesJSON(r, p.User.ClusterID))
}

func (s *Server) kubernetesJSON(r *http.Request, clusterID string) map[string]any {
	mod := k8sModule()
	row := s.featureRow(r, clusterID, mod)
	obs := k8s.Observe(s.K8sProcs)
	started := row.Enabled && row.RuntimeStatus == appdb.FeatureRunning && obs.KubeProcess
	reason := obs.Reason
	if !row.Enabled {
		reason = k8s.DisabledReason
	} else if row.Reason != "" {
		reason = row.Reason
	}
	return map[string]any{
		"enabled":         row.Enabled,
		"kubelet_started": started,
		"kube_process":    obs.KubeProcess,
		"state":           obs.State,
		"package_status":  row.PackageStatus,
		"runtime_status":  row.RuntimeStatus,
		"reason":          reason,
		"vm_requires_k8s": false,
		"ct_requires_k8s": false,
	}
}

func k8sModule() features.Module {
	mod, _ := features.Lookup(features.IDK8s)
	return mod
}
