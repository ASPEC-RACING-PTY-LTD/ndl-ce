package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// OCIRPC is the privileged agent surface for OCI application containers.
type OCIRPC interface {
	CreateOCI(ctx context.Context, spec oci.Spec) (oci.Result, error)
	LifecycleOCI(ctx context.Context, req oci.LifecycleRequest) (oci.Result, error)
}

type ociUnavailable struct{}

func (ociUnavailable) CreateOCI(context.Context, oci.Spec) (oci.Result, error) {
	return oci.Result{}, errUnavailable("oci agent is unavailable")
}
func (ociUnavailable) LifecycleOCI(context.Context, oci.LifecycleRequest) (oci.Result, error) {
	return oci.Result{}, errUnavailable("oci agent is unavailable")
}

func AdaptOCI(client any) OCIRPC {
	if v, ok := client.(OCIRPC); ok {
		return v
	}
	return ociUnavailable{}
}

func (s *Server) ociRPC() OCIRPC {
	if s.OCI != nil {
		return s.OCI
	}
	return AdaptOCI(s.Agent)
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListRegistries(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, registryJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
		Insecure bool   `json:"insecure"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := oci.ValidateRegistryURL(req.URL, req.Insecure); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row := appdb.Registry{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		URL: strings.TrimSpace(req.URL), Insecure: req.Insecure, Status: appdb.RegistryConfigured,
		CreatedAt: s.now(),
	}
	if err := s.Store.CreateRegistry(r.Context(), row, req.Username, req.Password); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "registry.create", "ok", row.ID)
	created, _ := s.Store.GetRegistry(r.Context(), p.User.ClusterID, row.ID)
	if created == nil {
		created = &row
		created.HasCredentials = req.Username != "" || req.Password != ""
	}
	writeJSON(w, http.StatusCreated, registryJSON(*created))
}

func registryJSON(r appdb.Registry) map[string]any {
	return map[string]any{
		"id": r.ID, "name": r.Name, "url": r.URL, "insecure": r.Insecure,
		"has_credentials": r.HasCredentials, "status": r.Status,
		"created_at": r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) createOCIWorkload(w http.ResponseWriter, r *http.Request, p *principal, req createWorkloadRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.ImagePin = strings.TrimSpace(req.ImagePin)
	if req.Name == "" || req.ImagePin == "" {
		writeErr(w, http.StatusBadRequest, "name and image_pin are required")
		return
	}
	if err := oci.ValidateImageRef(req.ImagePin); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Privileged && !hasRole(p, rbac.Admin) {
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create.privileged", "denied", "operator cannot create privileged containers")
		writeErr(w, http.StatusForbidden, "only admin may create privileged containers")
		return
	}
	if strings.TrimSpace(req.HostPath) != "" {
		writeErr(w, http.StatusBadRequest, "host_path mounts are not allowed; use volume_ids")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	rpc := s.ociRPC()
	if existing, _ := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, req.Name); existing != nil {
		writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key != "" {
		if existing, _ := s.Store.GetWorkloadByIdempotency(r.Context(), p.User.ClusterID, key); existing != nil {
			writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
			return
		}
	}
	var regURL string
	var specPullUser, specPullPass string
	if req.RegistryID != "" {
		reg, err := s.Store.GetRegistry(r.Context(), p.User.ClusterID, req.RegistryID)
		if err != nil || reg == nil {
			writeErr(w, http.StatusNotFound, "registry not found")
			return
		}
		regURL = reg.URL
		user, pass, _ := s.Store.RegistrySecrets(r.Context(), p.User.ClusterID, req.RegistryID)
		specPullUser, specPullPass = user, pass
	}
	vols := append([]oci.VolumeMount{}, req.Volumes...)
	if len(vols) == 0 {
		for i, id := range req.VolumeIDs {
			vols = append(vols, oci.VolumeMount{
				VolumeID: strings.TrimSpace(id), ContainerPath: fmt.Sprintf("/data/vol%d", i),
			})
		}
	}
	volumePaths := map[string]string{}
	for _, m := range vols {
		if err := oci.ValidateVolumeMount(m); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		vol, err := s.Store.GetVolume(r.Context(), p.User.ClusterID, m.VolumeID)
		if err != nil || vol == nil {
			writeErr(w, http.StatusNotFound, "volume not found")
			return
		}
		pool, err := s.Store.GetStoragePool(r.Context(), p.User.ClusterID, vol.PoolID)
		if err != nil || pool == nil {
			writeErr(w, http.StatusUnprocessableEntity, "volume pool unavailable")
			return
		}
		loc, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "volume locator is invalid")
			return
		}
		volumePaths[m.VolumeID] = loc
	}
	if req.CPUs < 1 {
		req.CPUs = oci.DefaultCPUs
	}
	if req.MemoryBytes < 1 {
		req.MemoryBytes = oci.DefaultMemoryBytes
	}
	if req.DesiredPower == "" {
		req.DesiredPower = "running"
	}
	var netw *appdb.Network
	if req.NetworkID != "" {
		n, err := s.Store.GetNetwork(r.Context(), p.User.ClusterID, req.NetworkID)
		if err != nil || n == nil {
			writeErr(w, http.StatusNotFound, "network not found")
			return
		}
		netw = n
	}
	ids := s.planCreateIDs(r.Context(), p.User.ClusterID, node.ID, key, "")
	spec := oci.Spec{
		WorkloadID: ids.WorkloadID, Name: req.Name, ImagePin: req.ImagePin,
		RegistryID: req.RegistryID, RegistryURL: regURL, Ports: req.Ports, Env: req.Env,
		SecretRefs: req.SecretRefs, Volumes: vols, Health: req.Health, Privileged: req.Privileged,
		VolumePaths: volumePaths, Resources: oci.Resources{CPUs: req.CPUs, MemoryBytes: req.MemoryBytes},
		PullUsername: specPullUser, PullPassword: specPullPass,
	}
	if netw != nil {
		spec.NetworkID = netw.ID
		spec.BridgeName = netw.BridgeName
	}
	if err := oci.ValidateSpec(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	op := s.startOpKeyed(r.Context(), p.User.ClusterID, node.ID, "workload.create", "creating", key, mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 20)
	if req.Privileged {
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create.privileged", "ok", ids.WorkloadID)
	}
	res, err := rpc.CreateOCI(r.Context(), spec)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create", "denied", err.Error())
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if existing, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, ids.WorkloadID); existing != nil {
		s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 100)
		writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
		return
	}
	health := res.Health
	if health.Status == "" {
		health = oci.Health{Status: oci.StatusNotConfigured, Message: "healthcheck not configured"}
		if req.Health != nil && (req.Health.HTTPPath != "" || req.Health.Port > 0) {
			health = oci.Health{Status: oci.StatusCollecting, Message: "healthcheck configured; awaiting observation"}
		}
	}
	applied, _ := json.Marshal(map[string]any{
		"schema_version": oci.LastAppliedSchema,
		"spec":           oci.Redact(spec),
		"image_digest":   res.ImageDigest,
		"health":         health,
	})
	specJSON, _ := json.Marshal(oci.Redact(spec))
	row := appdb.Workload{
		ID: ids.WorkloadID, ClusterID: p.User.ClusterID, NodeID: node.ID,
		OwnerNodeID: node.ID, DesiredNodeID: node.ID, Name: req.Name, Kind: oci.KindOCI,
		Status: res.Status, DesiredPower: req.DesiredPower, ImagePin: req.ImagePin,
		CPUs: req.CPUs, MemoryBytes: req.MemoryBytes, Privileged: req.Privileged,
		IdempotencyKey: key, SpecJSON: specJSON, AppliedJSON: applied,
		Devices: json.RawMessage(`[]`), MigrateBlockers: json.RawMessage(`["OCI recreate migrate is Phase 32"]`),
		CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	if row.Status == "" {
		row.Status = oci.StatusCollecting
	}
	if err := s.Store.CreateWorkload(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	for _, m := range vols {
		_ = s.Store.CreateWorkloadDisk(r.Context(), appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			VolumeID: m.VolumeID, Role: "data", Format: storage.FormatDirectory, CreatedAt: s.now(),
		})
	}
	if netw != nil {
		_ = s.Store.CreateWorkloadNIC(r.Context(), appdb.WorkloadNIC{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			NetworkID: netw.ID, CreatedAt: s.now(),
		})
	}
	s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), row))
}

func (s *Server) ociLifecycle(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, action string) {
	if action == "clone" {
		writeErr(w, http.StatusUnprocessableEntity, "OCI clone is not implemented")
		return
	}
	rpc := s.ociRPC()
	op := s.startOp(r.Context(), p.User.ClusterID, row.NodeID, "workload."+action, action, 40)
	res, err := rpc.LifecycleOCI(r.Context(), oci.LifecycleRequest{WorkloadID: row.ID, Action: action})
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, statusFor(err), err.Error())
		return
	}
	switch action {
	case "start", "restart":
		_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, DesiredPower: "running"})
	case "stop", "delete":
		_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, DesiredPower: "stopped"})
	}
	_ = res
	s.finishOp(r.Context(), op, "succeeded", action, 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "workload."+action, "ok", row.ID)
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = &row
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
}

func ociHealthFromWorkload(w appdb.Workload) map[string]any {
	health := map[string]any{"status": oci.StatusUnavailable, "message": "not observed"}
	if len(w.AppliedJSON) > 0 {
		var applied struct {
			Health oci.Health `json:"health"`
			Spec   oci.Spec   `json:"spec"`
		}
		if json.Unmarshal(w.AppliedJSON, &applied) == nil && applied.Health.Status != "" {
			health["status"] = applied.Health.Status
			health["message"] = applied.Health.Message
		}
		if applied.Spec.Health == nil || (applied.Spec.Health.HTTPPath == "" && applied.Spec.Health.Port == 0) {
			if w.Status == oci.StatusUnavailable {
				return map[string]any{"status": oci.StatusUnavailable, "message": "runtime unavailable"}
			}
			return map[string]any{"status": oci.StatusNotConfigured, "message": "healthcheck not configured"}
		}
	}
	if w.Status == oci.StatusUnavailable {
		return map[string]any{"status": oci.StatusUnavailable, "message": w.Reason}
	}
	return health
}
