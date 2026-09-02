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
	"github.com/no-dal/ndl-ce/internal/appmanifest"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/placement"
	"github.com/no-dal/ndl-ce/internal/rbac"
	storecatalog "github.com/no-dal/ndl-ce/store"
)

const destAgentNotConnected = "dest-agent-not-connected"

type storeStackDesired struct {
	PoolID    string            `json:"pool_id,omitempty"`
	NetworkID string            `json:"network_id,omitempty"`
	VolumeMap map[string]string `json:"volume_map,omitempty"`
	NodeID    string            `json:"node_id,omitempty"`
	GPUID     string            `json:"gpu_id,omitempty"`
}

const communityUnsignedWarn = "Unsigned Community package. Signatures and Verified class arrive in Phase 37. This still does not run helper scripts."

func (s *Server) seedOfficialStore(ctx context.Context, clusterID string) {
	official, err := storecatalog.Official()
	if err != nil {
		return
	}
	for _, f := range official {
		if existing, _ := s.Store.GetStorePackageByName(ctx, clusterID, f.Manifest.Name, f.Manifest.Version); existing != nil {
			continue
		}
		_ = s.Store.UpsertStorePackage(ctx, appdb.StorePackage{
			ID: uuid.NewString(), ClusterID: clusterID, Name: f.Manifest.Name, Version: f.Manifest.Version,
			Class: f.Manifest.Class, Title: f.Manifest.Title, Summary: f.Manifest.Summary,
			ManifestYAML: f.YAML, CreatedAt: s.now(),
		})
	}
}

func (s *Server) listStoreApps(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListStorePackages(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, s.storePackageJSON(r.Context(), item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	item, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || item == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	writeJSON(w, http.StatusOK, s.storePackageJSON(r.Context(), *item))
}

func (s *Server) importStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreInstall)
	if err != nil {
		return
	}
	var req struct {
		Manifest string `json:"manifest"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Manifest) == "" {
		writeErr(w, http.StatusBadRequest, "manifest is required")
		return
	}
	m, err := appmanifest.ParseYAML([]byte(req.Manifest))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if m.Class == appmanifest.ClassOfficial {
		m.Class = appmanifest.ClassCommunity
	}
	warn := ""
	unsigned := m.UnsignedCommunity()
	if unsigned {
		warn = communityUnsignedWarn
	}
	row := appdb.StorePackage{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: m.Name, Version: m.Version, Class: m.Class,
		Title: m.Title, Summary: m.Summary, ManifestYAML: req.Manifest, UnsignedWarning: unsigned, CreatedAt: s.now(),
	}
	if err := s.Store.UpsertStorePackage(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.import", "ok", row.ID)
	out := s.storePackageJSON(r.Context(), row)
	if warn != "" {
		out["warning"] = warn
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) installStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreInstall)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	pkg, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pkg == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	if err := s.enforceStoreTrust(r.Context(), p.User.ClusterID, *pkg); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	m, err := appmanifest.ParseYAML([]byte(pkg.ManifestYAML))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var req struct {
		Name        string `json:"name"`
		PoolID      string `json:"pool_id"`
		NetworkID   string `json:"network_id"`
		NodeID      string `json:"node_id"`
		CPUs        int    `json:"cpus"`
		MemoryBytes int64  `json:"memory_bytes"`
		GPUID       string `json:"gpu_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = m.Name
	}
	cpus := req.CPUs
	if cpus < 1 {
		cpus = m.Resources.CPU
	}
	mem := req.MemoryBytes
	if mem < 1 {
		mem = m.Resources.MemoryBytes
	}
	nodeID := strings.TrimSpace(req.NodeID)
	gpuID := strings.TrimSpace(req.GPUID)
	if err := s.requireLocalStoreDest(r.Context(), p.User.ClusterID, nodeID); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	job := appdb.StoreInstallation{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, PackageID: pkg.ID,
		Status: appdb.StoreInstallRunning, NodeID: nodeID, CreatedAt: s.now(),
	}
	if pkg.UnsignedWarning {
		job.Warning = communityUnsignedWarn
	}
	if err := s.Store.CreateStoreInstallation(r.Context(), job); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	stackID, workloadID, instErr := s.installManifest(r.Context(), p, *m, name, req.PoolID, req.NetworkID, nodeID, gpuID, cpus, mem)
	now := s.now()
	job.FinishedAt = &now
	if instErr != nil {
		job.Status = appdb.StoreInstallFailed
		job.Reason = instErr.Error()
		_ = s.Store.UpdateStoreInstallation(r.Context(), job)
		s.audit(r, p.User.ClusterID, p.User.ID, "store.install", "failed", job.ID)
		writeErr(w, statusFor(instErr), instErr.Error())
		return
	}
	job.Status = appdb.StoreInstallOK
	job.StackID = stackID
	job.WorkloadID = workloadID
	job.Reason = "installed from manifest; hooks call existing backup and compute APIs; AI actions are declarations"
	_ = s.Store.UpdateStoreInstallation(r.Context(), job)
	s.audit(r, p.User.ClusterID, p.User.ID, "store.install", "ok", job.ID)
	writeJSON(w, http.StatusCreated, s.storeInstallJSON(job, pkg))
}

