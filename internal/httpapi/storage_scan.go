package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storscan"
)

// Storage manager: measure what uses space under the host root or a storage
// pool, explain each item, and delete chosen items. Scans run in the agent in
// the background, so no request waits on a large disk.

// StorageScanRPC is the agent's storage scan surface.
type StorageScanRPC interface {
	StorageScan(ctx context.Context, action string, spec agentrpc.StorageScanSpec) (agentrpc.StorageScanResult, error)
}

// storageFilesDelete gates deleting host files from the storage manager.
// Only admins hold it by default.
const storageFilesDelete = "storage.files.delete"

type scanTarget struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	PoolID string `json:"pool_id,omitempty"`
	Total  int64  `json:"total_bytes,omitempty"`
	Used   int64  `json:"used_bytes,omitempty"`
}

func (s *Server) scanTargets(ctx context.Context, clusterID string) []scanTarget {
	out := []scanTarget{{Label: "Host root filesystem", Path: "/", Kind: "host"}}
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	for _, p := range pools {
		if p.RootPath == "" || !filepath.IsAbs(p.RootPath) {
			continue
		}
		t := scanTarget{Label: p.Name + " pool (" + p.BackendType + ")", Path: filepath.Clean(p.RootPath), Kind: "pool", PoolID: p.ID}
		if p.TotalBytes != nil && p.UsableBytes != nil && *p.UsableBytes <= *p.TotalBytes {
			t.Total, t.Used = *p.TotalBytes, *p.TotalBytes-*p.UsableBytes
		}
		out = append(out, t)
	}
	return out
}

// scanKnown labels the paths No-dal tracks: every workload's disks, pool
// roots and data folders. The same paths are protected from deletion.
func (s *Server) scanKnown(ctx context.Context, clusterID string) []storscan.Known {
	var known []storscan.Known
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	roots := map[string]string{}
	for _, p := range pools {
		roots[p.ID] = p.RootPath
	}
	owner := map[string]string{}
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	for _, w := range wls {
		disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, w.ID)
		for _, d := range disks {
			owner[d.VolumeID] = w.Kind + " " + w.Name
		}
	}
	for _, p := range pools {
		vols, _ := s.Store.ListVolumes(ctx, clusterID, p.ID)
		for _, v := range vols {
			ref := v.BackendRef
			if ref == "" {
				continue
			}
			if !filepath.IsAbs(ref) {
				ref = filepath.Join(roots[v.PoolID], ref)
			}
			label := "a " + strings.ReplaceAll(v.Class, "-", " ") + " volume"
			if o := owner[v.ID]; o != "" {
				label = "the disk of " + o
			}
			purpose := strings.ToUpper(label[:1]) + label[1:]
			adv := "Delete or shrink the workload from No-dal instead."
			known = append(known, storscan.Known{Path: filepath.Clean(ref), Purpose: purpose, Advice: adv, Protected: true})
			known = append(known, storscan.Known{Path: filepath.Clean(ref) + ".img", Purpose: purpose + " (disk image)", Advice: adv, Protected: true})
		}
	}
	return known
}

func (s *Server) scanRPC(w http.ResponseWriter) (StorageScanRPC, bool) {
	if s.Scan == nil {
		writeErr(w, http.StatusNotImplemented, "this node's agent cannot scan storage; update No-dal on the node")
		return nil, false
	}
	return s.Scan, true
}

func (s *Server) listScanTargets(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.scanTargets(r.Context(), p.User.ClusterID), "can_delete": rbac.Authorize(p.Grants, storageFilesDelete), "can_override": hasRole(p, rbac.Admin)})
}

