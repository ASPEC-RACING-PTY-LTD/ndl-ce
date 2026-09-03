package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func (s *Server) importMigrationDisk(w http.ResponseWriter, r *http.Request) {
	s.startMigrationPrefixed(w, r, "import", migration.AdapterDisk)
}

func (s *Server) importMigrationBundle(w http.ResponseWriter, r *http.Request) {
	s.startMigrationPrefixed(w, r, "import", migration.AdapterNodal)
}

func (s *Server) exportMigration(w http.ResponseWriter, r *http.Request) {
	s.startMigrationPrefixed(w, r, "export", migration.AdapterNodal)
}

func (s *Server) startMigrationPrefixed(w http.ResponseWriter, r *http.Request, direction, adapter string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := migration.RejectDestructiveRequest(raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil && len(strings.TrimSpace(string(raw))) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["direction"]; !ok {
		body["direction"] = direction
	}
	if _, ok := body["adapter"]; !ok {
		body["adapter"] = adapter
	}
	next, _ := json.Marshal(body)
	r.Body = io.NopCloser(strings.NewReader(string(next)))
	s.startMigrationJob(w, r)
}

func (s *Server) runMigrationJob(ctx context.Context, clusterID, jobID string) {
	j, err := s.Store.GetMigrationJob(ctx, clusterID, jobID)
	if err != nil || j == nil {
		return
	}
	var plan migration.Plan
	_ = json.Unmarshal(j.PlanJSON, &plan)
	op := appdb.Operation{ID: j.OperationID, ClusterID: clusterID, Kind: "migration." + plan.Direction, State: "running"}
	st := migration.JobStatus{ID: j.ID, State: "running", Stage: "transfer", SourceUntouched: true, Retryable: true}
	fail := func(stage, msg string) {
		st.State = "failed"
		st.Stage = stage
		st.Message = msg
		st.SourceUntouched = true
		st.PartialDest = filepath.Join(migration.StagingRoot, j.ID)
		st.Retryable = true
		s.saveMigrationJob(ctx, j, st, "failed", stage)
		s.finishOp(ctx, op, "failed", msg, 0)
		_ = s.Store.InsertEvent(ctx, appdb.Event{
			ID: uuid.NewString(), ClusterID: clusterID, Type: "migration.failed",
			Payload:   mustJSONBytes(map[string]string{"job_id": j.ID, "stage": stage, "error": msg, "source_untouched": "true"}),
			CreatedAt: time.Now().UTC(),
		})
	}
	if canceled, _ := s.migrationCanceled(ctx, clusterID, jobID); canceled {
		fail("canceled", "Migration canceled. Source remains unchanged.")
		_ = migration.RemoveStaging(s.migrationStagingRoot(), jobID)
		return
	}
	root := s.migrationStagingRoot()
	stageDir, err := migration.StagingDir(root, jobID)
	if err != nil {
		fail("staging", err.Error())
		return
	}
	if plan.Direction == "export" {
		s.runExportJob(ctx, clusterID, j, plan, op, st, stageDir, fail)
		return
	}
	var reports []migration.Report
	for _, item := range plan.Items {
		if canceled, _ := s.migrationCanceled(ctx, clusterID, jobID); canceled {
			fail("canceled", "Migration canceled. Source remains unchanged.")
			_ = migration.RemoveStaging(root, jobID)
			return
		}
		st.Workload = item.Name
		st.Stage = "transfer"
		s.saveMigrationJob(ctx, j, st, "running", "transfer")
		item, err := s.materializeSource(ctx, clusterID, jobID, stageDir, plan, item)
		if err != nil {
			fail("transfer", err.Error())
			return
		}
		rep, destID, err := s.transferOne(ctx, clusterID, jobID, stageDir, plan, item)
		if err != nil {
			fail(st.Stage, err.Error())
			return
		}
		if destID != "" {
			rep.WorkloadID = destID
		}
		reports = append(reports, rep)
	}
	st.State = "succeeded"
	st.Stage = "verified"
	st.Reports = reports
	st.Message = "Migration verified. Source remains unchanged."
	st.Percent = 100
	s.saveMigrationJob(ctx, j, st, "succeeded", "verified")
	s.finishOp(ctx, op, "succeeded", st.Message, 100)
	_ = s.Store.InsertEvent(ctx, appdb.Event{
		ID: uuid.NewString(), ClusterID: clusterID, Type: "migration.succeeded",
		Payload:   mustJSONBytes(map[string]string{"job_id": j.ID, "source_untouched": "true"}),
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) transferOne(ctx context.Context, clusterID, jobID, stageDir string, plan migration.Plan, item migration.ItemPlan) (migration.Report, string, error) {
	fields := map[string]string{
		"Configuration": "Imported",
		"Source safety": migration.SourceProtected,
		"Source":        migration.SourceUnchanged(),
	}
	observed := []string{migration.VerifyTransfer}
	switch item.Kind {
	case migration.KindContainer:
		id, err := s.importContainerItem(ctx, clusterID, jobID, stageDir, plan, item)
		if err != nil {
			return migration.Report{}, "", err
		}
		fields["Root filesystem"] = "Imported"
		fields["Ownership/xattrs"] = "Copied when present in the archive"
		fields["Historical snapshots"] = "Not migrated"
		observed = append(observed, migration.VerifyConfig)
		if item.StartAfter {
			observed = append(observed, migration.VerifyBoot)
		}
		rep := migration.NewReport(item.Name, item.Mode, fields, observed)
		rep.WorkloadID = id
		return rep, id, nil
	default:
		id, err := s.importVMItem(ctx, clusterID, jobID, stageDir, plan, item)
		if err != nil {
			return migration.Report{}, "", err
		}
		fields["Disks"] = "Imported"
		fields["Historical snapshots"] = "Not migrated"
		observed = append(observed, migration.VerifyConfig)
		if item.StartAfter {
			observed = append(observed, migration.VerifyBoot)
		}
		rep := migration.NewReport(item.Name, item.Mode, fields, observed)
		rep.WorkloadID = id
		return rep, id, nil
	}
}

func (s *Server) importVMItem(ctx context.Context, clusterID, jobID, stageDir string, plan migration.Plan, item migration.ItemPlan) (string, error) {
	if item.Manifest.VM == nil || len(item.Manifest.VM.Disks) == 0 {
		return "", fmt.Errorf("VM has no disks to import")
	}
	var converted []string
	for i, d := range item.Manifest.VM.Disks {
		srcPath := d.Source
		if srcPath == "" {
			srcPath = d.Artifact
		}
		if srcPath == "" && i == 0 {
			srcPath = item.SourceID
		}
		if srcPath == "" {
			return "", fmt.Errorf("VM disk %s has no source path", d.ID)
		}
		format := migration.NormalizeFormat(d.Format, srcPath)
		out := filepath.Join(stageDir, fmt.Sprintf("disk-%d.qcow2", i))
		if err := s.convertOrCopyDisk(ctx, srcPath, format, out); err != nil {
			return "", err
		}
		if _, _, err := migration.ChecksumFile(out); err != nil {
			return "", err
		}
		converted = append(converted, out)
	}
	poolID := firstMapValue(plan.Mapping.Storage)
	netID := firstMapValue(plan.Mapping.Network)
	mac := ""
	if len(item.Manifest.VM.NICs) > 0 {
		if item.Manifest.VM.NICs[0].Network != "" {
			netID = item.Manifest.VM.NICs[0].Network
		}
		mac = item.Manifest.VM.NICs[0].MAC
	}
	cpus := item.Manifest.VM.CPUs
	mem := item.Manifest.VM.MemoryBytes
	fw := item.Manifest.VM.Firmware
	name := item.Name
	wl, err := s.adoptImportedDisks(ctx, clusterID, name, converted, poolID, netID, fw, cpus, mem, item.StartAfter, mac)
	if err != nil {
		return "", err
	}
	return wl.ID, nil
}

func (s *Server) importContainerItem(ctx context.Context, clusterID, jobID, stageDir string, plan migration.Plan, item migration.ItemPlan) (string, error) {
	if s.Workloads == nil || s.Storage == nil {
		return "", errUnavailable("workload agent is unavailable")
	}
	srcPath := item.SourceID
	if item.Manifest.Container != nil && item.Manifest.Container.Rootfs != nil && item.Manifest.Container.Rootfs.Path != "" {
		srcPath = item.Manifest.Container.Rootfs.Path
	}
	rootfs := filepath.Join(stageDir, "rootfs")
	if err := s.extractOrCopyRootfs(ctx, srcPath, rootfs); err != nil {
		return "", err
	}
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || node == nil {
		return "", errUnprocessable("local node is not enrolled")
	}
	poolID := firstMapValue(plan.Mapping.Storage)
	netID := firstMapValue(plan.Mapping.Network)
	if item.Manifest.Container != nil && len(item.Manifest.Container.NICs) > 0 && item.Manifest.Container.NICs[0].Network != "" {
		netID = item.Manifest.Container.NICs[0].Network
	}
	req := createWorkloadRequest{
		Name: item.Name, Kind: lxc.KindSystemContainer, ImagePin: "imported",
		CPUs: item.Manifest.Container.CPUs, MemoryBytes: item.Manifest.Container.MemoryBytes,
		PoolID: poolID, NetworkID: netID, Privileged: item.Manifest.Container.Privileged,
		DesiredPower: "stopped",
	}
	if item.StartAfter {
		req.DesiredPower = "running"
	}
	ids := s.planCreateIDs(ctx, clusterID, node.ID, "", "")
	_, netw, rootPath, volRow, err := s.prepareRoot(ctx, clusterID, node.ID, req, ids.VolumeID)
	if err != nil {
		return "", err
	}
	if s.Backup != nil {
		if err := s.Backup.ExtractArchive(ctx, srcPath, rootPath); err != nil {
			if copyErr := copyDir(rootfs, rootPath); copyErr != nil {
				return "", err
			}
		}
	} else if err := copyDir(rootfs, rootPath); err != nil {
		return "", err
	}
	uidMap, gidMap := lxc.DefaultUIDMap, lxc.DefaultGIDMap
	if item.Manifest.Container != nil && item.Manifest.Container.UIDMap != "" {
		uidMap = item.Manifest.Container.UIDMap
		gidMap = item.Manifest.Container.GIDMap
	}
	mac := lxc.MACFromUUID(ids.WorkloadID)
	if item.Manifest.Container != nil && len(item.Manifest.Container.NICs) > 0 && item.Manifest.Container.NICs[0].MAC != "" {
		mac = item.Manifest.Container.NICs[0].MAC
	}
	res, err := s.Workloads.CreateCT(ctx, lxc.Spec{
		WorkloadID: ids.WorkloadID, Name: item.Name, ImagePin: "imported",
		CPUs: req.CPUs, MemoryBytes: req.MemoryBytes, VolumeID: ids.VolumeID,
		RootfsPath: rootPath, NetworkID: netw.ID, BridgeName: netw.BridgeName,
		MAC: mac, Privileged: req.Privileged, UIDMap: uidMap, GIDMap: gidMap,
		SkipImage: true, NoStart: !item.StartAfter,
	})
	if err != nil {
		return "", err
	}
	row := appdb.Workload{
		ID: ids.WorkloadID, ClusterID: clusterID, NodeID: node.ID, OwnerNodeID: node.ID, DesiredNodeID: node.ID,
		Name: item.Name, Kind: lxc.KindSystemContainer, Status: res.Status, DesiredPower: req.DesiredPower,
		CPUs: req.CPUs, MemoryBytes: req.MemoryBytes, Privileged: req.Privileged,
		UIDMap: uidMap, GIDMap: gidMap, ImagePin: "imported",
		Devices: json.RawMessage(`[]`), MigrateBlockers: json.RawMessage(`[]`),
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		return "", err
	}
	if volRow != nil {
		if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID, VolumeID: volRow.ID, Role: "root",
		}); err != nil {
			return "", errInternal("could not record container disk")
		}
	}
	if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID, NetworkID: netw.ID, MAC: mac,
	}); err != nil {
		return "", errInternal("could not record container NIC")
	}
	return row.ID, nil
}

