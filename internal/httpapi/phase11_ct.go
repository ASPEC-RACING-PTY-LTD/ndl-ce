package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuppack"
	"github.com/no-dal/ndl-ce/internal/ctbackup"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/objstore"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const (
	extraDiskSkipReason  = "additional attached disks are not included in this backup"
	ctQcow2RestoreReason = "system container restore requires a filesystem archive, not a qcow2 disk copy"
)

type ctBackupMeta struct {
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	Hostname       string          `json:"hostname,omitempty"`
	ImagePin       string          `json:"image_pin"`
	CPUs           int             `json:"cpus"`
	MemoryBytes    int64           `json:"memory_bytes"`
	Privileged     bool            `json:"privileged"`
	UIDMap         string          `json:"uid_map"`
	GIDMap         string          `json:"gid_map"`
	DesiredPower   string          `json:"desired_power"`
	Autostart      bool            `json:"autostart"`
	Nesting        *bool           `json:"nesting,omitempty"`
	TUN            bool            `json:"tun,omitempty"`
	AllowMknod     bool            `json:"allow_mknod,omitempty"`
	SpecJSON       json.RawMessage `json:"spec_json,omitempty"`
	AppliedJSON    json.RawMessage `json:"applied_json,omitempty"`
	StorageBackend string          `json:"storage_backend,omitempty"`
	RootSize       int64           `json:"root_size_bytes,omitempty"`
	WorkloadID     string          `json:"workload_id,omitempty"`
	NICs           []ctBackupNIC   `json:"nics,omitempty"`
}

type ctBackupNIC struct {
	NetworkID   string `json:"network_id"`
	MAC         string `json:"mac,omitempty"`
	IPv4Mode    string `json:"ipv4_mode,omitempty"`
	IPv4Address string `json:"ipv4_address,omitempty"`
	IPv4Gateway string `json:"ipv4_gateway,omitempty"`
	IPv6Mode    string `json:"ipv6_mode,omitempty"`
	IPv6Address string `json:"ipv6_address,omitempty"`
	IPv6Gateway string `json:"ipv6_gateway,omitempty"`
	DNS         string `json:"dns,omitempty"`
}

func encodeBackupPlan(p appdb.BackupPlan) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func parseBackupPlan(raw string) *appdb.BackupPlan {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var p appdb.BackupPlan
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil
	}
	return &p
}

func extraDiskSkipItems(spec vmspec.Spec, bootVolID string, disks []appdb.WorkloadDisk) []appdb.BackupPlanItem {
	seen := map[string]struct{}{}
	var out []appdb.BackupPlanItem
	add := func(id, role string) {
		id = strings.TrimSpace(id)
		if id == "" || id == bootVolID {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, appdb.BackupPlanItem{
			Kind: "disk", ID: id, Role: role, VolumeID: id, Reason: extraDiskSkipReason,
		})
	}
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleData && strings.TrimSpace(d.VolumeID) != "" && d.VolumeID != bootVolID {
			add(d.VolumeID, d.Role)
		}
	}
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleData && strings.TrimSpace(d.VolumeID) != "" && d.VolumeID != bootVolID {
			add(d.VolumeID, d.Role)
		}
	}
	return out
}

