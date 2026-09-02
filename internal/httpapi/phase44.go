package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

type migrationJobBody struct {
	SourceID            string                       `json:"source_id"`
	Adapter             string                       `json:"adapter"`
	Direction           string                       `json:"direction"`
	Selected            []string                     `json:"selected"`
	Modes               map[string]string            `json:"modes"`
	Mapping             migration.Mapping            `json:"mapping"`
	Overrides           map[string]migration.Mapping `json:"overrides"`
	LiveAck             map[string]bool              `json:"live_ack"`
	IdentityConflictAck map[string]bool              `json:"identity_conflict_ack"`
	StartAfter          bool                         `json:"start_after"`
	Path                string                       `json:"path"`
	XMLPath             string                       `json:"xml_path"`
	Name                string                       `json:"name"`
	Kind                string                       `json:"kind"`
	CPUs                int                          `json:"cpus"`
	MemoryBytes         int64                        `json:"memory_bytes"`
	Firmware            string                       `json:"firmware"`
	PoolID              string                       `json:"pool_id"`
	NetworkID           string                       `json:"network_id"`
	Format              string                       `json:"format"`
	WorkloadID          string                       `json:"workload_id"`
	ExportKind          string                       `json:"export_kind"`
	DestPath            string                       `json:"dest_path"`
	Mode                string                       `json:"mode"`
}

func (s *Server) listMigrationAdapters(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.MigrationRead); err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": migration.Catalog()})
}

func (s *Server) listMigrationModes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.MigrationRead); err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":         migration.Modes(),
		"source_safety": migration.SourceProtected,
		"source_policy": "No-dal does not delete or clean up source infrastructure. A completed migration means: Migration verified. Source remains unchanged.",
	})
}

