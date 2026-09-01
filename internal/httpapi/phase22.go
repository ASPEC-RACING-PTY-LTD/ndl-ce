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
	"github.com/no-dal/ndl-ce/internal/compose"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

const defaultStackVolumeSize = 1 << 30 // 1 GiB Directory volume for named compose volumes

type stackDesired struct {
	PoolID     string            `json:"pool_id,omitempty"`
	NetworkID  string            `json:"network_id,omitempty"`
	VolumeMap  map[string]string `json:"volume_map,omitempty"` // compose name -> volume UUID
	RegistryID string            `json:"registry_id,omitempty"`
}

type memberDesired struct {
	ServiceName string            `json:"service_name"`
	Name        string            `json:"name"`
	ImagePin    string            `json:"image_pin"`
	Env         []oci.EnvVar      `json:"env,omitempty"`
	Ports       []oci.Port        `json:"ports,omitempty"`
	Volumes     []oci.VolumeMount `json:"volumes,omitempty"`
	Privileged  bool              `json:"privileged,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Health      *oci.Healthcheck  `json:"health,omitempty"`
	NetworkID   string            `json:"network_id,omitempty"`
	RegistryID  string            `json:"registry_id,omitempty"`
	CPUs        int               `json:"cpus,omitempty"`
	MemoryBytes int64             `json:"memory_bytes,omitempty"`
}

func (s *Server) listStacks(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListStacks(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, s.stackJSON(r.Context(), item, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createStack(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	row := appdb.Stack{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		Status: appdb.StackStatusDraft, DesiredJSON: json.RawMessage(`{}`), CreatedAt: s.now(),
	}
	if err := s.Store.CreateStack(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "stack.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, s.stackJSON(r.Context(), row, nil))
}

func (s *Server) getStack(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	row, err := s.Store.GetStack(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "stack not found")
		return
	}
	members, _ := s.Store.ListStackMembers(r.Context(), p.User.ClusterID, row.ID)
	s.refreshMemberStatuses(r.Context(), p.User.ClusterID, members)
	members, _ = s.Store.ListStackMembers(r.Context(), p.User.ClusterID, row.ID)
	writeJSON(w, http.StatusOK, s.stackJSON(r.Context(), *row, members))
}

func (s *Server) patchStack(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	row, err := s.Store.GetStack(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "stack not found")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	upd := appdb.Stack{ID: row.ID, ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name)}
	if err := s.Store.UpdateStack(r.Context(), upd); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	updated, _ := s.Store.GetStack(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	members, _ := s.Store.ListStackMembers(r.Context(), p.User.ClusterID, row.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "stack.update", "ok", row.ID)
	writeJSON(w, http.StatusOK, s.stackJSON(r.Context(), *updated, members))
}

func (s *Server) deleteStack(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeDelete)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.DeleteStack(r.Context(), p.User.ClusterID, id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "stack.delete", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (s *Server) importStackCompose(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req struct {
		Name       string            `json:"name"`
		Compose    string            `json:"compose"`
		PoolID     string            `json:"pool_id"`
		NetworkID  string            `json:"network_id"`
		RegistryID string            `json:"registry_id"`
		VolumeMap  map[string]string `json:"volume_map"`
		Apply      bool              `json:"apply"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Compose) == "" {
		writeErr(w, http.StatusBadRequest, "name and compose are required")
		return
	}
	parsed, err := compose.ParseYAML([]byte(req.Compose))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, svc := range parsed.Services {
		if svc.Privileged && !hasRole(p, rbac.Admin) {
			s.audit(r, p.User.ClusterID, p.User.ID, "stack.import.privileged", "denied", svc.Name)
			writeErr(w, http.StatusForbidden, "only admin may import privileged services")
			return
		}
	}
	volMap := map[string]string{}
	for k, v := range req.VolumeMap {
		volMap[k] = v
	}
	if err := s.resolveStackVolumes(r.Context(), p.User.ClusterID, req.PoolID, parsed.NamedVolumes, volMap); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	desired := stackDesired{
		PoolID: req.PoolID, NetworkID: req.NetworkID, VolumeMap: volMap, RegistryID: req.RegistryID,
	}
	desiredJSON, _ := json.Marshal(desired)
	stack := appdb.Stack{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		Status: appdb.StackStatusDraft, DesiredJSON: desiredJSON, SourceCompose: req.Compose, CreatedAt: s.now(),
	}
	if err := s.Store.CreateStack(r.Context(), stack); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	for i, svc := range parsed.Services {
		md := memberDesired{
			ServiceName: svc.Name,
			Name:        stackWorkloadName(stack.Name, svc.Name),
			ImagePin:    svc.Image,
			Env:         svc.Env,
			Privileged:  svc.Privileged,
			Command:     svc.Command,
			Health:      svc.Health,
			NetworkID:   req.NetworkID,
			RegistryID:  req.RegistryID,
		}
		for _, pmap := range svc.Ports {
			md.Ports = append(md.Ports, oci.Port{
				ContainerPort: pmap.ContainerPort, HostPort: pmap.HostPort, Protocol: pmap.Protocol,
			})
		}
		for _, v := range svc.Volumes {
			vid := volMap[v.Name]
			if vid == "" {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("volume %q is not mapped", v.Name))
				_ = s.Store.DeleteStack(r.Context(), p.User.ClusterID, stack.ID)
				return
			}
			md.Volumes = append(md.Volumes, oci.VolumeMount{
				VolumeID: vid, ContainerPath: v.ContainerPath, ReadOnly: v.ReadOnly,
			})
		}
		body, _ := json.Marshal(md)
		mem := appdb.StackMember{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, StackID: stack.ID,
			ServiceName: svc.Name, DesiredJSON: body, Status: appdb.MemberStatusPending,
			SortOrder: i, CreatedAt: s.now(),
		}
		if err := s.Store.CreateStackMember(r.Context(), mem); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "stack.import", "ok", stack.ID)
	if req.Apply {
		if err := s.applyStackMembers(r.Context(), p, stack.ID); err != nil {
			// Import succeeded; apply may be partial. Return current stack with honest status.
			_ = err
		}
	}
	updated, _ := s.Store.GetStack(r.Context(), p.User.ClusterID, stack.ID)
	if updated == nil {
		updated = &stack
	}
	members, _ := s.Store.ListStackMembers(r.Context(), p.User.ClusterID, stack.ID)
	writeJSON(w, http.StatusCreated, s.stackJSON(r.Context(), *updated, members))
}

