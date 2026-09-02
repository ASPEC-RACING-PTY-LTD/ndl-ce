package httpapi

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	fenceConfirm         = "fence"
	promoteConfirm       = "promote"
	clusterUpdateConfirm = "cluster-update"
	haReplicaReason      = "replica DSN is stored; streaming replication is operator-managed Postgres and is not proven in this process"
	haFencingReason      = "STONITH is not implemented. Fence records that the operator isolated the old writer, then a standby may take the lease."
	workerUpdateReason   = "worker update agent is not connected; Phase 12 apply stays on this control node"
	rollingDrainReason   = "maintenance recorded; guests keep running; remote dest agent is not connected"
)

func (s *Server) haStateJSON(h appdb.HAState, lease *appdb.ClusterLease, writer bool) map[string]any {
	mode := h.Mode
	if mode == "" {
		mode = appdb.HAModeSingleWriter
	}
	replica := h.ReplicaStatus
	if replica == "" {
		replica = appdb.HAReplicaNotConfigured
	}
	fencing := h.FencingMode
	if fencing == "" {
		fencing = appdb.HAFencingOperator
	}
	out := map[string]any{
		"mode":           mode,
		"writer":         writer,
		"replica_status": replica,
		"fencing_mode":   fencing,
		"fencing_reason": haFencingReason,
		"multi_master":   false,
	}
	if h.ReplicaEndpoint != "" {
		out["replica_endpoint"] = h.ReplicaEndpoint
	}
	if h.Reason != "" {
		out["reason"] = h.Reason
	}
	if h.FencedHolder != "" {
		out["fenced_holder"] = h.FencedHolder
	}
	if h.FencedAt != nil {
		out["fenced_at"] = h.FencedAt.UTC().Format(time.RFC3339)
	}
	if h.PromotedHolder != "" {
		out["promoted_holder"] = h.PromotedHolder
	}
	if h.PromotedAt != nil {
		out["promoted_at"] = h.PromotedAt.UTC().Format(time.RFC3339)
	}
	if lease != nil {
		out["lease_holder"] = lease.HolderID
		out["lease_expires_at"] = lease.ExpiresAt.UTC().Format(time.RFC3339)
		out["lease_fenced"] = lease.Fenced
	}
	return out
}

func (s *Server) getClusterHA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterRead)
	if err != nil {
		return
	}
	h, _ := s.Store.GetHAState(r.Context(), p.User.ClusterID)
	if h == nil {
		h = &appdb.HAState{ClusterID: p.User.ClusterID, Mode: appdb.HAModeSingleWriter, ReplicaStatus: appdb.HAReplicaNotConfigured, FencingMode: appdb.HAFencingOperator}
	}
	lease, _ := s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	writer := true
	if lease != nil && s.LeaseHolder != "" && lease.HolderID != s.LeaseHolder && s.now().Before(lease.ExpiresAt) && !lease.Fenced {
		writer = false
	}
	writeJSON(w, http.StatusOK, s.haStateJSON(*h, lease, writer))
}

