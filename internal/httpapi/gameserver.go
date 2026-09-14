package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) gameRuntime() *gameserver.Runtime {
	if s.Game != nil {
		return s.Game
	}
	root := "/var/lib/ndl"
	if dir, err := os.MkdirTemp("", "ndl-gs-"); err == nil && strings.Contains(root, "not-used") {
		root = dir
	}
	s.Game = gameserver.NewRuntime(root)
	return s.Game
}

func (s *Server) requireGameFeature(w http.ResponseWriter, r *http.Request, perm string) (*principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, false
	}
	if !s.gameServersEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusNotFound, "Game Servers is not enabled. Enable it from Add Features.")
		return nil, false
	}
	return p, true
}

func (s *Server) gameServersEnabled(ctx context.Context, clusterID string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, features.IDGameServers)
	return row != nil && row.Enabled
}

func gameGrant(action string) string {
	switch action {
	case "view":
		return rbac.GameServerRead
	case "create":
		return rbac.GameServerCreate
	case "power":
		return rbac.GameServerPower
	case "console":
		return rbac.GameServerConsole
	case "files":
		return rbac.GameServerFiles
	case "config":
		return rbac.GameServerConfig
	case "content":
		return rbac.GameServerContent
	case "backup":
		return rbac.GameServerBackup
	case "schedule":
		return rbac.GameServerSchedule
	case "network":
		return rbac.GameServerNetwork
	case "users":
		return rbac.GameServerUsers
	case "reinstall":
		return rbac.GameServerReinstall
	case "delete":
		return rbac.GameServerDelete
	default:
		return rbac.GameServerAdmin
	}
}

func (s *Server) canGame(ctx context.Context, p *principal, srv *appdb.GameServer, action string) bool {
	if rbac.Authorize(p.Grants, rbac.All) || rbac.Authorize(p.Grants, rbac.GameServerAdmin) {
		return true
	}
	need := gameGrant(action)
	if !rbac.Authorize(p.Grants, need) && !rbac.Authorize(p.Grants, rbac.GameServerRead) && action != "view" {
		return false
	}
	if srv == nil {
		return rbac.Authorize(p.Grants, need)
	}
	if srv.OwnerUserID == p.User.ID {
		return true
	}
	acl, _ := s.Store.GetGameACL(ctx, srv.ID, p.User.ID)
	if acl == nil {
		return rbac.Authorize(p.Grants, rbac.GameServerAdmin) || (action == "view" && rbac.Authorize(p.Grants, rbac.GameServerRead) && hasRole(p, rbac.Operator))
	}
	var grants []string
	_ = json.Unmarshal(acl.Grants, &grants)
	for _, g := range grants {
		if g == action || g == "admin" {
			return true
		}
	}
	return action == "view" && len(grants) > 0
}

func (s *Server) loadGame(w http.ResponseWriter, r *http.Request, p *principal, action string) (*appdb.GameServer, bool) {
	row, err := s.Store.GetGameServer(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "game server not found")
		return nil, false
	}
	if !s.canGame(r.Context(), p, row, action) {
		writeErr(w, http.StatusForbidden, "not allowed for this game server")
		return nil, false
	}
	return row, true
}

func (s *Server) resolveTemplate(ctx context.Context, clusterID, id string) (gameserver.Template, error) {
	if t, ok := builtinByID(id); ok {
		return t, nil
	}
	stored, err := s.Store.GetGameTemplate(ctx, clusterID, id)
	if err != nil || stored == nil {
		return gameserver.Template{}, fmt.Errorf("template not found")
	}
	var t gameserver.Template
	if err := json.Unmarshal(stored.Body, &t); err != nil {
		return gameserver.Template{}, fmt.Errorf("stored template is corrupt")
	}
	return t, nil
}

func builtinByID(id string) (gameserver.Template, bool) {
	for _, t := range gameserver.BuiltinTemplates() {
		if t.ID == id {
			return t, true
		}
	}
	return gameserver.Template{}, false
}