func (s *Server) planBackup(ctx context.Context, clusterID string, wl appdb.Workload) (appdb.BackupPlan, error) {
	plan := appdb.BackupPlan{}
	if wl.Kind != vmspec.KindVM && wl.Kind != lxc.KindSystemContainer {
		return plan, errUnprocessable("workload kind cannot be backed up")
	}
	vol, pool, _, locErr := s.bootVolumeLocator(ctx, clusterID, wl)
	spec, _ := vmspec.Parse(wl.SpecJSON)
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, wl.ID)
	bootID := migrateBootVolumeID(spec, disks)
	if locErr == nil && vol != nil {
		bootID = vol.ID
	}
	plan.Skipped = extraDiskSkipItems(spec, bootID, disks)
	if locErr != nil {
		return plan, locErr
	}
	if pool != nil && pool.BackendType == storage.BackendISCSI {
		return plan, errUnprocessable(iscsiSnapReason)
	}
	if pool != nil && pool.BackendType == storage.BackendDistributed {
		return plan, errUnprocessable(distSnapReason)
	}
	role := vmspec.DiskRoleBoot
	if wl.Kind == lxc.KindSystemContainer {
		role = "root"
	}
	plan.Included = []appdb.BackupPlanItem{{
		Kind: "disk", ID: vol.ID, Role: role, VolumeID: vol.ID,
	}}
	switch {
	case wl.Kind == lxc.KindSystemContainer && pool.BackendType == storage.BackendZFS:
		plan.Method = appdb.BackupMethodZFSSend
		plan.Consistency = appdb.BackupConsistencyZFSSnapshot
	case wl.Kind == lxc.KindSystemContainer:
		plan.Method = appdb.BackupMethodDirectoryArchive
		if wl.Status == lxc.StatusRunning || wl.UnitActive {
			plan.Consistency = appdb.BackupConsistencyFreezer
			plan.Warning = "Directory storage has no snapshot. The container is frozen only while a consistent tree is copied; encoding and upload continue after resume."
		} else {
			plan.Consistency = appdb.BackupConsistencyStopped
		}
	case pool.BackendType == storage.BackendZFS:
		plan.Method = appdb.BackupMethodZFSSend
		plan.Consistency = appdb.BackupConsistencyZFSSnapshot
	case pool.BackendType == storage.BackendLVM:
		plan.Method = appdb.BackupMethodQCOW2Copy
		plan.Consistency = appdb.BackupConsistencyLVMSnapshot
	default:
		plan.Method = appdb.BackupMethodQCOW2Copy
		plan.Consistency = appdb.BackupConsistencyOverlay
	}
	return plan, nil
}

func (s *Server) ctBackupMeta(ctx context.Context, clusterID string, wl appdb.Workload, rootSize int64, backend string) []byte {
	meta := ctBackupMeta{
		Kind: lxc.KindSystemContainer, Name: wl.Name, Hostname: wl.Name, ImagePin: wl.ImagePin,
		CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes, Privileged: wl.Privileged,
		UIDMap: wl.UIDMap, GIDMap: wl.GIDMap, DesiredPower: wl.DesiredPower,
		Autostart: wl.Autostart, SpecJSON: wl.SpecJSON, AppliedJSON: wl.AppliedJSON,
		StorageBackend: backend, RootSize: rootSize, WorkloadID: wl.ID,
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
	for _, n := range nics {
		meta.NICs = append(meta.NICs, ctBackupNIC{
			NetworkID: n.NetworkID, MAC: n.MAC, IPv4Mode: n.IPv4Mode, IPv4Address: n.IPv4Address,
			IPv4Gateway: n.IPv4Gateway, IPv6Mode: n.IPv6Mode, IPv6Address: n.IPv6Address,
			IPv6Gateway: n.IPv6Gateway, DNS: n.DNS,
		})
	}
	b, _ := json.Marshal(meta)
	return b
}

func (s *Server) executeDirectoryCTBackup(ctx context.Context, clusterID string, wl appdb.Workload, vol *appdb.Volume, rootfs string, objectKind bool, tgt appdb.BackupTarget, run *appdb.BackupRun, artifactID string) error {
	staging := filepath.Join(ctbackup.StagingRoot, run.ID)
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupMkdir, "", staging); err != nil {
		return err
	}
	defer func() { _, _ = s.Backup.CopyBackup(ctx, qemu.BackupRmTree, "", staging) }()

	need := vol.SizeBytes + 32<<20
	if need < 64<<20 {
		need = 64 << 20
	}
	st, err := s.Backup.CopyBackup(ctx, qemu.BackupStatFS, "", staging)
	if err == nil && st.Size > 0 && st.Size < need {
		return fmt.Errorf("backup staging needs %d bytes free; %d available", need, st.Size)
	}

	tree := filepath.Join(staging, "tree")
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupMkdir, "", tree); err != nil {
		return err
	}
	unit := ""
	if wl.Status == lxc.StatusRunning || wl.UnitActive {
		unit = lxc.UnitName(wl.ID)
	}
	if _, err := s.Backup.CopyBackup(ctx, qemu.SyncTreeAction(unit), rootfs, tree); err != nil {
		return err
	}

	backend := ""
	if vol != nil {
		backend = vol.BackendType
	}
	meta := s.ctBackupMeta(ctx, clusterID, wl, vol.SizeBytes, backend)
	tmp, err := os.CreateTemp("", "ndl-ct-meta-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(meta)
	closeErr := tmp.Close()
	defer func() { _ = os.Remove(tmpName) }()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	cfgPath := filepath.Join(staging, "config.json")
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupWrite, tmpName, cfgPath); err != nil {
		return err
	}

	name := s.backupWorkloadName(ctx, clusterID, wl)
	stamp := backuppack.BackupStamp(run.StartedAt, artifactID)
	art := appdb.BackupArtifact{
		ID: artifactID, ClusterID: clusterID, RunID: run.ID, WorkloadID: wl.ID, Format: backuppack.FormatNDLB,
	}
	if objectKind {
		prefix := backuppack.ObjectPrefix(tgt.Prefix, name, stamp)
		put, err := s.putPackArtifact(ctx, tgt, staging, prefix, cfgPath)
		if err != nil {
			return err
		}
		if put.Status == appdb.BackupUnavailable || strings.EqualFold(put.Status, "unavailable") {
			return errUnprocessable(firstNonEmpty(put.Reason, "object upload is unavailable"))
		}
		art.Locator = objstore.Locator(tgt.Bucket, put.Key)
		art.ObjectKey = put.Key
		art.Encrypted = true
		art.ChecksumSHA256 = put.PlaintextSHA256
		art.SizeBytes = put.PlaintextSize
		art.TransferredBytes = put.TransferredBytes
		run.TransferredBytes = put.TransferredBytes
		_ = s.Store.UpdateBackupTargetStatus(ctx, clusterID, tgt.ID, appdb.BackupAvailable)
	} else {
		dest := filepath.Join(tgt.Locator, filepath.FromSlash(backuppack.ObjectPrefix("", name, stamp)))
		res, err := s.Backup.CopyBackup(ctx, qemu.BackupPack, staging, dest)
		if err != nil {
			return err
		}
		art.Locator = firstNonEmpty(res.Dest, dest)
		art.ChecksumSHA256 = res.SHA256
		art.SizeBytes = res.Size
		art.Format = firstNonEmpty(res.Format, backuppack.FormatNDLB)
	}
	return s.Store.CreateBackupArtifact(ctx, art)
}