func (s *Server) listStoreInstalls(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListStoreInstallations(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		pkg, _ := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, item.PackageID)
		out = append(out, s.storeInstallJSON(item, pkg))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) requireLocalStoreDest(ctx context.Context, clusterID, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	node, err := s.Store.GetNodeByID(ctx, clusterID, nodeID)
	if err != nil || node == nil {
		return errNotFound("node not found")
	}
	if !s.applyLocal(ctx, clusterID, node.ID) {
		return errFailedDependency(destAgentNotConnected)
	}
	return nil
}

func (s *Server) installManifest(ctx context.Context, p *principal, m appmanifest.Manifest, name, poolID, networkID, nodeID, gpuID string, cpus int, memory int64) (stackID, workloadID string, err error) {
	nodeID = strings.TrimSpace(nodeID)
	gpuID = strings.TrimSpace(gpuID)
	if err := s.requireLocalStoreDest(ctx, p.User.ClusterID, nodeID); err != nil {
		return "", "", err
	}
	volMap := map[string]string{}
	var volNames []string
	for _, vol := range m.Storage {
		if strings.TrimSpace(vol.Name) == "" {
			continue
		}
		volNames = append(volNames, vol.Name)
	}
	desired := storeStackDesired{PoolID: poolID, NetworkID: networkID, VolumeMap: volMap, NodeID: nodeID, GPUID: gpuID}
	desiredJSON, _ := json.Marshal(desired)
	stack := appdb.Stack{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name,
		Status: appdb.StackStatusDraft, DesiredJSON: desiredJSON, CreatedAt: s.now(),
	}
	if err := s.Store.CreateStack(ctx, stack); err != nil {
		return "", "", errConflict(err.Error())
	}
	if len(volNames) > 0 && strings.TrimSpace(poolID) != "" {
		volErr := s.resolveStackVolumes(ctx, p.User.ClusterID, poolID, volNames, volMap)
		desired.VolumeMap = volMap
		desiredJSON, _ = json.Marshal(desired)
		_ = s.Store.UpdateStack(ctx, appdb.Stack{ID: stack.ID, ClusterID: p.User.ClusterID, DesiredJSON: desiredJSON})
		if volErr != nil {
			s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
			return "", "", volErr
		}
	}
	md := memberDesired{
		ServiceName: m.Name, Name: name, ImagePin: m.Deployment.Image,
		NetworkID: networkID, CPUs: cpus, MemoryBytes: memory,
	}
	for _, port := range m.Ports {
		md.Ports = append(md.Ports, oci.Port{ContainerPort: port.Container, HostPort: port.Host, Protocol: "tcp"})
	}
	for _, volName := range volNames {
		if vid := volMap[volName]; vid != "" {
			md.Volumes = append(md.Volumes, oci.VolumeMount{VolumeID: vid, ContainerPath: "/" + volName})
		}
	}
	if err := validateMemberDesired(md); err != nil {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", err
	}
	body, _ := json.Marshal(struct {
		memberDesired
		NodeID string `json:"node_id,omitempty"`
		GPUID  string `json:"gpu_id,omitempty"`
	}{memberDesired: md, NodeID: nodeID, GPUID: gpuID})
	mem := appdb.StackMember{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, StackID: stack.ID,
		ServiceName: m.Name, Status: appdb.MemberStatusPending, DesiredJSON: body, CreatedAt: s.now(),
	}
	if err := s.Store.CreateStackMember(ctx, mem); err != nil {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", errConflict(err.Error())
	}
	req := createWorkloadRequest{
		Name: md.Name, Kind: oci.KindOCI, ImagePin: md.ImagePin, Env: md.Env, Ports: md.Ports,
		Volumes: md.Volumes, Health: md.Health, Privileged: md.Privileged, NetworkID: md.NetworkID,
		RegistryID: md.RegistryID, CPUs: md.CPUs, MemoryBytes: md.MemoryBytes, CommandSlice: md.Command,
		PoolID: poolID, NodeID: nodeID,
	}
	if nodeID != "" {
		req.Placement = placement.ModeNode
	}
	if gpuID != "" {
		req.RequireGPU = true
	}
	key := fmt.Sprintf("store-%s-member-%s", stack.ID, mem.ServiceName)
	wl, _, applyErr := s.provisionOCI(ctx, p, req, key, nil)
	if applyErr != nil {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", applyErr
	}
	if !s.applyLocal(ctx, p.User.ClusterID, wl.NodeID) {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", errFailedDependency(destAgentNotConnected)
	}
	status := memberStatusFromWorkload(wl)
	_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
		ID: mem.ID, ClusterID: p.User.ClusterID, WorkloadID: wl.ID, Status: status,
	})
	members, _ := s.Store.ListStackMembers(ctx, p.User.ClusterID, stack.ID)
	_ = s.Store.UpdateStack(ctx, appdb.Stack{ID: stack.ID, ClusterID: p.User.ClusterID, Status: deriveStackStatus(members)})
	if wl.ID == "" {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", errUnprocessable("install did not create a workload")
	}
	if gpuID != "" {
		if err := s.assignStoreGPU(ctx, p.User.ClusterID, wl.ID, gpuID); err != nil {
			s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
			return "", "", err
		}
	}
	return stack.ID, wl.ID, nil
}