func (s *Server) listGameServers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	rows, err := s.Store.ListGameServers(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if !s.canGame(r.Context(), p, &row, "view") {
			continue
		}
		items = append(items, s.gameJSON(r.Context(), row, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) searchGameServers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	rows, _ := s.Store.ListGameServers(r.Context(), p.User.ClusterID)
	var items []map[string]any
	for _, row := range rows {
		if !s.canGame(r.Context(), p, &row, "view") {
			continue
		}
		hay := strings.ToLower(row.Name + " " + row.Game + " " + row.Implementation + " " + row.Status + " " + row.Notes)
		if q == "" || strings.Contains(hay, q) {
			items = append(items, s.gameJSON(r.Context(), row, false))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "q": q})
}

type createGameReq struct {
	Name        string            `json:"name"`
	TemplateID  string            `json:"template_id"`
	ImageLabel  string            `json:"image_label"`
	NodeID      string            `json:"node_id"`
	Env         map[string]string `json:"env"`
	Ports       []gameserver.Port `json:"ports"`
	CPUs        int               `json:"cpus"`
	MemoryBytes int64             `json:"memory_bytes"`
	DiskBytes   int64             `json:"disk_bytes"`
	Notes       string            `json:"notes"`
}

func (s *Server) createGameServer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerCreate)
	if !ok {
		return
	}
	var req createGameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, req.TemplateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	env := mergeUserEnv(tmpl, req.Env)
	if err := validateCreate(tmpl, env); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	node, _ := s.Store.GetNode(r.Context(), p.User.ClusterID)
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" && node != nil {
		nodeID = node.ID
	}
	ports := req.Ports
	if len(ports) == 0 {
		ports = tmpl.DefaultPorts
	}
	mem := req.MemoryBytes
	if mem == 0 {
		mem = int64(tmpl.DefaultMemoryMB) << 20
	}
	disk := req.DiskBytes
	if disk == 0 {
		disk = int64(tmpl.DefaultDiskMB) << 20
	}
	cpus := req.CPUs
	if cpus == 0 {
		cpus = tmpl.DefaultCPUs
	}
	id := uuid.NewString()
	rt := s.gameRuntime()
	dir, err := rt.EnsureData(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := appdb.GameServer{
		ID: id, ClusterID: p.User.ClusterID, NodeID: nodeID, OwnerUserID: p.User.ID,
		Name: sanitizeGameName(req.Name), Notes: req.Notes, TemplateID: tmpl.ID, TemplateName: tmpl.Name,
		Game: tmpl.Game, Implementation: tmpl.Implementation, Family: tmpl.Family,
		Status: gameserver.StatusPending, DesiredPower: "stopped",
		Image: tmpl.ImageFor(req.ImageLabel), ImageLabel: req.ImageLabel,
		Startup: tmpl.Startup, EnvJSON: mustJSON(env), PortsJSON: mustJSON(ports),
		CPUs: cpus, MemoryBytes: mem, DiskBytes: disk, Capabilities: mustJSON(tmpl.Capabilities),
		DataDir: dir, CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	if err := s.Store.CreateGameServer(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "created", "Server created from "+tmpl.Name, "")
	go s.installGame(row, tmpl)
	writeJSON(w, http.StatusAccepted, s.gameJSON(r.Context(), row, true))
}

func (s *Server) installGame(row appdb.GameServer, tmpl gameserver.Template) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	row.Status = gameserver.StatusInstalling
	row.InstallPhase = "downloading"
	_ = s.Store.UpdateGameServer(ctx, row)
	rt := s.gameRuntime()
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	srv := gameserver.Server{ID: row.ID, Env: env}
	if err := rt.Install(ctx, srv, tmpl); err != nil {
		row.Status = gameserver.StatusFailed
		row.ErrorRaw = err.Error()
		row.ErrorHuman = gameserver.HumanError(err.Error())
		row.InstallPhase = "failed"
		_ = s.Store.UpdateGameServer(ctx, row)
		_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{ID: uuid.NewString(), ServerID: row.ID, ClusterID: row.ClusterID, Kind: "install.failed", Summary: row.ErrorHuman, Detail: clipGame(err.Error(), 800), CreatedAt: s.now()})
		return
	}
	row.Status = gameserver.StatusReady
	row.InstallPhase = "ready"
	row.ErrorHuman, row.ErrorRaw = "", ""
	_ = s.Store.UpdateGameServer(ctx, row)
	_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{ID: uuid.NewString(), ServerID: row.ID, ClusterID: row.ClusterID, Kind: "install.ok", Summary: "Install finished", CreatedAt: s.now()})
}