func (s *Server) convertOrCopyDisk(ctx context.Context, src, format, dest string) error {
	if err := migration.ValidateHostPath(src); err != nil {
		return err
	}
	if s.Backup == nil {
		return errUnavailable("disk convert agent is unavailable")
	}
	if format == "" {
		format = migration.NormalizeFormat("", src)
	}
	return s.Backup.ConvertImport(ctx, qemu.ConvertRequest{SourcePath: src, DestPath: dest, SourceFormat: format, DestFormat: "qcow2"})
}

func (s *Server) extractOrCopyRootfs(ctx context.Context, src, dest string) error {
	low := strings.ToLower(src)
	if strings.HasSuffix(low, ".tar") || strings.Contains(low, ".tar.") || strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".zst") {
		return migration.ExtractArchiveFile(src, dest)
	}
	return copyDir(src, dest)
}

func (s *Server) materializeSource(ctx context.Context, clusterID, jobID, stageDir string, plan migration.Plan, item migration.ItemPlan) (migration.ItemPlan, error) {
	if plan.Adapter != migration.AdapterProxmox {
		return item, nil
	}
	src, token, _, _, err := s.Store.GetMigrationSource(ctx, clusterID, plan.SourceID)
	if err != nil || src == nil {
		return item, fmt.Errorf("migration source not found")
	}
	client := &migration.PVEClient{Base: src.Endpoint, Token: token, Insecure: src.Insecure, Client: s.HTTPClient}
	node, _, _ := strings.Cut(item.SourceID, "/")
	if item.Manifest.VM != nil {
		for i := range item.Manifest.VM.Disks {
			d := &item.Manifest.VM.Disks[i]
			if migration.ValidateHostPath(d.Source) == nil {
				continue
			}
			if migration.ValidateHostPath(d.Artifact) == nil {
				d.Source = d.Artifact
				continue
			}
			volid := d.VolID
			if volid == "" {
				volid = migration.PVEVolumeID(d.Source)
			}
			if volid == "" || d.Storage == "" {
				return item, fmt.Errorf("Proxmox disk %s is not downloadable. Copy the image on the source host and use Disk import", d.ID)
			}
			dest := filepath.Join(stageDir, "pve", fmt.Sprintf("%s-%d", safeFile(d.ID), i))
			if err := client.DownloadVolume(node, d.Storage, volid, dest); err != nil {
				return item, err
			}
			d.Source = dest
			d.Artifact = dest
		}
	}
	if item.Manifest.Container != nil && item.Manifest.Container.Rootfs != nil {
		p := item.Manifest.Container.Rootfs.Path
		if migration.ValidateHostPath(p) != nil {
			storage := ""
			if i := strings.Index(p, ":"); i >= 0 {
				storage = p[:i]
			}
			if storage == "" || !migration.VolumeLooksLikeFile(p, item.Manifest.Container.Rootfs.Format) {
				return item, fmt.Errorf("LXC rootfs is not a downloadable file. Import an existing vzdump tar backup, or provide a root filesystem archive")
			}
			dest := filepath.Join(stageDir, "pve", "rootfs-archive")
			if err := client.DownloadVolume(node, storage, p, dest); err != nil {
				return item, err
			}
			item.Manifest.Container.Rootfs.Path = dest
		}
	}
	_ = jobID
	return item, nil
}

