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
	"github.com/no-dal/ndl-ce/internal/ndnet"
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
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	row, created, err := s.provisionOCI(r.Context(), p, req, key, func(action, outcome, detail string) {
		s.audit(r, p.User.ClusterID, p.User.ID, action, outcome, detail)
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if created {
		writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), row))
		return
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), row))
}

// provisionOCI creates an OCI workload via agent RPC. Stack apply reuses this path.
// created is false when an existing workload is returned idempotently.
func (s *Server) provisionOCI(ctx context.Context, p *principal, req createWorkloadRequest, key string, audit func(action, outcome, detail string)) (appdb.Workload, bool, error) {
	if audit == nil {
		audit = func(string, string, string) {}
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ImagePin = strings.TrimSpace(req.ImagePin)
	if req.Name == "" || req.ImagePin == "" {
		return appdb.Workload{}, false, errBadRequest("name and image_pin are required")
	}
	if err := oci.ValidateImageRef(req.ImagePin); err != nil {
		return appdb.Workload{}, false, errBadRequest(err.Error())
	}
	if req.Privileged && !hasRole(p, rbac.Admin) {
		audit("workload.create.privileged", "denied", "operator cannot create privileged containers")
		return appdb.Workload{}, false, errForbidden("only admin may create privileged containers")
	}
	if strings.TrimSpace(req.HostPath) != "" {
		return appdb.Workload{}, false, errBadRequest("host_path mounts are not allowed; use volume_ids")
	}
	node, local, err := s.placeCreate(ctx, p.User.ClusterID, req)
	if err != nil {
		return appdb.Workload{}, false, err
	}
	if !local {
		id := uuid.NewString()
		row := remotePlacedWorkload(p.User.ClusterID, node, req, id, oci.KindOCI)
		if err := s.Store.CreateWorkload(ctx, row); err != nil {
			return appdb.Workload{}, false, errConflict("could not record workload")
		}
		s.recordPlacement(ctx, p.User.ClusterID, row.ID, req)
		audit("workload.create", "ok", row.ID)
		return row, true, nil
	}
	rpc := s.ociRPC()
	if existing, _ := s.Store.GetWorkloadByName(ctx, p.User.ClusterID, req.Name); existing != nil {
		return *existing, false, nil
	}
	if key != "" {
		if existing, _ := s.Store.GetWorkloadByIdempotency(ctx, p.User.ClusterID, key); existing != nil {
			return *existing, false, nil
		}
	}
	var regURL string
	var specPullUser, specPullPass string
	if req.RegistryID != "" {
		reg, err := s.Store.GetRegistry(ctx, p.User.ClusterID, req.RegistryID)
		if err != nil || reg == nil {
			return appdb.Workload{}, false, errNotFound("registry not found")
		}
		regURL = reg.URL
		user, pass, _ := s.Store.RegistrySecrets(ctx, p.User.ClusterID, req.RegistryID)
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
			return appdb.Workload{}, false, errBadRequest(err.Error())
		}
		vol, err := s.Store.GetVolume(ctx, p.User.ClusterID, m.VolumeID)
		if err != nil || vol == nil {
			return appdb.Workload{}, false, errNotFound("volume not found")
		}
		if vol.Class != storage.ClassContainerRoot {
			return appdb.Workload{}, false, errConflict("volume is not a container-root")
		}
		if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
			return appdb.Workload{}, false, errConflict("storage is unavailable")
		}
		pool, err := s.Store.GetStoragePool(ctx, p.User.ClusterID, vol.PoolID)
		if err != nil || pool == nil {
			return appdb.Workload{}, false, errUnprocessable("volume pool unavailable")
		}
		if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
			return appdb.Workload{}, false, errConflict("storage is unavailable")
		}
		loc, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
		if err != nil {
			return appdb.Workload{}, false, errUnprocessable("volume locator is invalid")
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
		n, err := s.Store.GetNetwork(ctx, p.User.ClusterID, req.NetworkID)
		if err != nil || n == nil {
			return appdb.Workload{}, false, errNotFound("network not found")
		}
		if n.Status != ndnet.StatusAvailable && n.Status != ndnet.StatusWarning {
			return appdb.Workload{}, false, errConflict("an available network is required")
		}
		netw = n
	}
	ids := s.planCreateIDs(ctx, p.User.ClusterID, node.ID, key, "")
	spec := oci.Spec{
		WorkloadID: ids.WorkloadID, Name: req.Name, ImagePin: req.ImagePin,
		RegistryID: req.RegistryID, RegistryURL: regURL, Ports: req.Ports, Env: req.Env,
		SecretRefs: req.SecretRefs, Volumes: vols, Health: req.Health, Privileged: req.Privileged,
		VolumePaths: volumePaths, Resources: oci.Resources{CPUs: req.CPUs, MemoryBytes: req.MemoryBytes},
		PullUsername: specPullUser, PullPassword: specPullPass, Command: req.CommandSlice,
	}
	if netw != nil {
		spec.NetworkID = netw.ID
		spec.BridgeName = netw.BridgeName
	}
	if err := oci.ValidateSpec(spec); err != nil {
		return appdb.Workload{}, false, errBadRequest(err.Error())
	}
	op := s.startOpKeyed(ctx, p.User.ClusterID, node.ID, "workload.create", "creating", key, mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 20)
	if req.Privileged {
		audit("workload.create.privileged", "ok", ids.WorkloadID)
	}
	res, err := rpc.CreateOCI(ctx, spec)
	if err != nil {
		s.finishOp(ctx, op, "failed", err.Error(), 0)
		audit("workload.create", "denied", err.Error())
		return appdb.Workload{}, false, err
	}
	if existing, _ := s.Store.GetWorkload(ctx, p.User.ClusterID, ids.WorkloadID); existing != nil {
		s.finishOp(ctx, op, "succeeded", mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 100)
		return *existing, false, nil
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
		Devices: json.RawMessage(`[]`), MigrateBlockers: json.RawMessage(`["OCI migrate recreates the container; live is not supported"]`),
		CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	if row.Status == "" {
		row.Status = oci.StatusCollecting
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		s.finishOp(ctx, op, "failed", err.Error(), 0)
		return appdb.Workload{}, false, errConflict(err.Error())
	}
	for _, m := range vols {
		if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			VolumeID: m.VolumeID, Role: "data", Format: storage.FormatDirectory, CreatedAt: s.now(),
		}); err != nil {
			s.finishOp(ctx, op, "failed", err.Error(), 0)
			return appdb.Workload{}, false, errInternal("could not record OCI disk")
		}
	}
	if netw != nil {
		if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			NetworkID: netw.ID, CreatedAt: s.now(),
		}); err != nil {
			s.finishOp(ctx, op, "failed", err.Error(), 0)
			return appdb.Workload{}, false, errInternal("could not record OCI NIC")
		}
	}
	s.finishOp(ctx, op, "succeeded", mustCreateMsg(createIDs{WorkloadID: ids.WorkloadID}), 100)
	audit("workload.create", "ok", row.ID)
	return row, true, nil
}

func (s *Server) ociLifecycle(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, action string) {
	if action == "clone" {
		writeErr(w, http.StatusUnprocessableEntity, "OCI clone is not implemented")
		return
	}
	if !s.guardLocalApply(w, r, p.User.ClusterID, firstNonEmpty(row.DesiredNodeID, row.NodeID), action) {
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