func (s *Server) getGameServer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.gameJSON(r.Context(), *row, true))
}

func (s *Server) patchGameServer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name != "" {
		row.Name = sanitizeGameName(req.Name)
	}
	if req.Notes != "" {
		row.Notes = req.Notes
	}
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "updated", "Server details updated", "")
	writeJSON(w, http.StatusOK, s.gameJSON(r.Context(), *row, true))
}

func (s *Server) powerGame(w http.ResponseWriter, r *http.Request, action string) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerPower)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "power")
	if !ok {
		return
	}
	rt := s.gameRuntime()
	var err error
	switch action {
	case "start":
		err = s.startGame(r.Context(), rt, row)
	case "stop":
		err = rt.Stop(r.Context(), row.ID)
		row.Status = gameserver.StatusStopped
		row.DesiredPower = "stopped"
		row.ContainerID = ""
	case "restart":
		_ = rt.Stop(r.Context(), row.ID)
		err = s.startGame(r.Context(), rt, row)
	case "kill":
		err = rt.Kill(r.Context(), row.ID)
		row.Status = gameserver.StatusStopped
		row.DesiredPower = "stopped"
		row.ContainerID = ""
	}
	if err != nil {
		row.Status = gameserver.StatusFailed
		row.ErrorRaw = err.Error()
		row.ErrorHuman = gameserver.HumanError(err.Error())
		_ = s.Store.UpdateGameServer(r.Context(), *row)
		writeErr(w, http.StatusBadGateway, row.ErrorHuman)
		return
	}
	row.ErrorHuman, row.ErrorRaw = "", ""
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "power."+action, "Power "+action, "")
	writeJSON(w, http.StatusOK, s.gameJSON(r.Context(), *row, true))
}

func (s *Server) startGame(ctx context.Context, rt *gameserver.Runtime, row *appdb.GameServer) error {
	tmpl, err := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if err != nil {
		return err
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	if strings.EqualFold(env["EULA"], "false") && hasCapJSON(row.Capabilities, gameserver.CapEULA) {
		return fmt.Errorf("eula is not accepted")
	}
	if hasCapJSON(row.Capabilities, gameserver.CapLicenseKey) && strings.TrimSpace(env["FIVEM_LICENSE"]) == "" {
		return fmt.Errorf("fivem license key is required")
	}
	for _, key := range tmpl.StartRequires {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if strings.TrimSpace(env[key]) != "" {
			continue
		}
		switch key {
		case "CLUSTER_TOKEN":
			return fmt.Errorf("klei cluster token is required")
		case "FIVEM_LICENSE":
			return fmt.Errorf("fivem license key is required")
		default:
			return fmt.Errorf("%s is required to start", key)
		}
	}
	startup := gameserver.ExpandStartup(tmpl.Startup, env, gameserver.MemoryMB(row.MemoryBytes), gameserver.PrimaryPort(ports))
	cid, err := rt.Start(ctx, gameserver.RunSpec{
		ID: row.ID, Image: row.Image, Startup: startup, Env: env, Ports: ports,
		CPUs: row.CPUs, MemoryBytes: row.MemoryBytes, WorkDir: tmpl.WorkingDir,
	})
	if err != nil {
		return err
	}
	row.ContainerID = cid
	row.Status = gameserver.StatusRunning
	row.DesiredPower = "running"
	return nil
}

func (s *Server) gameServerStart(w http.ResponseWriter, r *http.Request) {
	s.powerGame(w, r, "start")
}
func (s *Server) gameServerStop(w http.ResponseWriter, r *http.Request) {
	s.powerGame(w, r, "stop")
}
func (s *Server) gameServerRestart(w http.ResponseWriter, r *http.Request) {
	s.powerGame(w, r, "restart")
}
func (s *Server) gameServerKill(w http.ResponseWriter, r *http.Request) {
	s.powerGame(w, r, "kill")
}

func (s *Server) gameServerReinstall(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerReinstall)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "reinstall")
	if !ok {
		return
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.gameRuntime().Stop(r.Context(), row.ID)
	s.createRollback(r.Context(), *row, "pre-reinstall")
	row.Status = gameserver.StatusPending
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	go s.installGame(*row, tmpl)
	s.noteGame(r, p, row.ID, "reinstall", "Reinstall started", "")
	writeJSON(w, http.StatusAccepted, s.gameJSON(r.Context(), *row, true))
}