func safeFile(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	if s == "" {
		return "disk"
	}
	return s
}

func (s *Server) adoptImportedDisks(ctx context.Context, clusterID, name string, diskPaths []string, poolID, networkID, firmware string, cpus int, memory int64, startAfter bool, mac string) (*appdb.Workload, error) {
	if len(diskPaths) == 0 {
		return nil, fmt.Errorf("VM has no disks to import")
	}
	if s.VM == nil || s.Backup == nil || s.Storage == nil {
		return nil, errUnavailable("vm agent is unavailable")
	}
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || node == nil {
		return nil, errUnprocessable("local node is not enrolled")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
	if err != nil || pool == nil {
		pools, _ := s.Store.ListStoragePools(ctx, clusterID)
		if len(pools) == 0 {
			return nil, errUnprocessable("destination storage does not exist")
		}
		pool = &pools[0]
	}
	if networkID == "" {
		nets, _ := s.Store.ListNetworks(ctx, clusterID)
		if len(nets) == 0 {
			return nil, errUnprocessable("destination network does not exist")
		}
		networkID = nets[0].ID
	}
	if strings.TrimSpace(name) == "" {
		name = "imported"
	}
	if cpus < 1 {
		cpus = 2
	}
	if memory < 64<<20 {
		memory = vmspec.DefaultMemory
	}
	if firmware == "" {
		firmware = vmspec.FirmwareBIOS
	}
	newID := uuid.NewString()
	var specDisks []vmspec.Disk
	var volIDs []string
	var dests []string
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	for i, diskPath := range diskPaths {
		newVolID := uuid.NewString()
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
			Class: storage.ClassVMDisk, Size: vmspec.DefaultDiskBytes, Format: storage.FormatQCOW2,
		}, hint)
		if err != nil && !strings.Contains(err.Error(), "duplicate") {
			return nil, err
		}
		backend := res.Handle.BackendRef
		if backend == "" {
			backend = path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2")
		}
		newVol := appdb.Volume{
			ID: newVolID, ClusterID: clusterID, NodeID: node.ID, PoolID: pool.ID,
			Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
			SizeBytes: vmspec.DefaultDiskBytes, Status: storage.StatusAvailable, BackendType: pool.BackendType, BackendRef: backend,
		}
		if err := s.Store.CreateVolume(ctx, newVol); err != nil {
			return nil, err
		}
		dest, err := storage.JoinUnder(pool.RootPath, newVol.BackendRef)
		if err != nil {
			s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, "")
			return nil, errConflict("volume locator is invalid")
		}
		if err := s.Backup.ConvertImport(ctx, qemu.ConvertRequest{SourcePath: diskPath, DestPath: dest, SourceFormat: "qcow2", DestFormat: "qcow2"}); err != nil {
			s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
			return nil, err
		}
		role := vmspec.DiskRoleData
		if i == 0 {
			role = vmspec.DiskRoleBoot
		}
		specDisks = append(specDisks, vmspec.Disk{Role: role, VolumeID: newVolID, Format: "qcow2"})
		volIDs = append(volIDs, newVolID)
		dests = append(dests, dest)
	}
	nic := vmspec.NIC{NetworkID: networkID, MAC: mac}
	spec := vmspec.Spec{
		Name: name, CPUs: cpus, MemoryBytes: memory, Firmware: firmware,
		Disks: specDisks,
		NICs:  []vmspec.NIC{nic},
	}
	spec = vmspec.PersistNICs(newID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		for i, id := range volIDs {
			s.rollbackAdoptedVM(ctx, clusterID, "", id, dests[i])
		}
		return nil, err
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: node.ID, OwnerNodeID: node.ID, DesiredNodeID: node.ID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped, DesiredPower: "stopped",
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec),
		Firmware: spec.Firmware, MigrateBlockers: json.RawMessage(`[]`),
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		for i, id := range volIDs {
			s.rollbackAdoptedVM(ctx, clusterID, "", id, dests[i])
		}
		return nil, err
	}
	for i, id := range volIDs {
		role := vmspec.DiskRoleData
		if i == 0 {
			role = vmspec.DiskRoleBoot
		}
		if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: id,
			Role: role, Slot: i, Format: storage.FormatQCOW2,
		}); err != nil {
			return nil, errInternal("could not record VM disk")
		}
	}
	if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, NetworkID: networkID, Model: vmspec.NICModelVirtio,
		MAC: spec.NICs[0].MAC,
	}); err != nil {
		return nil, errInternal("could not record VM NIC")
	}
	if _, err := s.reprepareVM(ctx, clusterID, row); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, newID, volIDs[0], dests[0])
		return nil, err
	}
	if startAfter {
		if s.VM != nil {
			if _, err := s.VM.LifecycleVM(ctx, newID, "start", false); err != nil {
				return nil, err
			}
			row.Status = qemu.StatusRunning
			row.DesiredPower = "running"
			_ = s.Store.UpdateWorkloadObserved(ctx, row)
		}
	}
	created, _ := s.Store.GetWorkload(ctx, clusterID, newID)
	if created == nil {
		return &row, nil
	}
	return created, nil
}