func (s *Server) listMigrationSources(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListMigrationSources(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, src := range items {
		_, token, username, _, _ := s.Store.GetMigrationSource(r.Context(), p.User.ClusterID, src.ID)
		out = append(out, s.migrationSourceJSON(src, token != "" || username != ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createMigrationSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationManage)
	if err != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := migration.RejectDestructiveRequest(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Adapter  string `json:"adapter"`
		Label    string `json:"label"`
		Endpoint string `json:"endpoint"`
		Token    string `json:"token"`
		Username string `json:"username"`
		Insecure bool   `json:"insecure"`
	}
	if err := json.Unmarshal(raw, &req); err != nil || strings.TrimSpace(req.Adapter) == "" {
		writeErr(w, http.StatusBadRequest, "adapter is required")
		return
	}
	if _, err := migration.AdapterByID(req.Adapter); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	src := appdb.MigrationSource{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Adapter: req.Adapter,
		Label: req.Label, Endpoint: strings.TrimSpace(req.Endpoint), Insecure: req.Insecure,
	}
	if src.Label == "" {
		src.Label = req.Adapter
	}
	if err := s.Store.CreateMigrationSource(r.Context(), src, strings.TrimSpace(req.Token), strings.TrimSpace(req.Username), nil); err != nil {
		writeErr(w, http.StatusConflict, "could not record source")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.source.create", "ok", src.Adapter)
	writeJSON(w, http.StatusCreated, s.migrationSourceJSON(src, strings.TrimSpace(req.Token) != "" || strings.TrimSpace(req.Username) != ""))
}

func (s *Server) getMigrationSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	src, token, username, _, err := s.Store.GetMigrationSource(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || src == nil {
		writeErr(w, http.StatusNotFound, "migration source not found")
		return
	}
	writeJSON(w, http.StatusOK, s.migrationSourceJSON(*src, token != "" || username != ""))
}

func (s *Server) deleteMigrationSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationManage)
	if err != nil {
		return
	}
	if err := s.Store.DeleteMigrationSource(r.Context(), p.User.ClusterID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "migration source not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.source.delete", "ok", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": "Stored credentials were removed. Source infrastructure was not changed."})
}

func (s *Server) discoverMigrationSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationImport)
	if err != nil {
		return
	}
	src, token, username, _, err := s.Store.GetMigrationSource(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || src == nil {
		writeErr(w, http.StatusNotFound, "migration source not found")
		return
	}
	d, err := s.discoverFrom(src, token, username)
	if err != nil {
		writeErr(w, statusFor(errUnprocessable(err.Error())), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.discover", "ok", src.Adapter)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) discoverFrom(src *appdb.MigrationSource, token, username string) (migration.Discovery, error) {
	switch src.Adapter {
	case migration.AdapterProxmox:
		client := &migration.PVEClient{Base: src.Endpoint, Token: token, Insecure: src.Insecure, Client: s.HTTPClient}
		return client.DiscoverRemote()
	default:
		info, err := migration.AdapterByID(src.Adapter)
		if err != nil {
			return migration.Discovery{}, err
		}
		return migration.Discovery{
			Adapter: src.Adapter, Endpoint: src.Endpoint,
			Warnings: []migration.Finding{{
				Level:   migration.CompatWarning,
				Code:    "no-discovery",
				Message: info.Label + " does not remote-discover. Use disk, archive, OVF, backup, or bundle import.",
			}},
		}, nil
	}
}

func (s *Server) migrationCompatibility(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err := migration.RejectDestructiveRequest(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req migrationJobBody
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := s.buildMigrationPlan(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(plan.Items))
	for _, it := range plan.Items {
		items = append(items, map[string]any{
			"source_id": it.SourceID, "name": it.Name, "kind": it.Kind, "mode": it.Mode,
			"compatibility": it.Compatibility, "findings": it.Findings,
			"consistency": modeConsistency(it.Mode), "source_safety": migration.SourceProtected,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "plan_id": plan.ID})
}

func (s *Server) createMigrationPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.MigrationImport, rbac.MigrationExport)
	if err != nil {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err := migration.RejectDestructiveRequest(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req migrationJobBody
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := s.buildMigrationPlan(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	reviews := make([]map[string]any, 0, len(plan.Items))
	nodeName := ""
	if n, _ := s.Store.GetNode(r.Context(), p.User.ClusterID); n != nil {
		nodeName = n.Name
	}
	for _, it := range plan.Items {
		rev := migration.Review(it, nodeName, plan.Mapping)
		if it.OverrideMapping != nil {
			rev = migration.Review(it, nodeName, *it.OverrideMapping)
		}
		reviews = append(reviews, rev)
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "review": reviews, "source_changes": migration.SourceChangesNone()})
}

func (s *Server) listMigrationJobs(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListMigrationJobs(r.Context(), p.User.ClusterID, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, j := range items {
		out = append(out, migrationJobJSON(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getMigrationJob(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	j, err := s.Store.GetMigrationJob(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found")
		return
	}
	writeJSON(w, http.StatusOK, migrationJobJSON(*j))
}

func (s *Server) startMigrationJob(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.MigrationImport, rbac.MigrationExport)
	if err != nil {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err := migration.RejectDestructiveRequest(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req migrationJobBody
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Direction == "" {
		req.Direction = "import"
	}
	if req.Direction == "export" && !rbac.Authorize(p.Grants, rbac.MigrationExport) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if req.Direction != "export" && !rbac.Authorize(p.Grants, rbac.MigrationImport) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	plan, err := s.buildMigrationPlan(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	nodeID := ""
	if n, _ := s.Store.GetNode(r.Context(), p.User.ClusterID); n != nil {
		nodeID = n.ID
		plan.DestinationNode = n.Name
	}
	kind := "migration.import"
	if plan.Direction == "export" {
		kind = "migration.export"
	}
	op := s.startOp(r.Context(), p.User.ClusterID, nodeID, kind, "preflight", 5)
	planBody, _ := json.Marshal(plan)
	st := migration.JobStatus{ID: uuid.NewString(), State: "running", Stage: "preflight", SourceUntouched: true, Retryable: true}
	stBody, _ := json.Marshal(st)
	job := appdb.MigrationJob{
		ID: st.ID, ClusterID: p.User.ClusterID, SourceID: req.SourceID, OperationID: op.ID,
		Adapter: plan.Adapter, Direction: plan.Direction, State: "running", Stage: "preflight",
		PlanJSON: planBody, StatusJSON: stBody,
	}
	if err := s.Store.CreateMigrationJob(r.Context(), job); err != nil {
		writeErr(w, http.StatusConflict, "could not record migration job")
		return
	}
	ack := false
	for _, v := range req.LiveAck {
		if v {
			ack = true
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, kind+".start", "ok", job.ID)
	if ack {
		s.audit(r, p.User.ClusterID, p.User.ID, "migration.live.ack", "ok", job.ID)
	}
	go s.runMigrationJob(context.Background(), p.User.ClusterID, job.ID)
	writeJSON(w, http.StatusAccepted, migrationJobJSON(job))
}

func (s *Server) cancelMigrationJob(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.MigrationImport, rbac.MigrationExport)
	if err != nil {
		return
	}
	j, err := s.Store.GetMigrationJob(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found")
		return
	}
	j.CancelRequested = true
	j.State = "canceling"
	if err := s.Store.UpdateMigrationJob(r.Context(), *j); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.cancel", "ok", j.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": j.ID, "state": "canceling", "source_untouched": true,
		"note": "Cancellation does not change source infrastructure. Incomplete No-dal staging may be removed.",
	})
}

func (s *Server) retryMigrationJob(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.MigrationImport, rbac.MigrationExport)
	if err != nil {
		return
	}
	j, err := s.Store.GetMigrationJob(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found")
		return
	}
	if j.State != "failed" && j.State != "canceled" {
		writeErr(w, http.StatusUnprocessableEntity, "only failed or canceled jobs can be retried")
		return
	}
	j.CancelRequested = false
	j.State = "running"
	j.Stage = "preflight"
	if err := s.Store.UpdateMigrationJob(r.Context(), *j); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.retry", "ok", j.ID)
	go s.runMigrationJob(context.Background(), p.User.ClusterID, j.ID)
	writeJSON(w, http.StatusAccepted, migrationJobJSON(*j))
}

func (s *Server) migrationSourceJSON(src appdb.MigrationSource, hasCred bool) map[string]any {
	return map[string]any{
		"id": src.ID, "adapter": src.Adapter, "label": src.Label, "endpoint": src.Endpoint,
		"insecure": src.Insecure, "has_credentials": hasCred,
	}
}

func migrationJobJSON(j appdb.MigrationJob) map[string]any {
	out := map[string]any{
		"id": j.ID, "adapter": j.Adapter, "direction": j.Direction, "state": j.State, "stage": j.Stage,
		"source_id": j.SourceID, "operation_id": j.OperationID, "cancel_requested": j.CancelRequested,
		"source_untouched": true,
	}
	if len(j.StatusJSON) > 0 {
		var st any
		_ = json.Unmarshal(j.StatusJSON, &st)
		out["status"] = st
	}
	if len(j.PlanJSON) > 0 {
		var plan any
		_ = json.Unmarshal(j.PlanJSON, &plan)
		out["plan"] = plan
	}
	return out
}

func modeConsistency(mode string) string {
	for _, m := range migration.Modes() {
		if m.ID == mode {
			return m.Consistency
		}
	}
	return migration.ConsistencyDepends
}

func (s *Server) buildMigrationPlan(ctx context.Context, clusterID string, req migrationJobBody) (migration.Plan, error) {
	adapter := req.Adapter
	if adapter == "" && req.SourceID != "" {
		src, _, _, _, err := s.Store.GetMigrationSource(ctx, clusterID, req.SourceID)
		if err != nil || src == nil {
			return migration.Plan{}, errUnprocessable("migration source not found")
		}
		adapter = src.Adapter
	}
	if adapter == "" {
		adapter = migration.AdapterDisk
	}
	if req.Direction == "export" {
		return s.buildExportPlan(ctx, clusterID, adapter, req)
	}
	discovered, manifests, err := s.planInputs(ctx, clusterID, adapter, req)
	if err != nil {
		return migration.Plan{}, err
	}
	if req.Modes == nil {
		req.Modes = map[string]string{}
	}
	if req.Mode != "" {
		for _, d := range discovered {
			if req.Modes[d.SourceID] == "" {
				req.Modes[d.SourceID] = req.Mode
			}
		}
	}
	selected := req.Selected
	if len(selected) == 0 && len(discovered) == 1 {
		selected = []string{discovered[0].SourceID}
	}
	plan, err := migration.BuildPlan(uuid.NewString(), adapter, discovered, selected, req.Modes, manifests, req.Mapping, req.Overrides, migration.DefaultQEMUFormats(), req.StartAfter, req.LiveAck)
	if err != nil {
		return migration.Plan{}, err
	}
	for i := range plan.Items {
		if req.IdentityConflictAck[plan.Items[i].SourceID] {
			plan.Items[i].IdentityConflictAck = true
		}
		caps := s.capsFor(adapter, plan.Items[i], discovered)
		env, err := s.preflightEnv(ctx, clusterID, req, plan.Items[i], discovered)
		if err != nil {
			return migration.Plan{}, err
		}
		if err := migration.Preflight(plan.Items[i], caps, env); err != nil {
			return migration.Plan{}, err
		}
	}
	plan.SourceID = req.SourceID
	return plan, nil
}

func (s *Server) capsFor(adapter string, item migration.ItemPlan, discovered []migration.DiscoveredWorkload) migration.Caps {
	running := false
	for _, d := range discovered {
		if d.SourceID == item.SourceID {
			running = d.Running
		}
	}
	switch adapter {
	case migration.AdapterProxmox:
		downloadable := false
		backupFmt := ""
		if item.Manifest.VM != nil {
			for _, d := range item.Manifest.VM.Disks {
				if d.Downloadable {
					downloadable = true
				}
			}
		}
		if item.Manifest.Container != nil && item.Manifest.Container.Rootfs != nil {
			backupFmt = item.Manifest.Container.Rootfs.Format
			if migration.VolumeLooksLikeFile(item.Manifest.Container.Rootfs.Path, backupFmt) {
				downloadable = true
			}
		}
		return migration.PVECaps(running, item.Kind, backupFmt, downloadable)
	case migration.AdapterDisk, migration.AdapterOVF, migration.AdapterLibvirt:
		return migration.Caps{Offline: false, Disk: true}
	case migration.AdapterBackup, migration.AdapterNodal:
		return migration.Caps{Backup: true, Disk: true}
	default:
		return migration.Caps{Disk: true, Offline: true}
	}
}

func (s *Server) preflightEnv(ctx context.Context, clusterID string, req migrationJobBody, item migration.ItemPlan, discovered []migration.DiscoveredWorkload) (migration.PreflightEnv, error) {
	env := migration.PreflightEnv{
		SourceExists: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true,
		DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true,
	}
	for _, d := range discovered {
		if d.SourceID == item.SourceID {
			env.SourceRunning = d.Running
			env.EstimatedBytes = d.EstimatedBytes
		}
	}
	if req.Path != "" {
		if _, err := os.Stat(req.Path); err != nil {
			env.SourceExists = false
		}
	}
	poolID := req.PoolID
	if poolID == "" && req.Mapping.Storage != nil {
		for _, v := range req.Mapping.Storage {
			poolID = v
			break
		}
	}
	if poolID != "" {
		pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
		if err != nil || pool == nil {
			env.DestPoolExists = false
		}
	}
	netID := req.NetworkID
	if netID == "" && req.Mapping.Network != nil {
		for _, v := range req.Mapping.Network {
			netID = v
			break
		}
	}
	if netID != "" {
		netw, err := s.Store.GetNetwork(ctx, clusterID, netID)
		if err != nil || netw == nil {
			env.DestNetExists = false
		}
	}
	name := req.Name
	if name == "" {
		name = item.Name
	}
	if name != "" {
		if existing, _ := s.Store.GetWorkloadByName(ctx, clusterID, name); existing != nil {
			env.NameAvailable = false
		}
	}
	return env, nil
}

func (s *Server) planInputs(ctx context.Context, clusterID, adapter string, req migrationJobBody) ([]migration.DiscoveredWorkload, map[string]migration.Manifest, error) {
	manifests := map[string]migration.Manifest{}
	switch adapter {
	case migration.AdapterProxmox:
		src, token, _, _, err := s.Store.GetMigrationSource(ctx, clusterID, req.SourceID)
		if err != nil || src == nil {
			return nil, nil, errUnprocessable("migration source not found")
		}
		client := &migration.PVEClient{Base: src.Endpoint, Token: token, Insecure: src.Insecure, Client: s.HTTPClient}
		d, err := client.DiscoverRemote()
		if err != nil {
			return nil, nil, errUnprocessable(err.Error())
		}
		for _, w := range d.Workloads {
			node, vmid, _ := strings.Cut(w.SourceID, "/")
			m, err := client.ManifestFor(node, w.Kind, vmid)
			if err != nil {
				continue
			}
			manifests[w.SourceID] = m
		}
		return d.Workloads, manifests, nil
	case migration.AdapterOVF:
		m, err := s.manifestFromOVF(req.Path)
		if err != nil {
			return nil, nil, err
		}
		applyManualVM(&m, req)
		w := discoveredFromManifest(m, req.Path)
		manifests[w.SourceID] = m
		return []migration.DiscoveredWorkload{w}, manifests, nil
	case migration.AdapterLibvirt:
		body, err := os.ReadFile(req.XMLPath)
		if err != nil {
			return nil, nil, errUnprocessable("libvirt domain XML is not readable")
		}
		m, err := migration.ParseLibvirtXML(strings.NewReader(string(body)))
		if err != nil {
			return nil, nil, errUnprocessable(err.Error())
		}
		applyManualVM(&m, req)
		w := discoveredFromManifest(m, req.XMLPath)
		manifests[w.SourceID] = m
		return []migration.DiscoveredWorkload{w}, manifests, nil
	case migration.AdapterNodal:
		m, err := migration.ReadBundle(req.Path)
		if err != nil {
			return nil, nil, errUnprocessable(err.Error())
		}
		w := discoveredFromManifest(m, req.Path)
		manifests[w.SourceID] = m
		return []migration.DiscoveredWorkload{w}, manifests, nil
	case migration.AdapterBackup:
		if migration.IsVMA(req.Path) {
			return nil, nil, errUnprocessable("Proxmox VM vzdump vma archives cannot be read without a vma extractor")
		}
		m, err := s.manifestFromBackup(req)
		if err != nil {
			return nil, nil, err
		}
		w := discoveredFromManifest(m, req.Path)
		manifests[w.SourceID] = m
		return []migration.DiscoveredWorkload{w}, manifests, nil
	default:
		m := manifestFromDisk(req)
		w := discoveredFromManifest(m, req.Path)
		manifests[w.SourceID] = m
		return []migration.DiscoveredWorkload{w}, manifests, nil
	}
}

func applyManualVM(m *migration.Manifest, req migrationJobBody) {
	if m.VM == nil {
		return
	}
	if req.CPUs > 0 {
		m.VM.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		m.VM.MemoryBytes = req.MemoryBytes
	}
	if req.Firmware != "" {
		m.VM.Firmware = req.Firmware
	}
	if req.Name != "" {
		m.Identity.Name = req.Name
	}
}

func manifestFromDisk(req migrationJobBody) migration.Manifest {
	kind := req.Kind
	if kind == "" {
		kind = migration.KindVM
	}
	name := req.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
	}
	m := migration.Manifest{
		SchemaVersion: migration.ManifestSchema, Kind: kind,
		Identity: migration.Identity{Name: name, SourceID: req.Path},
		Source:   migration.SourceMeta{Adapter: migration.AdapterDisk, Type: kind},
	}
	if kind == migration.KindContainer {
		m.Container = &migration.ContainerSection{
			CPUs: req.CPUs, MemoryBytes: req.MemoryBytes,
			Rootfs: &migration.Artifact{Path: req.Path, Format: req.Format},
		}
		if m.Container.CPUs == 0 {
			m.Container.CPUs = 1
		}
		return m
	}
	m.VM = &migration.VMSection{
		CPUs: req.CPUs, MemoryBytes: req.MemoryBytes, Firmware: req.Firmware,
		Disks: []migration.Disk{{ID: "boot", Role: "boot", Source: req.Path, Format: migration.NormalizeFormat(req.Format, req.Path), Artifact: req.Path}},
	}
	if req.NetworkID != "" {
		m.VM.NICs = []migration.NIC{{Network: req.NetworkID, Model: "virtio"}}
	}
	return m
}

func (s *Server) manifestFromOVF(p string) (migration.Manifest, error) {
	if strings.HasSuffix(strings.ToLower(p), ".ova") {
		dir := filepath.Join(os.TempDir(), "ndl-ova-"+uuid.NewString())
		if err := migration.ExtractOVA(p, dir); err != nil {
			return migration.Manifest{}, errUnprocessable(err.Error())
		}
		ovf, err := migration.FindOVF(dir)
		if err != nil {
			return migration.Manifest{}, errUnprocessable(err.Error())
		}
		p = ovf
	}
	f, err := os.Open(p)
	if err != nil {
		return migration.Manifest{}, errUnprocessable("OVF is not readable")
	}
	defer f.Close()
	m, _, err := migration.ParseOVF(f)
	if err != nil {
		return migration.Manifest{}, errUnprocessable(err.Error())
	}
	return m, nil
}

func (s *Server) manifestFromBackup(req migrationJobBody) (migration.Manifest, error) {
	if req.Path == "" {
		return migration.Manifest{}, errUnprocessable("backup path is required")
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		return migration.Manifest{}, errUnprocessable("backup artifact is not readable")
	}
	kind := req.Kind
	if kind == "" {
		kind = migration.KindVM
	}
	m := manifestFromDisk(req)
	m.Source.Adapter = migration.AdapterBackup
	m.Source.Notes = "Backup timestamp: " + info.ModTime().UTC().Format(time.RFC3339)
	m.Kind = kind
	return m, nil
}

func discoveredFromManifest(m migration.Manifest, path string) migration.DiscoveredWorkload {
	id := m.Identity.SourceID
	if id == "" {
		id = path
	}
	w := migration.DiscoveredWorkload{
		SourceID: id, Name: m.Identity.Name, Kind: m.Kind, TypeLabel: m.Kind, Running: m.Source.Running,
	}
	if m.VM != nil {
		w.CPUs = m.VM.CPUs
		w.MemoryBytes = m.VM.MemoryBytes
		w.Firmware = m.VM.Firmware
		for _, d := range m.VM.Disks {
			w.DiskBytes += d.SizeBytes
			w.EstimatedBytes += d.SizeBytes
		}
	}
	if m.Container != nil {
		w.CPUs = m.Container.CPUs
		w.MemoryBytes = m.Container.MemoryBytes
		if m.Container.Rootfs != nil {
			w.EstimatedBytes = m.Container.Rootfs.Size
		}
	}
	return w
}

func (s *Server) buildExportPlan(ctx context.Context, clusterID, adapter string, req migrationJobBody) (migration.Plan, error) {
	if req.WorkloadID == "" {
		return migration.Plan{}, errUnprocessable("workload_id is required for export")
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, req.WorkloadID)
	if err != nil || wl == nil {
		return migration.Plan{}, errNotFound("workload not found")
	}
	kind := wl.Kind
	m := migration.Manifest{
		SchemaVersion: migration.ManifestSchema, Kind: kind,
		Identity: migration.Identity{Name: wl.Name, SourceID: wl.ID},
		Source:   migration.SourceMeta{Adapter: migration.AdapterNodal, Type: kind},
		Tags:     nil,
	}
	if kind == vmspec.KindVM {
		spec, _ := vmspec.Parse(wl.SpecJSON)
		m.VM = &migration.VMSection{CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes, Firmware: wl.Firmware}
		if spec.Firmware != "" {
			m.VM.Firmware = spec.Firmware
		}
		disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, wl.ID)
		for i, d := range disks {
			m.VM.Disks = append(m.VM.Disks, migration.Disk{ID: d.ID, Role: d.Role, Format: d.Format, BootIndex: i})
		}
		nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
		for _, n := range nics {
			m.VM.NICs = append(m.VM.NICs, migration.NIC{MAC: n.MAC, Network: n.NetworkID, Model: n.Model})
		}
	} else {
		m.Kind = migration.KindContainer
		m.Container = &migration.ContainerSection{
			CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes, Privileged: wl.Privileged,
			UIDMap: wl.UIDMap, GIDMap: wl.GIDMap, Rootfs: &migration.Artifact{Path: "rootfs", Format: "dir"},
		}
	}
	exportKind := req.ExportKind
	if exportKind == "" {
		exportKind = migration.ExportBundle
	}
	item := migration.ItemPlan{
		SourceID: wl.ID, Name: wl.Name, Kind: m.Kind, Mode: migration.ModeDisk, Manifest: m,
		Compatibility: migration.CompatReady,
	}
	return migration.Plan{ID: uuid.NewString(), Direction: "export", Adapter: adapter, Items: []migration.ItemPlan{item}, DestinationNode: exportKind}, nil
}
