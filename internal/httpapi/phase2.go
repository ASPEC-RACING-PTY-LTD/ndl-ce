package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []map[string]any{}
	if node != nil {
		items = append(items, s.nodeSummary(node, inv, redactViewer(p)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, s.nodeSummary(node, inv, redactViewer(p)))
}

func (s *Server) nodeHardware(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if inv == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "collecting",
			"stale":   false,
			"message": "Collecting",
		})
		return
	}
	payload := inv.Payload
	if parsed, ok := decodeInv(inv); ok {
		parsed.Stale = inv.Stale
		if redactViewer(p) {
			parsed = inventory.RedactForViewer(parsed)
		}
		if b, err := json.Marshal(parsed); err == nil {
			payload = b
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":     node.ID,
		"observed_at": inv.ObservedAt.UTC().Format(time.RFC3339),
		"stale":       inv.Stale,
		"status":      inventoryStatus(inv),
		"inventory":   json.RawMessage(payload),
	})
}

func (s *Server) nodeCapabilities(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	caps := []inventory.Capability{}
	if parsed, ok := decodeInv(inv); ok {
		caps = parsed.Capabilities
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":      node.ID,
		"stale":        inv != nil && inv.Stale,
		"capabilities": caps,
	})
}

func (s *Server) nodeMetrics(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MetricsRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if s.Observer == nil {
		writeJSON(w, http.StatusOK, metrics.QueryResult{Status: metrics.StatusUnavailable, Series: []metrics.Series{}})
		return
	}
	if inv != nil && inv.Stale {
		writeJSON(w, http.StatusOK, metrics.QueryResult{Status: metrics.StatusStale, Series: []metrics.Series{}})
		return
	}
	from, to := parseWindow(r)
	res, err := s.Observer.GetMetrics(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusOK, metrics.QueryResult{Status: metrics.StatusUnavailable, Series: []metrics.Series{}})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	ops, err := s.Store.ListOperations(r.Context(), p.User.ClusterID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ops == nil {
		ops = []appdb.Operation{}
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		item := map[string]any{
			"id":         op.ID,
			"kind":       op.Kind,
			"state":      op.State,
			"stage":      op.Stage,
			"message":    op.Message,
			"created_at": op.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": op.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if op.Progress != nil {
			item["progress"] = *op.Progress
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.EventsRead)
	if err != nil {
		return
	}
	events, err := s.Store.ListEvents(r.Context(), p.User.ClusterID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": encodeEvents(events)})
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireTLS(w, r) {
		return
	}
	p, err := s.require(w, r, rbac.EventsRead)
	if err != nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "stream unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := s.Hub.subscribe()
	if ch != nil {
		defer s.Hub.unsubscribe(ch)
	}
	recent, _ := s.Store.ListEvents(r.Context(), p.User.ClusterID, 10)
	for i := len(recent) - 1; i >= 0; i-- {
		writeSSE(w, recent[i])
	}
	flusher.Flush()
	if ch == nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.ClusterID != "" && e.ClusterID != p.User.ClusterID {
				continue
			}
			writeSSE(w, e)
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, e appdb.Event) {
	b, err := json.Marshal(encodeEvent(e))
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}

func encodeEvents(events []appdb.Event) []map[string]any {
	if events == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		out = append(out, encodeEvent(e))
	}
	return out
}

func encodeEvent(e appdb.Event) map[string]any {
	payload := json.RawMessage(`{}`)
	if len(e.Payload) > 0 {
		payload = e.Payload
	}
	return map[string]any{
		"id":         e.ID,
		"type":       e.Type,
		"node_id":    e.NodeID,
		"payload":    payload,
		"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) cachedNode(r *http.Request, clusterID string) (*appdb.Node, *appdb.HardwareInventory, error) {
	node, err := s.Store.GetNode(r.Context(), clusterID)
	if err != nil || node == nil {
		return node, nil, err
	}
	inv, err := s.Store.GetInventory(r.Context(), node.ID)
	return node, inv, err
}

func (s *Server) nodeSummary(node *appdb.Node, inv *appdb.HardwareInventory, redact bool) map[string]any {
	out := map[string]any{
		"id":     node.ID,
		"name":   node.Name,
		"status": "unknown",
	}
	if parsed, ok := decodeInv(inv); ok {
		if redact {
			parsed = inventory.RedactForViewer(parsed)
		}
		out["status"] = inventoryStatus(inv)
		out["stale"] = inv.Stale
		out["observed_at"] = inv.ObservedAt.UTC().Format(time.RFC3339)
		out["host_os"] = parsed.Host.PrettyName
		if out["host_os"] == "" {
			out["host_os"] = parsed.Host.ID + " " + parsed.Host.VersionID
		}
		out["host_id"] = parsed.Host.ID
		out["host_version_id"] = parsed.Host.VersionID
		out["cpu_model"] = parsed.CPU.Model
		out["cpu_sockets"] = parsed.CPU.Sockets
		out["cpu_cores"] = parsed.CPU.Cores
		out["cpu_threads"] = parsed.CPU.Threads
		out["memory_bytes"] = parsed.Memory.TotalBytes
		out["disk_count"] = len(parsed.BlockDevices)
		var diskBytes uint64
		for _, d := range parsed.BlockDevices {
			diskBytes += d.SizeBytes
		}
		out["disk_bytes"] = diskBytes
		out["nic_count"] = len(parsed.NICs)
		out["gpu_count"] = len(parsed.GPUs)
		out["gpu_present"] = len(parsed.GPUs) > 0
	} else {
		out["status"] = "collecting"
	}
	return out
}

func decodeInv(inv *appdb.HardwareInventory) (inventory.Inventory, bool) {
	if inv == nil || len(inv.Payload) == 0 {
		return inventory.Inventory{}, false
	}
	var parsed inventory.Inventory
	if err := json.Unmarshal(inv.Payload, &parsed); err != nil {
		return inventory.Inventory{}, false
	}
	return parsed, true
}

func inventoryStatus(inv *appdb.HardwareInventory) string {
	if inv == nil {
		return "collecting"
	}
	if inv.Stale {
		return "stale"
	}
	return "available"
}

func redactViewer(p *principal) bool {
	if p == nil {
		return true
	}
	if rbac.Authorize(p.Grants, rbac.All) {
		return false
	}
	for _, role := range p.Roles {
		if role == rbac.Admin || role == rbac.Operator {
			return false
		}
	}
	return true
}

func parseWindow(r *http.Request) (time.Time, time.Time) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if mins := r.URL.Query().Get("minutes"); mins != "" {
		if n, err := strconv.Atoi(mins); err == nil && n > 0 {
			from = to.Add(-time.Duration(n) * time.Minute)
		}
	}
	return from, to
}
