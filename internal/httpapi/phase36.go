package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/appmanifest"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	storecatalog "github.com/no-dal/ndl-ce/store"
)

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
	s.seedOfficialStore(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListStorePackages(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, s.storePackageJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	s.seedOfficialStore(r.Context(), p.User.ClusterID)
	item, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || item == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	writeJSON(w, http.StatusOK, s.storePackageJSON(*item))
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
	out := s.storePackageJSON(row)
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
	s.seedOfficialStore(r.Context(), p.User.ClusterID)
	pkg, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pkg == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
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
	job := appdb.StoreInstallation{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, PackageID: pkg.ID,
		Status: appdb.StoreInstallRunning, NodeID: strings.TrimSpace(req.NodeID), CreatedAt: s.now(),
	}
	if pkg.UnsignedWarning {
		job.Warning = communityUnsignedWarn
	}
	if err := s.Store.CreateStoreInstallation(r.Context(), job); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	stackID, workloadID, instErr := s.installManifest(r.Context(), p, *m, name, req.PoolID, req.NetworkID, cpus, mem)
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

func (s *Server) installManifest(ctx context.Context, p *principal, m appmanifest.Manifest, name, poolID, networkID string, cpus int, memory int64) (stackID, workloadID string, err error) {
	desired := stackDesired{PoolID: poolID, NetworkID: networkID}
	desiredJSON, _ := json.Marshal(desired)
	stack := appdb.Stack{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name,
		Status: appdb.StackStatusDraft, DesiredJSON: desiredJSON, CreatedAt: s.now(),
	}
	if err := s.Store.CreateStack(ctx, stack); err != nil {
		return "", "", errConflict(err.Error())
	}
	md := memberDesired{
		ServiceName: m.Name, Name: name, ImagePin: m.Deployment.Image,
		NetworkID: networkID, CPUs: cpus, MemoryBytes: memory,
	}
	for _, port := range m.Ports {
		md.Ports = append(md.Ports, oci.Port{ContainerPort: port.Container, HostPort: port.Host, Protocol: "tcp"})
	}
	if err := validateMemberDesired(md); err != nil {
		_ = s.Store.DeleteStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", err
	}
	body, _ := json.Marshal(md)
	mem := appdb.StackMember{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, StackID: stack.ID,
		ServiceName: m.Name, Status: appdb.MemberStatusPending, DesiredJSON: body, CreatedAt: s.now(),
	}
	if err := s.Store.CreateStackMember(ctx, mem); err != nil {
		_ = s.Store.DeleteStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", errConflict(err.Error())
	}
	if err := s.applyStackMembers(ctx, p, stack.ID); err != nil {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", err
	}
	members, _ := s.Store.ListStackMembers(ctx, p.User.ClusterID, stack.ID)
	wlID := ""
	for _, item := range members {
		if item.WorkloadID != "" {
			wlID = item.WorkloadID
			break
		}
	}
	if wlID == "" {
		s.rollbackStoreStack(ctx, p.User.ClusterID, stack.ID)
		return "", "", errUnprocessable("install did not create a workload")
	}
	return stack.ID, wlID, nil
}

func (s *Server) rollbackStoreStack(ctx context.Context, clusterID, stackID string) {
	members, _ := s.Store.ListStackMembers(ctx, clusterID, stackID)
	for _, mem := range members {
		if mem.WorkloadID != "" {
			_ = s.Store.DeleteWorkload(ctx, clusterID, mem.WorkloadID)
		}
	}
	_ = s.Store.DeleteStack(ctx, clusterID, stackID)
}

func (s *Server) storePackageJSON(p appdb.StorePackage) map[string]any {
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