func (s *Server) backupWorkloadName(ctx context.Context, clusterID string, wl appdb.Workload) string {
	items, _ := s.Store.ListWorkloads(ctx, clusterID)
	others := make([]string, 0, len(items))
	for _, o := range items {
		if o.ID != wl.ID {
			others = append(others, o.Name)
		}
	}
	return backuppack.UniqueName(wl.Name, wl.ID, others)
}

func (s *Server) restoreSystemContainer(ctx context.Context, clusterID string, src *appdb.Workload, art appdb.BackupArtifact, mode string, dest *appdb.Node) (string, error) {
	if !ctbackup.IsArchiveFormat(art.Format) {
		return "", errUnprocessable(ctQcow2RestoreReason)
	}
	if mode == "replace" {
		if src == nil {
			return "", errUnprocessable("replace requires the original workload to still exist")
		}
		return src.ID, s.restoreReplaceCT(ctx, clusterID, *src, art)
	}
	return s.restoreNewCT(ctx, clusterID, src, art, dest)
}

func (s *Server) restoreReplaceCT(ctx context.Context, clusterID string, src appdb.Workload, art appdb.BackupArtifact) error {
	if s.Workloads == nil || s.Backup == nil {
		return errUnavailable("backup agent is unavailable")
	}
	vol, _, tip, err := s.bootVolumeLocator(ctx, clusterID, src)
	if err != nil {
		return err
	}
	spec := mustSpec(src)
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, src.ID)
	bootID := ""
	if vol != nil {
		bootID = vol.ID
	}
	if len(extraDiskSkipItems(spec, bootID, disks)) > 0 {
		return errUnprocessable("restore replace would mix a restored root with extra disks that were not in the backup")
	}
	if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: src.ID, Action: "stop"}); err != nil {
		return err
	}
	srcPath, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupExtractRoot, srcPath, tip); err != nil {
		return err
	}
	if src.DesiredPower == "running" {
		if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: src.ID, Action: "start"}); err != nil {
			return err
		}
	}
	return nil
}

func mustSpec(wl appdb.Workload) vmspec.Spec {
	spec, _ := vmspec.Parse(wl.SpecJSON)
	return spec
}