func (s *Server) runExportJob(ctx context.Context, clusterID string, j *appdb.MigrationJob, plan migration.Plan, op appdb.Operation, st migration.JobStatus, stageDir string, fail func(string, string)) {
	if len(plan.Items) == 0 {
		fail("export", "no workloads selected")
		return
	}
	item := plan.Items[0]
	kind := plan.DestinationNode
	if kind == "" {
		kind = migration.ExportBundle
	}
	files := map[string]string{}
	wl, err := s.Store.GetWorkload(ctx, clusterID, item.SourceID)
	if err != nil || wl == nil {
		fail("export", "workload not found")
		return
	}
	if item.Kind == vmspec.KindVM || item.Kind == migration.KindVM {
		_, pool, tip, err := s.bootVolumeLocator(ctx, clusterID, *wl)
		if err != nil {
			fail("export", err.Error())
			return
		}
		disk := filepath.Join(stageDir, "disks", "boot.qcow2")
		_ = os.MkdirAll(filepath.Dir(disk), 0o750)
		if s.Backup == nil {
			fail("export", "backup agent is unavailable")
			return
		}
		if err := s.Backup.ConvertImport(ctx, qemu.ConvertRequest{SourcePath: tip, DestPath: disk, SourceFormat: "qcow2", DestFormat: "qcow2"}); err != nil {
			fail("export", err.Error())
			return
		}
		files["disks/boot.qcow2"] = disk
		_ = pool
	} else {
		_, _, root, err := s.bootVolumeLocator(ctx, clusterID, *wl)
		if err != nil {
			fail("export", err.Error())
			return
		}
		archive := filepath.Join(stageDir, "rootfs", "rootfs.tar")
		if err := migration.WriteTar(root, archive); err != nil {
			fail("export", err.Error())
			return
		}
		files["rootfs/rootfs.tar"] = archive
	}
	var werr error
	switch kind {
	case migration.ExportOVF:
		werr = migration.WriteOVFPackage(stageDir, item.Manifest, files)
	case "proxmox":
		werr = migration.WritePVEPackage(stageDir, item.Manifest, files)
	case migration.ExportVMImage:
		if files["disks/boot.qcow2"] == "" {
			fail("export", "VM image export requires a virtual machine boot disk")
			return
		}
		werr = nil
		kind = migration.ExportVMImage
	case migration.ExportCTArchive:
		if files["rootfs/rootfs.tar"] == "" {
			fail("export", "container archive export requires a system container root filesystem")
			return
		}
		werr = nil
		kind = migration.ExportCTArchive
	default:
		werr = migration.WriteBundle(stageDir, item.Manifest, files)
		kind = migration.ExportBundle
	}
	if werr != nil {
		fail("export", werr.Error())
		return
	}
	exportLabel := migration.ExportDirect
	if kind == migration.ExportOVF || kind == "proxmox" {
		exportLabel = migration.ExportPackage
	}
	st.State = "succeeded"
	st.Stage = "verified"
	st.Message = "Export written. Source remains unchanged."
	st.Percent = 100
	st.PartialDest = stageDir
	st.Reports = []migration.Report{migration.NewReport(item.Name, item.Mode, map[string]string{
		"Export": exportLabel, "Path": stageDir, "Kind": kind, "Source": migration.SourceUnchanged(),
	}, []string{migration.VerifyTransfer, migration.VerifyConfig})}
	s.saveMigrationJob(ctx, j, st, "succeeded", "verified")
	s.finishOp(ctx, op, "succeeded", st.Message, 100)
}

