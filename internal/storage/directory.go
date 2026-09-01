package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Directory is the Phase 3 storage driver.
type Directory struct {
	Host Host
	Now  func() time.Time
	// AllowTestPrefix, when set, permits pool roots under that cleaned path.
	// Production callers must leave this empty so /tmp and other forbidden
	// prefixes stay rejected.
	AllowTestPrefix string
}

func (d Directory) forbidden(cleaned string) bool {
	if prefix, err := Normalize(d.AllowTestPrefix); err == nil && prefix != "" {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return false
		}
	}
	return Forbidden(cleaned)
}

func (d Directory) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

func (d Directory) host() Host {
	return d.Host.withDefaults()
}

// ValidateRoot checks a prospective Directory pool root.
func (d Directory) ValidateRoot(raw string, existing []string) (string, error) {
	cleaned, err := Normalize(raw)
	if err != nil {
		return "", err
	}
	if d.forbidden(cleaned) {
		return "", fmt.Errorf("%w: %s", ErrForbiddenPath, cleaned)
	}
	h := d.host()
	resolved, err := h.Eval(cleaned)
	if err == nil && resolved != "" {
		resolved = path.Clean(strings.ReplaceAll(resolved, `\`, "/"))
		if !strings.HasPrefix(resolved, "/") {
			resolved = cleaned
		}
		if resolved != cleaned && d.forbidden(resolved) {
			return "", fmt.Errorf("%w: %s", ErrSymlinkEscape, resolved)
		}
		cleaned = resolved
	}
	for _, other := range existing {
		o, err := Normalize(other)
		if err != nil {
			continue
		}
		if Overlaps(cleaned, o) {
			return "", fmt.Errorf("%w: %s", ErrOverlap, o)
		}
	}
	return cleaned, nil
}

// CreatePool adopts or creates a Directory pool root.
func (d Directory) CreatePool(ctx context.Context, req CreatePoolRequest, existing []string) (CreatePoolResult, error) {
	if _, err := uuid.Parse(req.PoolID); err != nil {
		return CreatePoolResult{}, fmt.Errorf("pool_id must be a UUID")
	}
	root, err := d.ValidateRoot(req.RootPath, existing)
	if err != nil {
		return CreatePoolResult{}, err
	}
	h := d.host()
	info, statErr := h.Stat(root)
	adopted := false
	if statErr == nil {
		if !info.IsDir() {
			return CreatePoolResult{}, ErrNotDirectory
		}
		adopted = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return CreatePoolResult{}, statErr
	} else if !req.Create {
		return CreatePoolResult{}, fmt.Errorf("storage path does not exist")
	} else if err := h.MkdirAll(root, 0o750); err != nil {
		return CreatePoolResult{}, err
	}
	if err := d.ensureLayout(root); err != nil {
		return CreatePoolResult{}, err
	}
	if err := d.writable(root); err != nil {
		return CreatePoolResult{}, err
	}
	obs, err := d.observeRoot(root, req.PoolID, BackingIdentity{}, false)
	if err != nil {
		return CreatePoolResult{}, err
	}
	if obs.Capacity.UsableBytes != nil && *obs.Capacity.UsableBytes < MinPoolFreeBytes {
		return CreatePoolResult{}, fmt.Errorf("%w: filesystem has less than 16 MiB free", ErrCapacity)
	}
	marker := PoolMarker{
		SchemaVersion: MarkerSchema,
		PoolID:        req.PoolID,
		BackendType:   BackendDirectory,
		CreatedAt:     d.now().Format(time.RFC3339),
		Adopted:       adopted,
		Backing:       obs.Backing,
	}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return CreatePoolResult{}, err
	}
	if err := h.WriteFile(path.Join(root, MarkerFile), append(body, '\n'), 0o640); err != nil {
		return CreatePoolResult{}, err
	}
	obs.PoolID = req.PoolID
	return CreatePoolResult{
		PoolID:       req.PoolID,
		RootPath:     root,
		Adopted:      adopted,
		Status:       obs.Status,
		Warnings:     obs.Warnings,
		WarningText:  obs.WarningText,
		Capacity:     obs.Capacity,
		Capabilities: obs.Capabilities,
		Backing:      obs.Backing,
	}, nil
}

func (d Directory) ensureLayout(root string) error {
	h := d.host()
	for _, rel := range poolDirs() {
		dest := path.Join(root, rel)
		if err := d.refuseEscape(root, dest); err != nil {
			return err
		}
		if err := h.MkdirAll(dest, 0o750); err != nil {
			return err
		}
		if err := d.refuseEscape(root, dest); err != nil {
			return err
		}
	}
	return nil
}

// refuseEscape rejects a dest whose resolved path leaves the pool root.
func (d Directory) refuseEscape(root, dest string) error {
	resolved, err := d.host().Eval(dest)
	if err != nil || resolved == "" {
		return nil
	}
	resolved = path.Clean(strings.ReplaceAll(resolved, `\`, "/"))
	if resolved == root || strings.HasPrefix(resolved, root+"/") {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrSymlinkEscape, resolved)
}

func (d Directory) writable(root string) error {
	h := d.host()
	probe := path.Join(root, ".ndl-write-probe")
	if err := h.WriteFile(probe, []byte("ok\n"), 0o600); err != nil {
		return ErrNotWritable
	}
	_ = h.Remove(probe)
	return nil
}

// Observe inspects known pools. Missing or remounted pools become unavailable.
// It never deletes objects.
func (d Directory) Observe(hints []PoolHint) Observation {
	out := Observation{ObservedAt: d.now()}
	for _, hint := range hints {
		if hint.BackendType != "" && hint.BackendType != BackendDirectory {
			out.Pools = append(out.Pools, ObservedPool{
				PoolID:      hint.PoolID,
				BackendType: hint.BackendType,
				RootPath:    hint.RootPath,
				Status:      StatusUnavailable,
				Reason:      "backend is not Directory",
				ObservedAt:  out.ObservedAt,
			})
			continue
		}
		pool, vols, libs := d.observeHint(hint)
		pool.ObservedAt = out.ObservedAt
		out.Pools = append(out.Pools, pool)
		out.Volumes = append(out.Volumes, vols...)
		out.Library = append(out.Library, libs...)
	}
	return out
}

func (d Directory) observeHint(hint PoolHint) (ObservedPool, []ObservedVolume, []ObservedLibrary) {
	root, err := Normalize(hint.RootPath)
	if err != nil {
		return unavailable(hint, err.Error()), nil, nil
	}
	h := d.host()
	if _, err := h.Stat(root); err != nil {
		return unavailable(hint, "pool path is missing"), nil, nil
	}
	obs, err := d.observeRoot(root, hint.PoolID, hint.Backing, true)
	if err != nil {
		return unavailable(hint, err.Error()), nil, nil
	}
	if obs.Status == StatusUnavailable {
		return obs, nil, nil
	}
	vols, libs := d.scanOwned(root, hint.PoolID)
	alloc, prov := sumObserved(vols, libs)
	obs.Capacity.AllocatedBytes = int64ptr(alloc)
	obs.Capacity.ProvisionedBytes = int64ptr(prov)
	return obs, vols, libs
}

func unavailable(hint PoolHint, reason string) ObservedPool {
	return ObservedPool{
		PoolID:      hint.PoolID,
		BackendType: BackendDirectory,
		RootPath:    hint.RootPath,
		Status:      StatusUnavailable,
		Reason:      reason,
		Backing:     hint.Backing,
	}
}

func (d Directory) observeRoot(root, poolID string, expected BackingIdentity, requireMarker bool) (ObservedPool, error) {
	h := d.host()
	text, err := h.ReadMounts()
	if err != nil {
		return ObservedPool{}, err
	}
	mounts := ParseMountinfo(text)
	st, err := h.StatFS(root)
	if err != nil {
		return ObservedPool{}, err
	}
	cover, ok := CoveringMount(root, mounts)
	if !ok {
		return ObservedPool{}, fmt.Errorf("no covering mount for %s", root)
	}
	uuid := cover.FSUUID
	if uuid == "" {
		uuid = h.LookupUUID(cover.Source)
	}
	var rootDev uint64
	var rootUUID string
	if rst, err := h.StatFS("/"); err == nil {
		rootDev = rst.Dev
	}
	if rootCover, ok := CoveringMount("/", mounts); ok {
		rootUUID = rootCover.FSUUID
		if rootUUID == "" {
			rootUUID = h.LookupUUID(rootCover.Source)
		}
	}
	backing := backingFromMount(cover, st.Dev, uuid, rootDev, rootUUID)
	if requireMarker {
		marker, err := d.readMarker(root)
		if err != nil {
			return ObservedPool{
				PoolID: poolID, BackendType: BackendDirectory, RootPath: root,
				Status: StatusUnavailable, Reason: "pool marker missing or unreadable",
				Backing: backing,
			}, nil
		}
		if poolID != "" && marker.PoolID != poolID {
			return ObservedPool{
				PoolID: poolID, BackendType: BackendDirectory, RootPath: root,
				Status: StatusUnavailable, Reason: "pool marker identity does not match",
				Backing: backing,
			}, nil
		}
		if expected.FSUUID == "" && expected.Dev == 0 {
			expected = marker.Backing
		}
		if !SameBacking(expected, backing) {
			return ObservedPool{
				PoolID: poolID, BackendType: BackendDirectory, RootPath: root,
				Status: StatusUnavailable, Reason: ErrBackingChanged.Error(),
				Backing: backing,
			}, nil
		}
		if expected.RootBacked == false && backing.RootBacked {
			return ObservedPool{
				PoolID: poolID, BackendType: BackendDirectory, RootPath: root,
				Status: StatusUnavailable, Reason: "backing mount disappeared; refusing to use the naked mountpoint on the root filesystem",
				Backing: backing,
			}, nil
		}
	}
	xattrOK := d.probeXattr(root)
	caps := DirectoryCapabilities(xattrOK, backing.Shared)
	usable := st.usable()
	total := st.total()
	obs := ObservedPool{
		PoolID:       poolID,
		BackendType:  BackendDirectory,
		RootPath:     root,
		Status:       StatusAvailable,
		Capacity:     Capacity{UsableBytes: &usable, TotalBytes: &total, AllocatedBytes: int64ptr(0), ProvisionedBytes: int64ptr(0)},
		Capabilities: caps,
		Backing:      backing,
		Writable:     d.writable(root) == nil,
	}
	if backing.RootBacked {
		obs.Warnings = append(obs.Warnings, WarnRootFilesystem)
		obs.WarningText = append(obs.WarningText, RootHeadroomMessage)
	}
	if backing.Shared {
		obs.Warnings = append(obs.Warnings, WarnSharedFilesystem)
		obs.WarningText = append(obs.WarningText, SharedFSMessage)
	}
	if len(obs.Warnings) > 0 && obs.Status == StatusAvailable {
		obs.Status = StatusWarning
	}
	if !obs.Writable {
		obs.Status = StatusUnavailable
		obs.Reason = ErrNotWritable.Error()
		obs.Capacity = Capacity{}
	}
	return obs, nil
}

func (d Directory) readMarker(root string) (PoolMarker, error) {
	b, err := d.host().ReadFile(path.Join(root, MarkerFile))
	if err != nil {
		return PoolMarker{}, err
	}
	var m PoolMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return PoolMarker{}, err
	}
	if m.PoolID == "" {
		return PoolMarker{}, fmt.Errorf("pool marker missing pool_id")
	}
	return m, nil
}

func (d Directory) probeXattr(root string) bool {
	h := d.host()
	probe := path.Join(root, ".ndl-xattr-probe")
	if err := h.WriteFile(probe, []byte("x\n"), 0o600); err != nil {
		return false
	}
	defer h.Remove(probe)
	if err := h.SetXattr(probe, XattrVolumeID, "probe"); err != nil {
		return classifyXattrErr(err) == XattrOK
	}
	return true
}

func (d Directory) scanOwned(root, poolID string) ([]ObservedVolume, []ObservedLibrary) {
	var vols []ObservedVolume
	var libs []ObservedLibrary
	h := d.host()
	for _, class := range []string{ClassVMDisk, ClassTemplate, ClassContainerRoot, ClassBackupStaging} {
		dir := path.Join(root, "volumes", class)
		ents, err := h.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			abs := path.Join(dir, e.Name())
			rel, err := RelUnder(root, abs)
			if err != nil {
				continue
			}
			id := uuidFromRel(rel)
			if _, err := uuid.Parse(id); err != nil {
				continue
			}
			cls, kind, format := classFromRel(rel)
			st := ObservedVolume{
				VolumeID: id, PoolID: poolID, BackendRef: rel,
				Class: cls, Kind: kind, Format: format, Status: StatusAvailable,
			}
			if kind == KindFilesystem {
				alloc, logi, _ := h.WalkSize(abs)
				st.Allocated, st.Provisioned = alloc, logi
				st.XattrState = XattrUnsupported
			} else {
				if n, err := h.Allocated(abs); err == nil {
					st.Allocated = n
				}
				if info, err := qemuImageInfo(h, abs); err == nil && info.VirtualSize > 0 {
					st.Provisioned = info.VirtualSize
					if st.Allocated == 0 && info.ActualSize > 0 {
						st.Allocated = info.ActualSize
					}
				} else if fi, err := h.Stat(abs); err == nil {
					st.Provisioned = fi.Size()
				}
				st.XattrState = d.readVolumeXattr(abs, id)
			}
			vols = append(vols, st)
		}
	}
	for _, kind := range []string{LibraryISO, LibraryCloudImage, LibraryDiskImage} {
		dir := path.Join(root, "library", kind)
		ents, err := h.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			abs := path.Join(dir, e.Name())
			rel, err := RelUnder(root, abs)
			if err != nil {
				continue
			}
			id := uuidFromRel(rel)
			if _, err := uuid.Parse(id); err != nil {
				continue
			}
			var size int64
			if info, err := h.Stat(abs); err == nil {
				size = info.Size()
			}
			libs = append(libs, ObservedLibrary{
				ItemID: id, PoolID: poolID, BackendRef: rel, Kind: kind,
				Status: StatusAvailable, SizeBytes: size,
			})
		}
	}
	return vols, libs
}

func (d Directory) readVolumeXattr(abs, volumeID string) string {
	val, err := d.host().GetXattr(abs, XattrVolumeID)
	if err != nil {
		return classifyXattrErr(err)
	}
	if val != volumeID {
		return XattrMismatch
	}
	return XattrOK
}

func sumObserved(vols []ObservedVolume, libs []ObservedLibrary) (alloc, prov int64) {
	for _, v := range vols {
		alloc += v.Allocated
		prov += v.Provisioned
	}
	for _, l := range libs {
		alloc += l.SizeBytes
		prov += l.SizeBytes
	}
	return alloc, prov
}

// AssertWritablePool rechecks backing identity before a mutation.
func (d Directory) AssertWritablePool(hint PoolHint) (ObservedPool, error) {
	pool, _, _ := d.observeHint(hint)
	if pool.Status == StatusUnavailable {
		if pool.Reason != "" {
			return pool, fmt.Errorf("%w: %s", ErrPoolUnavailable, pool.Reason)
		}
		return pool, ErrPoolUnavailable
	}
	if !pool.Writable {
		return pool, ErrNotWritable
	}
	return pool, nil
}

// CreateVolume allocates a Directory-backed volume identified by UUID.
func (d Directory) CreateVolume(ctx context.Context, req CreateVolumeRequest, hint PoolHint) (CreateVolumeResult, error) {
	if _, err := uuid.Parse(req.VolumeID); err != nil {
		return CreateVolumeResult{}, fmt.Errorf("volume_id must be a UUID")
	}
	kind, format, err := classKindFormat(req.Class, req.Format)
	if err != nil {
		return CreateVolumeResult{}, err
	}
	if req.Size <= 0 || req.Size > MaxVolumeBytes {
		return CreateVolumeResult{}, ErrInvalidSize
	}
	if kind == KindBlock && req.Size < MinBlockBytes {
		return CreateVolumeResult{}, ErrInvalidSize
	}
	if hint.RootPath == "" {
		hint.RootPath = req.RootPath
	}
	if hint.PoolID == "" {
		hint.PoolID = req.PoolID
	}
	pool, err := d.AssertWritablePool(hint)
	if err != nil {
		return CreateVolumeResult{}, err
	}
	if pool.Capacity.UsableBytes != nil && *pool.Capacity.UsableBytes < MinPoolFreeBytes {
		return CreateVolumeResult{}, ErrCapacity
	}
	rel := volumeRel(req.Class, req.VolumeID, format)
	abs, err := JoinUnder(pool.RootPath, rel)
	if err != nil {
		return CreateVolumeResult{}, err
	}
	if err := d.refuseEscape(pool.RootPath, abs); err != nil {
		return CreateVolumeResult{}, err
	}
	h := d.host()
	if _, err := h.Stat(abs); err == nil {
		return CreateVolumeResult{}, ErrDuplicate
	}
	if err := h.MkdirAll(path.Dir(abs), 0o750); err != nil {
		return CreateVolumeResult{}, err
	}
	xattrState := XattrUnsupported
	if kind == KindFilesystem {
		if err := h.MkdirAll(abs, 0o750); err != nil {
			return CreateVolumeResult{}, err
		}
	} else {
		argv, err := QEMUCreateArgv(h.QEMUBin, format, abs, req.Size)
		if err != nil {
			return CreateVolumeResult{}, err
		}
		if err := runQEMU(ctx, h.QEMU, argv); err != nil {
			_ = h.Remove(abs)
			return CreateVolumeResult{}, err
		}
		if err := fileNonEmpty(abs); err != nil {
			_ = h.Remove(abs)
			return CreateVolumeResult{}, err
		}
		xattrState = d.writeVolumeXattr(abs, req.VolumeID)
	}
	alloc := int64(0)
	if n, err := h.Allocated(abs); err == nil {
		alloc = n
	}
	return CreateVolumeResult{
		Handle: VolumeHandle{
			VolumeID:    req.VolumeID,
			BackendType: BackendDirectory,
			BackendRef:  rel,
			Kind:        kind,
			Class:       req.Class,
			Format:      format,
		},
		Allocated:  alloc,
		XattrState: xattrState,
	}, nil
}

func (d Directory) writeVolumeXattr(abs, volumeID string) string {
	h := d.host()
	if err := h.SetXattr(abs, XattrVolumeID, volumeID); err != nil {
		return classifyXattrErr(err)
	}
	got, err := h.GetXattr(abs, XattrVolumeID)
	if err != nil {
		return classifyXattrErr(err)
	}
	if got != volumeID {
		return XattrMismatch
	}
	return XattrOK
}
