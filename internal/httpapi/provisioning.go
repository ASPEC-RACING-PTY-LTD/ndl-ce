package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Provisioning API: lets an outside hosting platform (ASPEC Hosting) create
// and run game servers and system containers on this host, keyed by its own
// service id. Every call is idempotent per external id, so the platform's
// job worker can retry safely. The work itself is done by the regular
// game-server and workload handlers, with the caller's own credentials, so
// every validation, permission and safety rule of the UI applies unchanged.
// See docs/aspec-hosting-integration.md.

const (
	provKindGame      = "game"
	provKindContainer = "container"
	destroyConfirm    = "destroy-service"
	replaceConfirm    = "replace-service"
)

var externalIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type provisionRequest struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	TemplateID string            `json:"template_id"`
	ImagePin   string            `json:"image_pin"`
	CPUs       int               `json:"cpus"`
	MemoryMB   int64             `json:"memory_mb"`
	DiskGB     int64             `json:"disk_gb"`
	Env        map[string]string `json:"env"`
	PoolID     string            `json:"pool_id"`
	NetworkID  string            `json:"network_id"`
	Labels     map[string]string `json:"labels"`
	// Start defaults to true: the service should be running once it is
	// provisioned.
	Start *bool `json:"start"`
}

// delegate runs another handler as the same caller and returns its status
// and JSON body.
func (s *Server) delegate(r *http.Request, method, path string, body any, values map[string]string, confirm string, h http.HandlerFunc) (int, map[string]any) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method = method
	req.URL.Path = "/api/v1" + path
	req.URL.RawQuery = ""
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Del(confirmHeader)
	if confirm != "" {
		req.Header.Set(confirmHeader, confirm)
	}
	for k, v := range values {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (s *Server) provisionTarget(w http.ResponseWriter, r *http.Request, perm string) (*principal, string, *appdb.ProvisionedService, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, "", nil, false
	}
	ext := strings.TrimSpace(r.PathValue("external_id"))
	if !externalIDPattern.MatchString(ext) {
		writeErr(w, http.StatusBadRequest, "external_id must be 1-128 letters, digits, dot, dash, colon or underscore")
		return nil, "", nil, false
	}
	ps, err := s.Store.GetProvisionedService(r.Context(), p.User.ClusterID, ext)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, "", nil, false
	}
	return p, ext, ps, true
}

// putProvisionedService creates the service, or reports the existing one.
func (s *Server) putProvisionedService(w http.ResponseWriter, r *http.Request) {
	p, ext, ps, ok := s.provisionTarget(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	var req provisionRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	template := strings.TrimSpace(req.TemplateID)
	if req.Kind == provKindContainer {
		template = strings.TrimSpace(req.ImagePin)
	}
	if ps != nil {
		if ps.Kind != req.Kind || ps.Template != template {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "this external id already runs " + ps.Kind + " " + ps.Template + "; use replace to change it",
				"service": s.provisionedJSON(r, *ps),
			})
			return
		}
		writeJSON(w, http.StatusOK, s.provisionedJSON(r, *ps))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = ext
	}
	code, body, resourceID := 0, map[string]any{}, ""
	switch req.Kind {
	case provKindGame:
		if template == "" {
			writeErr(w, http.StatusBadRequest, "template_id is required for a game server")
			return
		}
		code, body = s.delegate(r, http.MethodPost, "/game-servers", map[string]any{
			"name": name, "template_id": template, "env": req.Env, "cpus": req.CPUs,
			"memory_bytes": req.MemoryMB << 20, "disk_bytes": req.DiskGB << 30,
			"notes": "Provisioned for " + ext,
		}, nil, "", s.createGameServer)
	case provKindContainer:
		if template == "" {
			writeErr(w, http.StatusBadRequest, "image_pin is required for a container")
			return
		}
		power := "running"
		if req.Start != nil && !*req.Start {
			power = "stopped"
		}
		code, body = s.delegate(r, http.MethodPost, "/workloads", map[string]any{
			"name": sanitizeContainerName(name), "kind": lxc.KindSystemContainer, "image_pin": template,
			"cpus": req.CPUs, "memory_bytes": req.MemoryMB << 20, "disk_bytes": req.DiskGB << 30,
			"pool_id": req.PoolID, "network_id": req.NetworkID, "desired_power": power, "autostart": true,
		}, nil, "", s.createWorkload)
	default:
		writeErr(w, http.StatusBadRequest, `kind must be "game" or "container"`)
		return
	}
	if code >= 300 {
		writeJSON(w, code, body)
		return
	}
	resourceID, _ = body["id"].(string)
	desired := "running"
	if req.Start != nil && !*req.Start {
		desired = "stopped"
	}
	row := appdb.ProvisionedService{
		ClusterID: p.User.ClusterID, ExternalID: ext, Kind: req.Kind, ResourceID: resourceID,
		Template: template, DesiredPower: desired, Labels: req.Labels,
	}
	if err := s.Store.UpsertProvisionedService(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, "the service was created but could not be recorded: "+err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "provisioning.create", "ok", ext+" "+req.Kind+" "+resourceID)
	writeJSON(w, http.StatusCreated, s.provisionedJSON(r, row))
}