func (s *Server) applyStack(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	row, err := s.Store.GetStack(r.Context(), p.User.ClusterID, id)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "stack not found")
		return
	}
	if err := s.applyStackMembers(r.Context(), p, row.ID); err != nil {
		updated, _ := s.Store.GetStack(r.Context(), p.User.ClusterID, row.ID)
		if updated == nil {
			updated = row
		}
		members, _ := s.Store.ListStackMembers(r.Context(), p.User.ClusterID, row.ID)
		writeJSON(w, http.StatusOK, s.stackJSON(r.Context(), *updated, members))
		return
	}
	updated, _ := s.Store.GetStack(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	members, _ := s.Store.ListStackMembers(r.Context(), p.User.ClusterID, row.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "stack.apply", "ok", row.ID)
	writeJSON(w, http.StatusOK, s.stackJSON(r.Context(), *updated, members))
}

func (s *Server) applyStackMembers(ctx context.Context, p *principal, stackID string) error {
	_ = s.Store.UpdateStack(ctx, appdb.Stack{ID: stackID, ClusterID: p.User.ClusterID, Status: appdb.StackStatusApplying})
	members, err := s.Store.ListStackMembers(ctx, p.User.ClusterID, stackID)
	if err != nil {
		return err
	}
	var firstErr error
	ready := 0
	for _, mem := range members {
		if mem.WorkloadID != "" {
			wl, _ := s.Store.GetWorkload(ctx, p.User.ClusterID, mem.WorkloadID)
			if wl != nil {
				status := memberStatusFromWorkload(*wl)
				_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
					ID: mem.ID, ClusterID: p.User.ClusterID, WorkloadID: mem.WorkloadID, Status: status,
				})
				if status == appdb.MemberStatusReady || status == appdb.MemberStatusCollecting {
					ready++
				}
				continue
			}
		}
		var md memberDesired
		if err := json.Unmarshal(mem.DesiredJSON, &md); err != nil {
			_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
				ID: mem.ID, ClusterID: p.User.ClusterID, Status: appdb.MemberStatusFailed, Reason: "invalid member desired state",
			})
			if firstErr == nil {
				firstErr = errBadRequest("invalid member desired state")
			}
			continue
		}
		if md.Privileged && !hasRole(p, rbac.Admin) {
			_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
				ID: mem.ID, ClusterID: p.User.ClusterID, Status: appdb.MemberStatusFailed,
				Reason: "only admin may create privileged containers",
			})
			if firstErr == nil {
				firstErr = errForbidden("only admin may create privileged containers")
			}
			continue
		}
		_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
			ID: mem.ID, ClusterID: p.User.ClusterID, Status: appdb.MemberStatusCreating,
		})
		req := createWorkloadRequest{
			Name: md.Name, Kind: oci.KindOCI, ImagePin: md.ImagePin, Env: md.Env, Ports: md.Ports,
			Volumes: md.Volumes, Health: md.Health, Privileged: md.Privileged, NetworkID: md.NetworkID,
			RegistryID: md.RegistryID, CPUs: md.CPUs, MemoryBytes: md.MemoryBytes, CommandSlice: md.Command,
		}
		key := fmt.Sprintf("stack-%s-member-%s", stackID, mem.ServiceName)
		wl, _, err := s.provisionOCI(ctx, p, req, key, nil)
		if err != nil {
			_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
				ID: mem.ID, ClusterID: p.User.ClusterID, Status: appdb.MemberStatusFailed, Reason: err.Error(),
			})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		status := memberStatusFromWorkload(wl)
		_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
			ID: mem.ID, ClusterID: p.User.ClusterID, WorkloadID: wl.ID, Status: status,
		})
		if status == appdb.MemberStatusReady || status == appdb.MemberStatusCollecting {
			ready++
		}
	}
	members, _ = s.Store.ListStackMembers(ctx, p.User.ClusterID, stackID)
	stackStatus := deriveStackStatus(members)
	_ = s.Store.UpdateStack(ctx, appdb.Stack{ID: stackID, ClusterID: p.User.ClusterID, Status: stackStatus})
	_ = ready
	return firstErr
}

