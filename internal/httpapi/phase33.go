package httpapi

import (
	"net/http"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) exportBackupDR(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	nodes, err := s.Store.ListClusterNodes(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	workloads, err := s.Store.ListWorkloads(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	arts, err := s.Store.ListBackupArtifacts(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	targets, err := s.Store.ListBackupTargets(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeItems := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		role := n.Role
		if role == "" {
			role = "control"
		}
		item := map[string]any{"id": n.ID, "name": n.Name, "role": role, "hostname": n.Hostname}
		if n.RevokedAt != nil {
			item["revoked"] = true
			item["revoked_at"] = n.RevokedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		nodeItems = append(nodeItems, item)
	}
	wlItems := make([]map[string]any, 0, len(workloads))
	for _, wl := range workloads {
		wlItems = append(wlItems, map[string]any{
			"id": wl.ID, "name": wl.Name, "kind": wl.Kind,
			"node_id": wl.NodeID, "owner_node_id": wl.OwnerNodeID, "desired_node_id": wl.DesiredNodeID,
			"status": wl.Status,
		})
	}
	artItems := make([]map[string]any, 0, len(arts))
	for _, a := range arts {
		appdb.FillArtifactLocality(&a)
		item := map[string]any{
			"id": a.ID, "workload_id": a.WorkloadID, "run_id": a.RunID,
			"checksum_sha256": a.ChecksumSHA256, "size_bytes": a.SizeBytes,
			"locator": a.Locator, "format": a.Format, "locality": a.Locality,
			"created_at": a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if a.ObjectKey != "" {
			item["object_key"] = a.ObjectKey
		}
		if a.PullURL != "" {
			item["pull_url"] = a.PullURL
		}
		if a.Encrypted {
			item["encrypted"] = true
		}
		artItems = append(artItems, item)
	}
	tgtItems := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		item := map[string]any{"id": t.ID, "name": t.Name, "kind": t.Kind, "status": t.Status}
		if t.Locator != "" {
			item["locator"] = t.Locator
		}
		if t.Endpoint != "" {
			item["endpoint"] = t.Endpoint
		}
		if t.Region != "" {
			item["region"] = t.Region
		}
		if t.Bucket != "" {
			item["bucket"] = t.Bucket
		}
		if t.Prefix != "" {
			item["prefix"] = t.Prefix
		}
		tgtItems = append(tgtItems, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster_id":  p.User.ClusterID,
		"exported_at": s.now().UTC().Format("2006-01-02T15:04:05Z"),
		"nodes":       nodeItems,
		"workloads":   wlItems,
		"artifacts":   artItems,
		"targets":     tgtItems,
	})
}