func (s *Server) assignStoreGPU(ctx context.Context, clusterID, workloadID, gpuID string) error {
	id, err := gpu.ParseGPUID(gpuID)
	if err != nil {
		return errUnprocessable(err.Error())
	}
	a := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: clusterID, GPUID: id, WorkloadID: workloadID,
		Mode: gpu.ModeRender, Exclusive: gpu.ExclusiveForMode(gpu.ModeRender, true), Status: gpu.StatusAssigned,
	}
	if err := s.Store.CreateGPUAssignment(ctx, a); err != nil {
		return errConflict(err.Error())
	}
	if s.GPU == nil {
		return nil
	}
	res, err := s.GPU.GPUAssign(ctx, gpu.AssignRequest{
		Action: "assign", GPUID: id, WorkloadID: workloadID, Mode: gpu.ModeRender, Exclusive: a.Exclusive,
	})
	if err != nil {
		_ = s.Store.DeleteGPUAssignment(ctx, clusterID, a.ID)
		return err
	}
	if res.Status == gpu.StatusFailed {
		_ = s.Store.DeleteGPUAssignment(ctx, clusterID, a.ID)
		return errUnprocessable(res.Reason)
	}
	return nil
}

func (s *Server) rollbackStoreStack(ctx context.Context, clusterID, stackID string) {
	volIDs := map[string]struct{}{}
	if stack, _ := s.Store.GetStack(ctx, clusterID, stackID); stack != nil {
		var desired storeStackDesired
		if err := json.Unmarshal(stack.DesiredJSON, &desired); err == nil {
			for _, id := range desired.VolumeMap {
				if strings.TrimSpace(id) != "" {
					volIDs[id] = struct{}{}
				}
			}
		}
	}
	members, _ := s.Store.ListStackMembers(ctx, clusterID, stackID)
	for _, mem := range members {
		if mem.WorkloadID == "" {
			continue
		}
		if disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, mem.WorkloadID); len(disks) > 0 {
			for _, d := range disks {
				if strings.TrimSpace(d.VolumeID) != "" {
					volIDs[d.VolumeID] = struct{}{}
				}
			}
		}
		_ = s.Store.DeleteWorkload(ctx, clusterID, mem.WorkloadID)
	}
	for id := range volIDs {
		_ = s.Store.DeleteVolume(ctx, clusterID, id)
	}
	_ = s.Store.DeleteStack(ctx, clusterID, stackID)
}

func (s *Server) storePackageJSON(ctx context.Context, p appdb.StorePackage) map[string]any {
	out := map[string]any{
		"id": p.ID, "name": p.Name, "version": p.Version, "class": p.Class,
		"title": p.Title, "summary": p.Summary, "unsigned": p.UnsignedWarning,
	}
	if m, err := appmanifest.ParseYAML([]byte(p.ManifestYAML)); err == nil {
		out["gpu_optional"] = m.Devices.GPU.Optional
		out["deployment_kind"] = m.Deployment.Kind
		out["image"] = m.Deployment.Image
		out["hooks"] = map[string]string{"backup": m.Hooks.Backup, "restore": m.Hooks.Restore}
		if len(m.AIActions) > 0 {
			out["ai_actions"] = m.AIActions
		}
	}
	if p.UnsignedWarning {
		out["warning"] = communityUnsignedWarn
	}
	if sig, _ := s.Store.LatestPackageSignature(ctx, p.ClusterID, p.ID); sig != nil {
		out["signed"] = true
		out["payload_sha256"] = sig.PayloadSHA256
		out["key_id"] = sig.KeyID
		if key, _ := s.Store.GetSigningKey(ctx, p.ClusterID, sig.KeyID); key != nil && key.Name == officialKeyName {
			out["signer"] = "cluster-local signing key"
		}
	} else {
		out["signed"] = false
	}
	if v, _ := s.Store.LatestStoreVerification(ctx, p.ClusterID, p.ID); v != nil {
		out["trust_class"] = v.TrustClass
		out["verify_status"] = v.Status
		out["verify_reason"] = v.Reason
	}
	return out
}

func (s *Server) storeInstallJSON(in appdb.StoreInstallation, pkg *appdb.StorePackage) map[string]any {
	out := map[string]any{
		"id": in.ID, "package_id": in.PackageID, "status": in.Status,
		"stack_id": in.StackID, "workload_id": in.WorkloadID, "kubelet_started": false,
	}
	if in.Reason != "" {
		out["reason"] = in.Reason
	}
	if in.Warning != "" {
		out["warning"] = in.Warning
	}
	if in.NodeID != "" {
		out["node_id"] = in.NodeID
	}
	if in.FinishedAt != nil {
		out["finished_at"] = in.FinishedAt.UTC().Format(time.RFC3339)
	}
	if pkg != nil {
		out["name"] = pkg.Name
		out["class"] = pkg.Class
	}
	return out
}