func (s *Server) restoreNewCT(ctx context.Context, clusterID string, src *appdb.Workload, art appdb.BackupArtifact, dest *appdb.Node) (string, error) {
	if dest == nil {
		node, err := s.Store.GetNode(ctx, clusterID)
		if err != nil || node == nil {
			return "", errUnprocessable("local node is not enrolled")
		}
		dest = node
	}
	local := s.applyLocal(ctx, clusterID, dest.ID)
	if local && (s.Workloads == nil || s.Backup == nil || s.Storage == nil) {
		return "", errUnavailable("backup agent is unavailable")
	}
	srcPath, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
	if err != nil {
		return "", err
	}
	defer cleanup()
	meta := ctBackupMeta{}
	if raw, err := os.ReadFile(srcPath + ".ndl-meta.json"); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &meta)
	}
	if meta.Kind == "" {
		if raw, err := ctbackup.ReadMeta(ctx, srcPath); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &meta)
		}
	}
	if meta.Nesting == nil && len(meta.AppliedJSON) > 0 {
		var applied lxc.Applied
		if json.Unmarshal(meta.AppliedJSON, &applied) == nil {
			if meta.Nesting == nil {
				meta.Nesting = applied.Spec.Nesting
			}
			if !meta.TUN {
				meta.TUN = applied.Spec.TUN
			}
			if !meta.AllowMknod {
				meta.AllowMknod = applied.Spec.AllowMknod
			}
		}
	}
	if src != nil {
		if meta.Kind == "" {
			meta.Kind = lxc.KindSystemContainer
		}
		if meta.Name == "" {
			meta.Name = src.Name
		}
		if meta.ImagePin == "" {
			meta.ImagePin = src.ImagePin
		}
		if meta.CPUs == 0 {
			meta.CPUs = src.CPUs
		}
		if meta.MemoryBytes == 0 {
			meta.MemoryBytes = src.MemoryBytes
		}
		if meta.UIDMap == "" {
			meta.UIDMap = src.UIDMap
		}
		if meta.GIDMap == "" {
			meta.GIDMap = src.GIDMap
		}
		if meta.DesiredPower == "" {
			meta.DesiredPower = src.DesiredPower
		}
		if !meta.Autostart {
			meta.Autostart = src.Autostart
		}
		meta.Privileged = src.Privileged
		if len(meta.NICs) == 0 {
			nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
			for _, n := range nics {
				meta.NICs = append(meta.NICs, ctBackupNIC{
					NetworkID: n.NetworkID, IPv4Mode: n.IPv4Mode, IPv4Address: n.IPv4Address,
					IPv4Gateway: n.IPv4Gateway, IPv6Mode: n.IPv6Mode, IPv6Address: n.IPv6Address,
					IPv6Gateway: n.IPv6Gateway, DNS: n.DNS,
				})
			}
		}
	}
	if meta.CPUs < 1 {
		meta.CPUs = lxc.DefaultCPUs
	}
	if meta.MemoryBytes < 1 {
		meta.MemoryBytes = lxc.DefaultMemoryBytes
	}
	if meta.UIDMap == "" {
		meta.UIDMap = lxc.DefaultUIDMap
	}
	if meta.GIDMap == "" {
		meta.GIDMap = lxc.DefaultGIDMap
	}
	if meta.ImagePin == "" {
		meta.ImagePin = "imported"
	}
	if meta.DesiredPower == "" {
		// Stay stopped unless the archive recorded a running desired power.
		// Missing metadata must not start a restored guest onto the LAN.
		meta.DesiredPower = "stopped"
	}
	size := meta.RootSize
	if src != nil {
		if vol, _, _, err := s.bootVolumeLocator(ctx, clusterID, *src); err == nil && vol != nil && vol.SizeBytes > 0 {
			size = vol.SizeBytes
		}
	}
	if size < lxc.MinRootSize {
		size = lxc.DefaultRootSize
	}
	var pool *appdb.StoragePool
	if src != nil {
		if vol, p, _, err := s.bootVolumeLocator(ctx, clusterID, *src); err == nil {
			_ = vol
			pool = p
		}
	}
	if pool == nil {
		pools, err := s.Store.ListStoragePools(ctx, clusterID)
		if err != nil || len(pools) == 0 {
			return "", errUnprocessable("no storage pool is available for restore")
		}
		cp := pools[0]
		pool = &cp
	}
	if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
		return "", errConflict("storage pool is unavailable")
	}
	netID := ""
	bridge := ""
	if len(meta.NICs) > 0 {
		netID = meta.NICs[0].NetworkID
	}
	if netID == "" && src != nil {
		nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
		if len(nics) > 0 {
			netID = nics[0].NetworkID
		}
	}
	if netID == "" {
		nets, err := s.Store.ListNetworks(ctx, clusterID)
		if err != nil || len(nets) == 0 {
			return "", errUnprocessable("no network is available for restore")
		}
		netID = nets[0].ID
	}
	netw, err := s.Store.GetNetwork(ctx, clusterID, netID)
	if err != nil || netw == nil {
		return "", errNotFound("network is not found")
	}
	bridge = netw.BridgeName
	newID := uuid.NewString()
	newVolID := uuid.NewString()
	backend := path.Join("volumes", storage.ClassContainerRoot, newVolID)
	rootfs := ""
	if local {
		hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
			Class: storage.ClassContainerRoot, Size: size, Format: storage.FormatDirectory,
		}, hint)
		if err != nil && !strings.Contains(err.Error(), "duplicate") {
			return "", err
		}
		if res.Handle.BackendRef != "" {
			backend = res.Handle.BackendRef
		}
		loc, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, backend)
		if err != nil {
			return "", errConflict("volume locator is invalid")
		}
		rootfs = loc
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupExtractRoot, srcPath, rootfs); err != nil {
			return "", err
		}
	} else {
		var locErr error
		rootfs, locErr = storage.HostVolumePath(pool.BackendType, pool.RootPath, backend)
		if locErr != nil {
			rootfs = backend
		}
	}
	newVol := appdb.Volume{
		ID: newVolID, ClusterID: clusterID, NodeID: dest.ID, PoolID: pool.ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		SizeBytes: size, Status: storage.StatusAvailable, BackendType: firstNonEmpty(pool.BackendType, storage.BackendDirectory),
		BackendRef: backend,
	}
	if !local {
		newVol.Status = storage.StatusUnavailable
	}
	if err := s.Store.CreateVolume(ctx, newVol); err != nil {
		return "", err
	}
	name := uniqueRestoredName(firstNonEmpty(meta.Name, "restored"), newID)
	ip := lxc.IPConfig{IPv4Mode: lxc.IPModeDHCP, IPv6Mode: lxc.IPModeDisabled}
	if len(meta.NICs) > 0 {
		ip = ipFromNIC(appdb.WorkloadNIC{
			IPv4Mode: meta.NICs[0].IPv4Mode, IPv4Address: meta.NICs[0].IPv4Address, IPv4Gateway: meta.NICs[0].IPv4Gateway,
			IPv6Mode: meta.NICs[0].IPv6Mode, IPv6Address: meta.NICs[0].IPv6Address, IPv6Gateway: meta.NICs[0].IPv6Gateway,
			DNS: meta.NICs[0].DNS,
		})
	}
	if local {
		if _, err := s.Workloads.CreateCT(ctx, lxc.Spec{
			WorkloadID: newID, Name: name, ImagePin: meta.ImagePin,
			CPUs: meta.CPUs, MemoryBytes: meta.MemoryBytes, VolumeID: newVolID,
			RootfsPath: rootfs, NetworkID: netID, BridgeName: bridge,
			Privileged: meta.Privileged, UIDMap: meta.UIDMap, GIDMap: meta.GIDMap,
			IP: ip, SkipImage: true, NoStart: meta.DesiredPower != "running",
			Nesting: meta.Nesting, TUN: meta.TUN, AllowMknod: meta.AllowMknod,
		}); err != nil {
			return "", err
		}
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: dest.ID, OwnerNodeID: dest.ID, DesiredNodeID: dest.ID,
		Name: name, Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped, DesiredPower: meta.DesiredPower,
		ImagePin: meta.ImagePin, CPUs: meta.CPUs, MemoryBytes: meta.MemoryBytes, Privileged: meta.Privileged,
		UIDMap: meta.UIDMap, GIDMap: meta.GIDMap, Autostart: meta.Autostart,
		Devices: json.RawMessage(`[]`), MigrateBlockers: json.RawMessage(`["live migrate of system containers is post-1.0"]`),
	}
	if meta.DesiredPower == "running" && local {
		row.Status = lxc.StatusRunning
	}
	if !local {
		row.Status = "unavailable"
		row.Reason = "cross-node restore recorded; dest agent is not connected"
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		return "", err
	}
	if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: newVolID, Role: "root",
	}); err != nil {
		return "", errInternal("could not record container disk")
	}
	if err := s.Store.CreateWorkloadNIC(ctx, nicFromIP(appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, NetworkID: netID,
	}, ip)); err != nil {
		return "", errInternal("could not record container NIC")
	}
	return newID, nil
}