func (s *Server) resolveStackVolumes(ctx context.Context, clusterID, poolID string, names []string, volMap map[string]string) error {
	for _, name := range names {
		if volMap[name] != "" {
			vol, err := s.Store.GetVolume(ctx, clusterID, volMap[name])
			if err != nil || vol == nil {
				return errNotFound(fmt.Sprintf("volume_map %q not found", name))
			}
			continue
		}
		if strings.TrimSpace(poolID) == "" {
			return errBadRequest("pool_id is required to create Directory volumes for named compose volumes")
		}
		pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
		if err != nil || pool == nil {
			return errNotFound("pool not found")
		}
		if s.Storage == nil {
			return errUnavailable("storage agent is unavailable")
		}
		volID := uuid.NewString()
		hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: volID, PoolID: pool.ID, RootPath: pool.RootPath,
			Class: storage.ClassContainerRoot, Size: defaultStackVolumeSize, Format: storage.FormatDirectory,
		}, hint)
		if err != nil {
			return errBadRequest(err.Error())
		}
		row := appdb.Volume{
			ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
			Class: res.Handle.Class, Kind: res.Handle.Kind, Format: res.Handle.Format,
			SizeBytes: defaultStackVolumeSize, Status: storage.StatusAvailable,
			BackendType: res.Handle.BackendType, BackendRef: res.Handle.BackendRef,
			XattrState: res.XattrState, AllocatedBytes: &res.Allocated,
		}
		if row.Class == "" {
			row.Class = storage.ClassContainerRoot
			row.Kind = storage.KindFilesystem
			row.Format = storage.FormatDirectory
			row.BackendType = storage.BackendDirectory
			row.BackendRef = "volumes/container-root/" + volID
		}
		if err := s.Store.CreateVolume(ctx, row); err != nil {
			return errConflict(err.Error())
		}
		volMap[name] = volID
	}
	return nil
}