func (s *Server) configureHAReplica(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterPromote)
	if err != nil {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		DSN      string `json:"dsn"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	dsn := strings.TrimSpace(req.DSN)
	if endpoint == "" && dsn == "" {
		writeErr(w, http.StatusBadRequest, "endpoint or dsn is required")
		return
	}
	if strings.Contains(endpoint, "@") || strings.Contains(strings.ToLower(endpoint), "password") {
		writeErr(w, http.StatusBadRequest, "endpoint must not include credentials")
		return
	}
	if u, err := url.Parse(endpoint); err == nil && u.User != nil {
		writeErr(w, http.StatusBadRequest, "endpoint must not include credentials")
		return
	}
	h, _ := s.Store.GetHAState(r.Context(), p.User.ClusterID)
	if h == nil {
		h = &appdb.HAState{ClusterID: p.User.ClusterID}
	}
	h.Mode = appdb.HAModeSingleWriter
	h.FencingMode = appdb.HAFencingOperator
	if endpoint != "" {
		h.ReplicaEndpoint = endpoint
	}
	if dsn != "" {
		if err := s.Store.SetHAReplicaDSN(r.Context(), p.User.ClusterID, dsn); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.ReplicaStatus = appdb.HAReplicaUnavailable
		h.Reason = haReplicaReason
	} else {
		h.ReplicaStatus = appdb.HAReplicaNotConfigured
		h.Reason = "replica endpoint is recorded; DSN is not stored"
	}
	h.UpdatedAt = s.now()
	if err := s.Store.UpsertHAState(r.Context(), *h); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.ha.replica", "ok", h.ReplicaEndpoint)
	lease, _ := s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, s.haStateJSON(*h, lease, true))
}

func (s *Server) fenceClusterHA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterPromote)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != fenceConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "fence requires X-Nodal-Confirm: fence")
		return
	}
	lease, _ := s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	at := s.now().Add(-time.Second)
	if err := s.Store.FenceLease(r.Context(), p.User.ClusterID, at); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h, _ := s.Store.GetHAState(r.Context(), p.User.ClusterID)
	if h == nil {
		h = &appdb.HAState{ClusterID: p.User.ClusterID, Mode: appdb.HAModeSingleWriter, FencingMode: appdb.HAFencingOperator}
	}
	if lease != nil {
		h.FencedHolder = lease.HolderID
	}
	h.FencedAt = &at
	h.Reason = haFencingReason
	h.UpdatedAt = s.now()
	if err := s.Store.UpsertHAState(r.Context(), *h); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record HA fence")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.ha.fence", "ok", h.FencedHolder)
	lease, _ = s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, s.haStateJSON(*h, lease, false))
}

func (s *Server) promoteClusterHA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterPromote)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != promoteConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "promote requires X-Nodal-Confirm: promote")
		return
	}
	if s.LeaseHolder == "" {
		writeErr(w, http.StatusUnprocessableEntity, "this process has no lease holder identity")
		return
	}
	lease, _ := s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	if lease != nil && s.now().Before(lease.ExpiresAt) && !lease.Fenced && lease.HolderID != s.LeaseHolder {
		writeErr(w, http.StatusConflict, "old writer still holds the lease; fence it first")
		return
	}
	if err := s.Store.AcquireLease(r.Context(), p.User.ClusterID, s.LeaseHolder, s.now().Add(30*time.Second)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	now := s.now()
	h, _ := s.Store.GetHAState(r.Context(), p.User.ClusterID)
	if h == nil {
		h = &appdb.HAState{ClusterID: p.User.ClusterID, Mode: appdb.HAModeSingleWriter, FencingMode: appdb.HAFencingOperator}
	}
	h.PromotedHolder = s.LeaseHolder
	h.PromotedAt = &now
	h.Mode = appdb.HAModeSingleWriter
	h.Reason = "this process holds the writer lease"
	h.UpdatedAt = now
	if err := s.Store.UpsertHAState(r.Context(), *h); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record HA promote")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.ha.promote", "ok", s.LeaseHolder)
	lease, _ = s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, s.haStateJSON(*h, lease, true))
}

func rollingPlanJSON(p appdb.RollingPlan, steps []appdb.RollingStep) map[string]any {
	items := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		item := map[string]any{
			"id": st.ID, "node_id": st.NodeID, "ordinal": st.Ordinal,
			"action": st.Action, "status": st.Status,
		}
		if st.Reason != "" {
			item["reason"] = st.Reason
		}
		if st.UpdateOperationID != "" {
			item["update_operation_id"] = st.UpdateOperationID
		}
		items = append(items, item)
	}
	out := map[string]any{
		"id": p.ID, "status": p.Status, "steps": items,
		"created_at": p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if p.Reason != "" {
		out["reason"] = p.Reason
	}
	if p.FinishedAt != nil {
		out["finished_at"] = p.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

func (s *Server) getClusterUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	plan, _ := s.Store.LatestRollingPlan(r.Context(), p.User.ClusterID)
	preview := s.previewRollingSteps(r, p.User.ClusterID)
	body := map[string]any{
		"preview": preview,
		"note":    "Rolling drains one node, applies Phase 12 update on this control node, then the next. Guests are not stopped. This is not multi-master.",
	}
	if plan != nil {
		steps, _ := s.Store.ListRollingSteps(r.Context(), p.User.ClusterID, plan.ID)
		body["plan"] = rollingPlanJSON(*plan, steps)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) previewRollingSteps(r *http.Request, clusterID string) []map[string]any {
	nodes, _ := s.Store.ListClusterNodes(r.Context(), clusterID)
	ordered := rollingNodeOrder(nodes)
	out := make([]map[string]any, 0, len(ordered)*2)
	ord := 1
	for _, n := range ordered {
		if n.RevokedAt != nil {
			continue
		}
		out = append(out, map[string]any{"ordinal": ord, "node_id": n.ID, "name": n.Name, "action": appdb.RollingActionDrain})
		ord++
		action := map[string]any{"ordinal": ord, "node_id": n.ID, "name": n.Name, "action": appdb.RollingActionUpdate}
		if !s.applyLocal(r.Context(), clusterID, n.ID) {
			action["status"] = appdb.RollingUnavailable
			action["reason"] = workerUpdateReason
		}
		out = append(out, action)
		ord++
	}
	return out
}

func rollingNodeOrder(nodes []appdb.Node) []appdb.Node {
	out := append([]appdb.Node{}, nodes...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := nodeRole(out[i]), nodeRole(out[j])
		if ri != rj {
			return ri == cluster.RoleWorker
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func nodeRole(n appdb.Node) string {
	if n.Role == "" {
		return cluster.RoleControl
	}
	return n.Role
}

func (s *Server) runClusterUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != clusterUpdateConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "rolling update requires X-Nodal-Confirm: cluster-update")
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	nodes, err := s.Store.ListClusterNodes(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plan := appdb.RollingPlan{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Status: appdb.RollingRunning, CreatedAt: s.now(),
	}
	if err := s.Store.CreateRollingPlan(r.Context(), plan); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ord := 1
	var steps []appdb.RollingStep
	for _, n := range rollingNodeOrder(nodes) {
		if n.RevokedAt != nil {
			continue
		}
		drain, err := s.execRollingDrain(r, p.User.ClusterID, plan.ID, n, ord)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		steps = append(steps, drain)
		ord++
		upd, err := s.execRollingUpdate(r, p, plan.ID, n, ord)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		steps = append(steps, upd)
		ord++
	}
	plan.Status = appdb.RollingSucceeded
	plan.Reason = "guests were not stopped"
	for _, st := range steps {
		if st.Status == appdb.RollingFailed {
			plan.Status = appdb.RollingFailed
			plan.Reason = st.Reason
			break
		}
		if st.Status == appdb.RollingUnavailable && plan.Status != appdb.RollingFailed {
			plan.Status = appdb.RollingUnavailable
			plan.Reason = st.Reason
		}
	}
	fin := s.now()
	plan.FinishedAt = &fin
	if err := s.Store.UpdateRollingPlan(r.Context(), plan); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record rolling plan")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.update", plan.Status, plan.ID)
	writeJSON(w, http.StatusOK, rollingPlanJSON(plan, steps))
}

func (s *Server) execRollingDrain(r *http.Request, clusterID, planID string, n appdb.Node, ord int) (appdb.RollingStep, error) {
	st := appdb.RollingStep{
		ID: uuid.NewString(), PlanID: planID, ClusterID: clusterID, NodeID: n.ID,
		Ordinal: ord, Action: appdb.RollingActionDrain, Status: appdb.RollingSucceeded,
		Reason: rollingDrainReason, CreatedAt: s.now(),
	}
	_ = s.Store.SetNodeMaintenance(r.Context(), appdb.NodeMaintenance{
		NodeID: n.ID, ClusterID: clusterID, Reason: "rolling update", Since: s.now(),
	})
	workloads, _ := s.Store.ListWorkloads(r.Context(), clusterID)
	for _, wl := range workloads {
		if wl.NodeID == n.ID || wl.DesiredNodeID == n.ID {
			op := s.startOp(r.Context(), clusterID, n.ID, "workload.migrate", "queued", 0)
			op.State = "queued"
			op.Message = "queued; rolling update does not stop guests"
			if s.Migrate != nil {
				dest, err := s.migrateDest(r.Context(), clusterID, wl, "")
				if err == nil && dest != nil && s.destEligibleLocal(r.Context(), dest) {
					_, code, msg := s.runMigrate(r.Context(), wl, dest, migrateModeFor(wl))
					if code == http.StatusOK {
						op.State = "succeeded"
						op.Message = "rolling drain migrated to local dest; guests were not stopped by rolling"
					} else if msg != "" {
						op.Message = msg
					}
				} else {
					op.Message = destAgentMissing
				}
			}
			_ = s.Store.UpsertOperation(r.Context(), op)
		}
	}
	if err := s.Store.CreateRollingStep(r.Context(), st); err != nil {
		return st, errInternal("could not record rolling step")
	}
	return st, nil
}

func (s *Server) execRollingUpdate(r *http.Request, p *principal, planID string, n appdb.Node, ord int) (appdb.RollingStep, error) {
	st := appdb.RollingStep{
		ID: uuid.NewString(), PlanID: planID, ClusterID: p.User.ClusterID, NodeID: n.ID,
		Ordinal: ord, Action: appdb.RollingActionUpdate, CreatedAt: s.now(),
	}
	if !s.applyLocal(r.Context(), p.User.ClusterID, n.ID) {
		st.Status = appdb.RollingUnavailable
		st.Reason = workerUpdateReason
		if err := s.Store.CreateRollingStep(r.Context(), st); err != nil {
			return st, errInternal("could not record rolling step")
		}
		return st, nil
	}
	version := s.recordedControlVersion(r.Context(), p.User.ClusterID)
	_, op, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "apply", Channel: hostos.ChannelStable, Version: version, DryRun: false})
	if err != nil {
		return st, err
	}
	st.UpdateOperationID = op.ID
	st.Status = op.Status
	st.Reason = op.Error
	if st.Status == "" {
		st.Status = appdb.RollingSucceeded
	}
	if err := s.Store.CreateRollingStep(r.Context(), st); err != nil {
		return st, errInternal("could not record rolling step")
	}
	return st, nil
}