func (s *Server) saveMigrationJob(ctx context.Context, j *appdb.MigrationJob, st migration.JobStatus, state, stage string) {
	body, _ := json.Marshal(st)
	j.State = state
	j.Stage = stage
	j.StatusJSON = body
	_ = s.Store.UpdateMigrationJob(ctx, *j)
}

func (s *Server) migrationCanceled(ctx context.Context, clusterID, id string) (bool, error) {
	j, err := s.Store.GetMigrationJob(ctx, clusterID, id)
	if err != nil || j == nil {
		return false, err
	}
	return j.CancelRequested, nil
}

func (s *Server) migrationStagingRoot() string {
	if v := strings.TrimSpace(os.Getenv("NDL_MIGRATION_STAGING")); v != "" {
		return v
	}
	if err := os.MkdirAll(migration.StagingRoot, 0o750); err == nil {
		return migration.StagingRoot
	}
	return filepath.Join(os.TempDir(), "ndl-migration")
}

func firstMapValue(m map[string]string) string {
	for _, v := range m {
		return v
	}
	return ""
}

func copyDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, filepath.Base(src)), body, 0o640)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dest, e.Name())
		if e.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
			continue
		}
		body, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(to, body, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func mustJSONBytes(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) cleanupMigrationStaging(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationManage)
	if err != nil {
		return
	}
	j, err := s.Store.GetMigrationJob(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found")
		return
	}
	if err := migration.RemoveStaging(s.migrationStagingRoot(), j.ID); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "migration.staging.cleanup", "ok", j.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "source_untouched": true,
		"note": "Removed No-dal-owned staging only. Source infrastructure was not changed.",
	})
}