func (s *Server) refreshMemberStatuses(ctx context.Context, clusterID string, members []appdb.StackMember) {
	for _, mem := range members {
		if mem.WorkloadID == "" {
			continue
		}
		wl, _ := s.Store.GetWorkload(ctx, clusterID, mem.WorkloadID)
		if wl == nil {
			_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
				ID: mem.ID, ClusterID: clusterID, Status: appdb.MemberStatusUnavailable, Reason: "workload missing",
			})
			continue
		}
		status := memberStatusFromWorkload(*wl)
		_ = s.Store.UpdateStackMember(ctx, appdb.StackMember{
			ID: mem.ID, ClusterID: clusterID, WorkloadID: mem.WorkloadID, Status: status,
		})
	}
}

func memberStatusFromWorkload(wl appdb.Workload) string {
	switch wl.Status {
	case oci.StatusRunning, oci.StatusStopped:
		return appdb.MemberStatusReady
	case oci.StatusCollecting:
		return appdb.MemberStatusCollecting
	case oci.StatusUnavailable:
		return appdb.MemberStatusUnavailable
	case oci.StatusFailed:
		return appdb.MemberStatusFailed
	default:
		if wl.Status == "" {
			return appdb.MemberStatusCollecting
		}
		return appdb.MemberStatusCollecting
	}
}

func deriveStackStatus(members []appdb.StackMember) string {
	if len(members) == 0 {
		return appdb.StackStatusDraft
	}
	ready, failed, pending, creating := 0, 0, 0, 0
	for _, m := range members {
		switch m.Status {
		case appdb.MemberStatusReady, appdb.MemberStatusCollecting:
			ready++
		case appdb.MemberStatusFailed, appdb.MemberStatusUnavailable:
			failed++
		case appdb.MemberStatusCreating:
			creating++
		default:
			pending++
		}
	}
	switch {
	case ready == len(members):
		return appdb.StackStatusApplied
	case ready > 0 && (failed > 0 || pending > 0 || creating > 0):
		return appdb.StackStatusPartial
	case failed == len(members):
		return appdb.StackStatusFailed
	case creating > 0:
		return appdb.StackStatusApplying
	default:
		return appdb.StackStatusPartial
	}
}

func stackWorkloadName(stackName, service string) string {
	base := strings.TrimSpace(stackName) + "-" + strings.TrimSpace(service)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if len(base) > 63 {
		base = base[:63]
	}
	return strings.Trim(base, "-")
}

func (s *Server) stackJSON(ctx context.Context, stack appdb.Stack, members []appdb.StackMember) map[string]any {
	out := map[string]any{
		"id": stack.ID, "name": stack.Name, "status": stack.Status,
		"created_at": stack.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": stack.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if len(stack.DesiredJSON) > 0 {
		var desired any
		if json.Unmarshal(stack.DesiredJSON, &desired) == nil {
			out["desired"] = desired
		}
	}
	// Source compose is available for inspect; it is not runtime SoT.
	if stack.SourceCompose != "" {
		out["has_source_compose"] = true
	}
	memberOut := make([]map[string]any, 0, len(members))
	for _, mem := range members {
		item := map[string]any{
			"id": mem.ID, "service_name": mem.ServiceName, "status": mem.Status,
			"sort_order": mem.SortOrder, "workload_id": nil,
		}
		if mem.WorkloadID != "" {
			item["workload_id"] = mem.WorkloadID
			if wl, _ := s.Store.GetWorkload(ctx, stack.ClusterID, mem.WorkloadID); wl != nil {
				item["workload"] = map[string]any{
					"id": wl.ID, "name": wl.Name, "kind": wl.Kind, "status": wl.Status,
					"image_pin": wl.ImagePin, "health": ociHealthFromWorkload(*wl),
				}
			} else {
				item["workload"] = map[string]any{
					"id": mem.WorkloadID, "status": oci.StatusUnavailable, "kind": oci.KindOCI,
				}
			}
		}
		if mem.Reason != "" {
			item["reason"] = mem.Reason
		}
		if len(mem.DesiredJSON) > 0 {
			var desired any
			if json.Unmarshal(mem.DesiredJSON, &desired) == nil {
				item["desired"] = desired
			}
		}
		memberOut = append(memberOut, item)
	}
	if members != nil {
		out["members"] = memberOut
	}
	return out
}