func (s *Server) getProvisionedService(w http.ResponseWriter, r *http.Request) {
	_, _, ps, ok := s.provisionTarget(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	if ps == nil {
		writeErr(w, http.StatusNotFound, "no service is provisioned for this external id")
		return
	}
	writeJSON(w, http.StatusOK, s.provisionedJSON(r, *ps))
}

func (s *Server) listProvisionedServices(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	rows, err := s.Store.ListProvisionedServices(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.provisionedJSON(r, row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// power starts or stops the resource behind a provisioned service.
func (s *Server) provisionPower(r *http.Request, ps appdb.ProvisionedService, start bool) (int, map[string]any) {
	values := map[string]string{"id": ps.ResourceID}
	if ps.Kind == provKindGame {
		if start {
			return s.delegate(r, http.MethodPost, "/game-servers/"+ps.ResourceID+"/start", nil, values, "", s.gameServerStart)
		}
		return s.delegate(r, http.MethodPost, "/game-servers/"+ps.ResourceID+"/stop", nil, values, "", s.gameServerStop)
	}
	action := "stop"
	if start {
		action = "start"
	}
	return s.delegate(r, http.MethodPost, "/workloads/"+ps.ResourceID+"/"+action, nil, values, "", s.lifecycleWorkload(action))
}

func (s *Server) suspendProvisionedService(w http.ResponseWriter, r *http.Request) {
	s.setProvisionedPower(w, r, false)
}

func (s *Server) resumeProvisionedService(w http.ResponseWriter, r *http.Request) {
	s.setProvisionedPower(w, r, true)
}

func (s *Server) setProvisionedPower(w http.ResponseWriter, r *http.Request, running bool) {
	p, ext, ps, ok := s.provisionTarget(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	if ps == nil {
		writeErr(w, http.StatusNotFound, "no service is provisioned for this external id")
		return
	}
	if code, body := s.provisionPower(r, *ps, running); code >= 300 && !(running == false && code == http.StatusConflict) {
		writeJSON(w, code, body)
		return
	}
	ps.Suspended = !running
	ps.DesiredPower = "stopped"
	if running {
		ps.DesiredPower = "running"
	}
	if err := s.Store.UpsertProvisionedService(r.Context(), *ps); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "provisioning.suspend"
	if running {
		action = "provisioning.resume"
	}
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", ext)
	writeJSON(w, http.StatusOK, s.provisionedJSON(r, *ps))
}

// deleteProvisionedService destroys the resource and forgets the external
// id. Deleting an unknown id succeeds, so a retried destroy is harmless.
func (s *Server) deleteProvisionedService(w http.ResponseWriter, r *http.Request) {
	p, ext, ps, ok := s.provisionTarget(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != destroyConfirm {
		writeErr(w, http.StatusConflict, "destroying a service requires X-Nodal-Confirm: "+destroyConfirm)
		return
	}
	if ps == nil {
		writeJSON(w, http.StatusOK, map[string]any{"external_id": ext, "deleted": true, "existed": false})
		return
	}
	if code, body := s.destroyResource(r, *ps); code >= 300 && code != http.StatusNotFound {
		writeJSON(w, code, body)
		return
	}
	if err := s.Store.DeleteProvisionedService(r.Context(), p.User.ClusterID, ext); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "provisioning.destroy", "ok", ext+" "+ps.ResourceID)
	writeJSON(w, http.StatusOK, map[string]any{"external_id": ext, "deleted": true, "existed": true})
}

func (s *Server) destroyResource(r *http.Request, ps appdb.ProvisionedService) (int, map[string]any) {
	if ps.ResourceID == "" {
		return http.StatusOK, nil
	}
	values := map[string]string{"id": ps.ResourceID}
	if ps.Kind == provKindGame {
		return s.delegate(r, http.MethodPost, "/game-servers/"+ps.ResourceID+"/delete", nil, values, "delete", s.gameServerDelete)
	}
	// A running container must stop before it can be deleted.
	_, _ = s.provisionPower(r, ps, false)
	return s.delegate(r, http.MethodPost, "/workloads/"+ps.ResourceID+"/delete", nil, values, "delete", s.lifecycleWorkload("delete"))
}

// replaceProvisionedService switches a game service to another template:
// the current server and its files are deleted, then the new one is
// installed under the same external id. Archiving the old files is not
// done here.
func (s *Server) replaceProvisionedService(w http.ResponseWriter, r *http.Request) {
	p, ext, ps, ok := s.provisionTarget(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != replaceConfirm {
		writeErr(w, http.StatusConflict, "replacing a service requires X-Nodal-Confirm: "+replaceConfirm)
		return
	}
	if ps == nil {
		writeErr(w, http.StatusNotFound, "no service is provisioned for this external id")
		return
	}
	var req provisionRequest
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.TemplateID) == "" || ps.Kind != provKindGame {
		writeErr(w, http.StatusBadRequest, "replace needs a game service and a template_id")
		return
	}
	if code, body := s.destroyResource(r, *ps); code >= 300 && code != http.StatusNotFound {
		writeJSON(w, code, body)
		return
	}
	_ = s.Store.DeleteProvisionedService(r.Context(), p.User.ClusterID, ext)
	req.Kind = provKindGame
	raw, _ := json.Marshal(req)
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(raw))
	r2.ContentLength = int64(len(raw))
	s.audit(r, p.User.ClusterID, p.User.ID, "provisioning.replace", "ok", ext+" "+ps.Template+" -> "+req.TemplateID)
	s.putProvisionedService(w, r2)
}

// provisionedJSON reports one service with a state the platform can act
// on: provisioning, active, stopped, suspended or failed. A game server
// that finished installing is started here when it should be running.
func (s *Server) provisionedJSON(r *http.Request, ps appdb.ProvisionedService) map[string]any {
	out := map[string]any{
		"external_id": ps.ExternalID, "kind": ps.Kind, "resource_id": ps.ResourceID,
		"template": ps.Template, "desired_power": ps.DesiredPower, "suspended": ps.Suspended,
		"labels": ps.Labels, "created_at": ps.CreatedAt, "updated_at": ps.UpdatedAt,
	}
	ctx := r.Context()
	state, detail, errText, errDetail := "provisioning", "", "", ""
	switch ps.Kind {
	case provKindGame:
		row, err := s.Store.GetGameServer(ctx, ps.ClusterID, ps.ResourceID)
		if err != nil || row == nil {
			state, errText = "failed", "the game server no longer exists on this host"
			break
		}
		detail = row.Status
		switch row.Status {
		case gameserver.StatusRunning:
			state = "active"
		case gameserver.StatusFailed:
			state, errText, errDetail = "failed", humanGameError(*row), row.ErrorRaw
		case gameserver.StatusReady, gameserver.StatusStopped:
			state = "stopped"
			if ps.DesiredPower == "running" && !ps.Suspended {
				if code, body := s.provisionPower(r, ps, true); code < 300 {
					state = "provisioning"
				} else if msg, _ := body["error"].(string); msg != "" {
					state, errText = "failed", msg
				}
			}
		default:
			detail = firstNonEmpty(row.InstallPhase, row.Status)
		}
		var ports []gameserver.Port
		_ = json.Unmarshal(row.PortsJSON, &ports)
		out["ports"] = ports
	case provKindContainer:
		wl, err := s.Store.GetWorkload(ctx, ps.ClusterID, ps.ResourceID)
		if err != nil || wl == nil {
			state, errText = "failed", "the container no longer exists on this host"
			break
		}
		detail = wl.Status
		switch {
		case wl.Status == lxc.StatusRunning || wl.UnitActive:
			state = "active"
		case wl.Status == lxc.StatusStopped:
			state = "stopped"
		case strings.Contains(strings.ToLower(wl.Status), "fail") || wl.Status == lxc.StatusUnavailable:
			state = "failed"
		}
		if nics, _ := s.Store.ListWorkloadNICs(ctx, ps.ClusterID, wl.ID); len(nics) > 0 {
			out["ipv4"] = nics[0].IPv4Address
		}
		out["name"] = wl.Name
	}
	if ps.Suspended && state != "failed" {
		state = "suspended"
	}
	out["state"] = state
	out["detail"] = detail
	if errText != "" {
		out["error"] = errText
		out["error_detail"] = errDetail
	}
	return out
}

// provisioningCapacity tells the platform what this host can hold and
// whether it can run game servers at all.
func (s *Server) provisioningCapacity(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	if node, inv, err := s.cachedNode(r, p.User.ClusterID); err == nil && node != nil {
		summary := s.nodeSummary(node, inv, false)
		for _, k := range []string{"id", "name", "cpu_cores", "cpu_threads", "memory_bytes", "host_os", "status"} {
			if v, ok := summary[k]; ok {
				out[k] = v
			}
		}
	}
	var allocCPU int
	var allocMem, allocDisk int64
	if rows, err := s.Store.ListWorkloads(ctx, p.User.ClusterID); err == nil {
		for _, wl := range rows {
			allocCPU += wl.CPUs
			allocMem += wl.MemoryBytes
		}
		out["workloads"] = len(rows)
	}
	if rows, err := s.Store.ListGameServers(ctx, p.User.ClusterID); err == nil {
		for _, gs := range rows {
			allocCPU += gs.CPUs
			allocMem += gs.MemoryBytes
			allocDisk += gs.DiskBytes
		}
		out["game_servers"] = len(rows)
	}
	out["allocated"] = map[string]any{"cpus": allocCPU, "memory_bytes": allocMem, "game_disk_bytes": allocDisk}
	if pools, err := s.Store.ListStoragePools(ctx, p.User.ClusterID); err == nil {
		list := make([]map[string]any, 0, len(pools))
		for _, pool := range pools {
			list = append(list, map[string]any{"id": pool.ID, "name": pool.Name, "backend": pool.BackendType, "status": pool.Status, "usable_bytes": pool.UsableBytes})
		}
		out["pools"] = list
	}
	runtime := map[string]any{"ready": true}
	if err := s.gameRuntimeReady(ctx); err != nil {
		runtime = map[string]any{"ready": false, "reason": gameserver.HumanError(err.Error())}
	}
	out["game_runtime"] = runtime
	writeJSON(w, http.StatusOK, out)
}

var containerNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// sanitizeContainerName turns a service name into a host name LXC accepts.
func sanitizeContainerName(name string) string {
	n := strings.Trim(containerNameUnsafe.ReplaceAllString(name, "-"), "-")
	if len(n) > 60 {
		n = strings.Trim(n[:60], "-")
	}
	if n == "" {
		n = "service"
	}
	return n
}
