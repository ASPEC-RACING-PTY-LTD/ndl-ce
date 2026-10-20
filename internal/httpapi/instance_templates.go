package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Settings templates for system containers, OCI containers and game
// servers. A template is the create form's fields; using one opens the
// create page filled in. VM templates (phase18.go) also keep a disk snapshot.

const (
	tmplKindContainer = "system-container"
	tmplKindOCI       = "oci"
	tmplKindGame      = "game-server"
	tmplMaxSpec       = 16 << 10
	tmplMaxPerCluster = 500
)

type containerTmplSpec struct {
	ImagePin    string   `json:"image_pin,omitempty"`
	CPUs        int      `json:"cpus,omitempty"`
	MemoryBytes int64    `json:"memory_bytes,omitempty"`
	DiskBytes   int64    `json:"disk_bytes,omitempty"`
	NetworkID   string   `json:"network_id,omitempty"`
	PoolID      string   `json:"pool_id,omitempty"`
	Privileged  bool     `json:"privileged,omitempty"`
	Autostart   *bool    `json:"autostart,omitempty"`
	Extras      []string `json:"extras,omitempty"`
}

type ociTmplSpec struct {
	Image       string            `json:"image,omitempty"`
	CPUs        int               `json:"cpus,omitempty"`
	MemoryBytes int64             `json:"memory_bytes,omitempty"`
	Ports       []string          `json:"ports,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

type gameTmplSpec struct {
	GameTemplateID string            `json:"game_template_id,omitempty"`
	CPUs           int               `json:"cpus,omitempty"`
	MemoryBytes    int64             `json:"memory_bytes,omitempty"`
	DiskBytes      int64             `json:"disk_bytes,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

func boundedCPU(n int) bool     { return n >= 0 && n <= 512 }
func boundedBytes(n int64) bool { return n >= 0 && n <= 64<<40 }
func boundedEnv(m map[string]string) bool {
	if len(m) > 100 {
		return false
	}
	for k, v := range m {
		if k == "" || len(k) > 128 || len(v) > 4096 || strings.ContainsAny(k, "=\x00") {
			return false
		}
	}
	return true
}

// normalizeTemplateSpec decodes spec for kind, rejects values out of range
// and re-encodes it so unknown keys are dropped.
func normalizeTemplateSpec(kind string, raw json.RawMessage) (json.RawMessage, string) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > tmplMaxSpec {
		return nil, "spec is too large"
	}
	var out any
	switch kind {
	case tmplKindContainer:
		var s containerTmplSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "spec is not valid for a system container"
		}
		if !boundedCPU(s.CPUs) || !boundedBytes(s.MemoryBytes) || !boundedBytes(s.DiskBytes) || len(s.ImagePin) > 512 {
			return nil, "spec values are out of range"
		}
		if len(s.Extras) > 64 {
			return nil, "too many extras"
		}
		known := map[string]bool{}
		for _, e := range guestextras.Catalog() {
			known[e.ID] = true
		}
		extras := make([]string, 0, len(s.Extras))
		for _, id := range uniqueStrings(s.Extras) {
			if !known[id] {
				return nil, "unknown extra " + id
			}
			if !guestextras.IsBaseline(id) {
				extras = append(extras, id)
			}
		}
		s.Extras = extras
		out = s
	case tmplKindOCI:
		var s ociTmplSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "spec is not valid for an OCI container"
		}
		if !boundedCPU(s.CPUs) || !boundedBytes(s.MemoryBytes) || len(s.Image) > 512 || len(s.Ports) > 64 || !boundedEnv(s.Env) {
			return nil, "spec values are out of range"
		}
		out = s
	case tmplKindGame:
		var s gameTmplSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "spec is not valid for a game server"
		}
		if !boundedCPU(s.CPUs) || !boundedBytes(s.MemoryBytes) || !boundedBytes(s.DiskBytes) || len(s.GameTemplateID) > 128 || !boundedEnv(s.Env) {
			return nil, "spec values are out of range"
		}
		out = s
	default:
		return nil, "kind must be system-container, oci or game-server"
	}
	b, _ := json.Marshal(out)
	return b, ""
}

// specFromWorkload copies a workload's settings into a template spec.
func specFromWorkload(w appdb.Workload) (string, json.RawMessage) {
	if w.Kind == tmplKindOCI {
		b, _ := json.Marshal(ociTmplSpec{Image: w.ImagePin, CPUs: w.CPUs, MemoryBytes: w.MemoryBytes})
		return tmplKindOCI, b
	}
	auto := w.Autostart
	b, _ := json.Marshal(containerTmplSpec{ImagePin: w.ImagePin, CPUs: w.CPUs, MemoryBytes: w.MemoryBytes, Privileged: w.Privileged, Autostart: &auto})
	return tmplKindContainer, b
}

func instanceTemplateJSON(t appdb.InstanceTemplate) map[string]any {
	spec := t.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	return map[string]any{
		"id": t.ID, "kind": t.Kind, "name": t.Name, "description": t.Description, "spec": spec,
		"source_workload_id": t.SourceWorkloadID, "created_by": t.CreatedBy,
		"created_at": t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listInstanceTemplates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListInstanceTemplates(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		if kind == "" || t.Kind == kind {
			out = append(out, instanceTemplateJSON(t))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getInstanceTemplate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	t, err := s.Store.GetInstanceTemplate(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if t == nil {
		writeErr(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, instanceTemplateJSON(*t))
}

func (s *Server) createInstanceTemplate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req struct {
		Kind        string          `json:"kind"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Spec        json.RawMessage `json:"spec"`
		WorkloadID  string          `json:"workload_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(req.Name)
	kind := strings.TrimSpace(req.Kind)
	spec := req.Spec
	source := ""
	if id := strings.TrimSpace(req.WorkloadID); id != "" {
		row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
		if err != nil || row == nil || (row.Kind != tmplKindContainer && row.Kind != tmplKindOCI) {
			writeErr(w, http.StatusNotFound, "workload not found")
			return
		}
		kind, spec = specFromWorkload(*row)
		source = row.ID
		if name == "" {
			name = row.Name + "-template"
		}
	}
	if name == "" || len(name) > 128 {
		writeErr(w, http.StatusBadRequest, "name is required (at most 128 characters)")
		return
	}
	if len(req.Description) > 1000 {
		writeErr(w, http.StatusBadRequest, "description is at most 1000 characters")
		return
	}
	norm, msg := normalizeTemplateSpec(kind, spec)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	existing, err := s.Store.ListInstanceTemplates(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(existing) >= tmplMaxPerCluster {
		writeErr(w, http.StatusConflict, "too many templates; delete some first")
		return
	}
	t := appdb.InstanceTemplate{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Kind: kind, Name: name,
		Description: strings.TrimSpace(req.Description), Spec: norm, SourceWorkloadID: source, CreatedBy: p.User.ID,
	}
	if err := s.Store.CreateInstanceTemplate(r.Context(), t); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	stored, err := s.Store.GetInstanceTemplate(r.Context(), p.User.ClusterID, t.ID)
	if err != nil || stored == nil {
		writeErr(w, http.StatusInternalServerError, "could not record template")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "instance.template.create", "ok", stored.ID)
	writeJSON(w, http.StatusCreated, instanceTemplateJSON(*stored))
}

func (s *Server) deleteInstanceTemplate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	ok, err := s.Store.DeleteInstanceTemplate(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "template not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "instance.template.delete", "ok", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}