func (s *Server) gameServerDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerDelete)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != "delete" {
		writeErr(w, http.StatusConflict, "deleting a game server requires X-Nodal-Confirm: delete")
		return
	}
	row, ok := s.loadGame(w, r, p, "delete")
	if !ok {
		return
	}
	_ = s.gameRuntime().Kill(r.Context(), row.ID)
	_ = os.RemoveAll(s.gameRuntime().DataDir(row.ID))
	if err := s.Store.DeleteGameServer(r.Context(), p.User.ClusterID, row.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.delete", "ok", row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": row.ID})
}

func (s *Server) gameServerFavorite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	row.Pinned = !row.Pinned
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	writeJSON(w, http.StatusOK, s.gameJSON(r.Context(), *row, false))
}

func (s *Server) gameJSON(ctx context.Context, row appdb.GameServer, detail bool) map[string]any {
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	var caps []string
	_ = json.Unmarshal(row.Capabilities, &caps)
	out := map[string]any{
		"id": row.ID, "name": row.Name, "status": row.Status, "desired_power": row.DesiredPower,
		"template_id": row.TemplateID, "template_name": row.TemplateName, "game": row.Game,
		"implementation": row.Implementation, "family": row.Family, "node_id": row.NodeID,
		"cpus": row.CPUs, "memory_bytes": row.MemoryBytes, "disk_bytes": row.DiskBytes,
		"ports": ports, "capabilities": caps, "pinned": row.Pinned, "created_at": row.CreatedAt,
		"updated_at": row.UpdatedAt, "install_phase": row.InstallPhase, "error_human": row.ErrorHuman,
		"image_label": row.ImageLabel, "notes": row.Notes,
	}
	if detail {
		out["env"] = gameserver.RedactEnv(env)
		out["startup"] = row.Startup
		out["image"] = row.Image
		out["owner_user_id"] = row.OwnerUserID
		if row.ErrorRaw != "" {
			out["error_raw"] = gameserver.RedactLog(row.ErrorRaw, env)
		}
		out["install_log"] = gameserver.RedactLog(row.InstallLog, env)
	}
	return out
}

func (s *Server) noteGame(r *http.Request, p *principal, serverID, kind, summary, detail string) {
	_ = s.Store.InsertGameEvent(r.Context(), appdb.GameEvent{
		ID: uuid.NewString(), ServerID: serverID, ClusterID: p.User.ClusterID, UserID: p.User.ID,
		Kind: kind, Summary: summary, Detail: detail, CreatedAt: s.now(),
	})
}

func sanitizeGameName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Game server"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func mergeUserEnv(t gameserver.Template, user map[string]string) map[string]string {
	out := map[string]string{}
	for _, v := range t.Variables {
		out[v.Env] = v.Default
	}
	for k, v := range user {
		out[k] = v
	}
	return out
}

func validateCreate(t gameserver.Template, env map[string]string) error {
	for _, v := range t.Variables {
		if v.Required && strings.TrimSpace(env[v.Env]) == "" {
			return fmt.Errorf("%s is required", v.Name)
		}
	}
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func hasCapJSON(raw []byte, cap string) bool {
	var caps []string
	_ = json.Unmarshal(raw, &caps)
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

func clipGame(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (s *Server) createRollback(ctx context.Context, row appdb.GameServer, reason string) {
	_, _ = s.snapshotBackup(ctx, row, "Rollback "+reason, reason)
}