func (s *Server) startStorageScan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	rpc, ok := s.scanRPC(w)
	if !ok {
		return
	}
	var in struct {
		Path  string `json:"path"`
		OneFS *bool  `json:"one_filesystem"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	path := filepath.Clean(strings.TrimSpace(in.Path))
	allowed := false
	for _, t := range s.scanTargets(r.Context(), p.User.ClusterID) {
		if t.Path == path || strings.HasPrefix(path, strings.TrimRight(t.Path, "/")+"/") {
			allowed = true
		}
	}
	if !allowed || !filepath.IsAbs(path) {
		writeErr(w, http.StatusBadRequest, "choose the host root, a storage pool, or a folder inside one")
		return
	}
	oneFS := in.OneFS == nil || *in.OneFS
	res, err := rpc.StorageScan(r.Context(), "scan-start", agentrpc.StorageScanSpec{Root: path, OneFS: oneFS})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.scan.start", "ok", path)
	writeJSON(w, http.StatusAccepted, res.Scan)
}

type scanItem struct {
	storscan.Node
	Path    string           `json:"path"`
	Insight storscan.Insight `json:"insight"`
}

func (s *Server) getStorageScan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	rpc, ok := s.scanRPC(w)
	if !ok {
		return
	}
	rel := strings.Trim(filepath.ToSlash(filepath.Clean("/"+r.URL.Query().Get("path"))), "/")
	res, err := rpc.StorageScan(r.Context(), "scan-status", agentrpc.StorageScanSpec{ID: r.PathValue("id")})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if rel != "" && storscan.Secret(filepath.Join(res.Scan.Root, rel)) {
		writeErr(w, http.StatusForbidden, "this folder holds keys or credentials; its contents are not listed")
		return
	}
	res, err = rpc.StorageScan(r.Context(), "scan-status", agentrpc.StorageScanSpec{ID: r.PathValue("id"), Path: rel})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	out := map[string]any{"scan": res.Scan, "path": rel}
	if res.Node != nil {
		known := s.scanKnown(r.Context(), p.User.ClusterID)
		now := s.now()
		abs := filepath.Join(res.Scan.Root, rel)
		self := scanItem{Node: *res.Node, Path: rel, Insight: storscan.Classify(abs, res.Node, known, now)}
		self.Children = nil
		items := make([]scanItem, 0, len(res.Node.Children))
		for _, c := range res.Node.Children {
			cr := strings.TrimPrefix(rel+"/"+c.Name, "/")
			item := scanItem{Node: *c, Path: cr, Insight: storscan.Classify(filepath.Join(abs, c.Name), c, known, now)}
			if storscan.Secret(filepath.Join(abs, c.Name)) {
				item.HasChildren = false
				item.Insight = storscan.Insight{Purpose: "Keys and credentials", Importance: storscan.ImportanceCritical,
					Confidence: storscan.ConfidenceLikely, Advice: "Contents are not listed.", Protected: true}
			}
			items = append(items, item)
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Size > items[j].Size })
		out["node"], out["items"] = self, items
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cancelStorageScan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	rpc, ok := s.scanRPC(w)
	if !ok {
		return
	}
	res, err := rpc.StorageScan(r.Context(), "scan-cancel", agentrpc.StorageScanSpec{ID: r.PathValue("id")})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.scan.cancel", "ok", res.Scan.Root)
	writeJSON(w, http.StatusOK, res.Scan)
}

// deleteScannedItems deletes items from a finished scan. Workload disks, pool
// roots, backups, No-dal's own folders and system folders are refused by the
// agent, as is anything with a filesystem mounted inside it.
func (s *Server) deleteScannedItems(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, storageFilesDelete)
	if err != nil {
		return
	}
	rpc, ok := s.scanRPC(w)
	if !ok {
		return
	}
	var in struct {
		Items   []string `json:"items"`
		Confirm string   `json:"confirm"`
		// Override lets an Owner delete items No-dal protects.
		Override bool `json:"override"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(in.Items) == 0 || len(in.Items) > 500 {
		writeErr(w, http.StatusBadRequest, "choose between 1 and 500 items")
		return
	}
	if want := fmt.Sprintf("delete %d", len(in.Items)); strings.TrimSpace(strings.ToLower(in.Confirm)) != want {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("confirm must be %q", want))
		return
	}
	if in.Override && !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "only an Owner can delete protected items")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	var protect []string
	for _, k := range s.scanKnown(ctx, clusterID) {
		if k.Protected {
			protect = append(protect, k.Path)
		}
	}
	var keep []string
	for _, t := range s.scanTargets(ctx, clusterID) {
		if t.Kind == "pool" {
			keep = append(keep, t.Path, filepath.Join(t.Path, "volumes"))
		}
	}
	protect = append(protect, "/var/lib/ndl/backup-repo", "/storage/backups")
	res, err := rpc.StorageScan(ctx, "delete", agentrpc.StorageScanSpec{ID: r.PathValue("id"), Items: in.Items, Protect: protect, Keep: keep, Override: in.Override})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	var freed int64
	done := 0
	for _, d := range res.Deleted {
		result := "ok"
		if !d.Deleted {
			result = "refused"
		} else {
			done++
			freed += d.Freed
		}
		action := "storage.files.delete"
		if in.Override {
			action = "storage.files.delete.override"
		}
		s.audit(r, clusterID, p.User.ID, action, result, filepath.Join(res.Scan.Root, d.Path)+" "+d.Error)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res.Deleted, "deleted": done, "freed_bytes": freed})
}
