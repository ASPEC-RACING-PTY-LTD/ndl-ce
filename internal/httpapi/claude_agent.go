package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/claudeagent"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

// Claude Agent (Add Features > Claude Agent). One system container runs
// Claude Code as "claude remote-control", so the operator starts chats on
// claude.ai/code or in the Claude app. It only reaches what is granted here:
// game server folders, mounted into the container with an ACL for its user,
// and through the ndl command (a scoped token checked by the routes under
// /api/v1/claude-agent/api) a server's log, console and restart.

const (
	caWorkloadName = "claude-agent"
	caActor        = "claude-agent"
	caTokenPrefix  = "ndlca_"
	caDefaultCPUs  = 2
	caDefaultMemMB = 2048
	caDefaultDisk  = 16
	// caSetupTimeout bounds creating the container and installing Claude Code.
	caSetupTimeout = 40 * time.Minute
	// caRemoveConfirm is the X-Nodal-Confirm value for removing the agent.
	caRemoveConfirm = "remove-claude-agent"
)

// caPollEvery is how often setup checks whether the container is up.
var caPollEvery = 5 * time.Second

// Agent setup states.
const (
	caStateCreating   = "creating"
	caStateInstalling = "installing"
	caStateReady      = "ready"
	caStateFailed     = "failed"
)

// caApplied is a folder whose ACL and mount are in place.
type caApplied struct {
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
	Write    bool   `json:"write"`
	Source   string `json:"source"`
	Target   string `json:"target"`
}

// caConfig is the claude_agent_config document.
type caConfig struct {
	WorkloadID     string              `json:"workload_id,omitempty"`
	State          string              `json:"state,omitempty"`
	Error          string              `json:"error,omitempty"`
	Log            []string            `json:"log,omitempty"`
	Name           string              `json:"name,omitempty"`
	PermissionMode string              `json:"permission_mode,omitempty"`
	TokenHash      string              `json:"token_hash,omitempty"`
	Grants         []claudeagent.Grant `json:"grants"`
	Applied        []caApplied         `json:"applied,omitempty"`
}

type claudeAgentRPC interface {
	ClaudeAgent(ctx context.Context, action string, req claudeagent.Request, out any) error
}

// caState is the in-memory side: one setup at a time, and serialised
// changes to the config document.
type caState struct {
	mu    sync.Mutex
	setup bool
}

func (s *Server) caRPC() (claudeAgentRPC, bool) {
	rpc, ok := s.Workloads.(claudeAgentRPC)
	return rpc, ok && s.Workloads != nil
}

func (s *Server) caEnabled(ctx context.Context, clusterID string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, features.IDClaudeAgent)
	return row != nil && row.Enabled
}

func (s *Server) caLoad(ctx context.Context, clusterID string) (caConfig, error) {
	var c caConfig
	raw, err := s.Store.ClaudeAgentConfig(ctx, clusterID)
	if err != nil {
		return c, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("claude agent settings are corrupt: %w", err)
		}
	}
	if c.Name == "" {
		c.Name = "No-dal"
	}
	if c.PermissionMode == "" {
		c.PermissionMode = "default"
	}
	if c.Grants == nil {
		c.Grants = []claudeagent.Grant{}
	}
	return c, nil
}

func (s *Server) caSave(ctx context.Context, clusterID string, c caConfig, by string) error {
	if len(c.Log) > 60 {
		c.Log = c.Log[len(c.Log)-60:]
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.Store.PutClaudeAgentConfig(ctx, clusterID, raw, by)
}

// caUpdate changes the document under the lock.
func (s *Server) caUpdate(ctx context.Context, clusterID, by string, change func(*caConfig) error) (caConfig, error) {
	s.claudeAgent.mu.Lock()
	defer s.claudeAgent.mu.Unlock()
	c, err := s.caLoad(ctx, clusterID)
	if err != nil {
		return c, err
	}
	if err := change(&c); err != nil {
		return c, err
	}
	return c, s.caSave(ctx, clusterID, c, by)
}

func (s *Server) caLog(ctx context.Context, clusterID, format string, args ...any) {
	line := s.now().UTC().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	_, _ = s.caUpdate(ctx, clusterID, caActor, func(c *caConfig) error {
		c.Log = append(c.Log, line)
		return nil
	})
}

func (s *Server) caRequire(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return nil, false
	}
	if !s.caEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusConflict, "turn on Claude Agent in Add Features first")
		return nil, false
	}
	return p, true
}

// ---- status -------------------------------------------------------------

func (s *Server) getClaudeAgent(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	ctx := r.Context()
	c, err := s.caLoad(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{
		"enabled": s.caEnabled(ctx, p.User.ClusterID), "workload_id": c.WorkloadID, "state": c.State, "error": c.Error,
		"log": c.Log, "name": c.Name, "permission_mode": c.PermissionMode, "permission_modes": claudeagent.PermissionModes,
		"grants": c.Grants, "servers": s.caServers(ctx, p.User.ClusterID),
	}
	if c.WorkloadID != "" && c.State == caStateReady {
		if rpc, ok := s.caRPC(); ok {
			var st claudeagent.Status
			sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if err := rpc.ClaudeAgent(sctx, "status", claudeagent.Request{WorkloadID: c.WorkloadID}, &st); err != nil {
				out["status_error"] = err.Error()
			} else {
				out["status"] = st
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// caServers lists the game servers the operator can grant.
func (s *Server) caServers(ctx context.Context, clusterID string) []map[string]any {
	rows, _ := s.Store.ListGameServers(ctx, clusterID)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "name": row.Name, "game": row.Game, "family": row.Family,
			"default_folders": claudeagent.DefaultFolders(row.Game), "slug": claudeagent.Slug(row.Name, row.ID),
		})
	}
	return out
}

// ---- setup --------------------------------------------------------------

func (s *Server) setupClaudeAgent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caRequire(w, r)
	if !ok {
		return
	}
	var req struct {
		Name           string `json:"name"`
		PermissionMode string `json:"permission_mode"`
	}
	_ = readJSON(r, &req)
	ctx := r.Context()
	clusterID := p.User.ClusterID
	if _, ok := s.caRPC(); !ok {
		writeErr(w, http.StatusBadGateway, "this node's agent cannot run the Claude Agent; update No-dal")
		return
	}
	s.claudeAgent.mu.Lock()
	busy := s.claudeAgent.setup
	s.claudeAgent.mu.Unlock()
	if busy {
		writeErr(w, http.StatusConflict, "setup is already running")
		return
	}
	c, err := s.caUpdate(ctx, clusterID, p.User.ID, func(c *caConfig) error {
		if req.Name != "" {
			if err := claudeagent.ValidName(req.Name); err != nil {
				return errBadRequest(err.Error())
			}
			c.Name = req.Name
		}
		if req.PermissionMode != "" {
			if err := claudeagent.ValidMode(req.PermissionMode); err != nil {
				return errBadRequest(err.Error())
			}
			c.PermissionMode = req.PermissionMode
		}
		c.State, c.Error, c.Log = caStateCreating, "", nil
		return nil
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.claudeAgent.mu.Lock()
	s.claudeAgent.setup = true
	s.claudeAgent.mu.Unlock()
	s.audit(r, clusterID, p.User.ID, "claude_agent.setup", "ok", c.WorkloadID)
	go func() {
		defer func() {
			s.claudeAgent.mu.Lock()
			s.claudeAgent.setup = false
			s.claudeAgent.mu.Unlock()
		}()
		bg, cancel := context.WithTimeout(context.Background(), caSetupTimeout)
		defer cancel()
		if err := s.caProvision(bg, clusterID); err != nil {
			s.caLog(bg, clusterID, "setup failed: %v", err)
			_, _ = s.caUpdate(bg, clusterID, caActor, func(c *caConfig) error {
				c.State, c.Error = caStateFailed, err.Error()
				return nil
			})
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"state": caStateCreating})
}

// caProvision creates the container if needed, waits for it, installs
// Claude Code and writes the settings and grants.
func (s *Server) caProvision(ctx context.Context, clusterID string) error {
	c, err := s.caLoad(ctx, clusterID)
	if err != nil {
		return err
	}
	rpc, _ := s.caRPC()
	if c.WorkloadID != "" {
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, c.WorkloadID); wl == nil {
			s.caLog(ctx, clusterID, "the previous container is gone; creating a new one")
			c.WorkloadID, c.Applied = "", nil
		}
	}
	if c.WorkloadID == "" {
		poolID, netID := s.chPlacement(ctx, clusterID, chSettings{})
		if poolID == "" || netID == "" {
			return errors.New("no usable storage pool or network for the agent's container")
		}
		s.caLog(ctx, clusterID, "creating container %s (%d vCPU, %d MB, %d GB)", caWorkloadName, caDefaultCPUs, caDefaultMemMB, caDefaultDisk)
		code, body := s.systemCallAs(ctx, clusterID, caActor, http.MethodPost, "/api/v1/workloads", map[string]any{
			"name": caWorkloadName, "kind": "system-container", "image_pin": chDefaultImage,
			"cpus": caDefaultCPUs, "memory_bytes": int64(caDefaultMemMB) << 20, "disk_bytes": int64(caDefaultDisk) << 30,
			"pool_id": poolID, "network_id": netID, "desired_power": "running", "autostart": true,
		})
		if code >= 300 {
			return fmt.Errorf("creating the container failed: %s", apiErrText(code, body))
		}
		var wl struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &wl); err != nil || wl.ID == "" {
			return errors.New("creating the container returned no workload")
		}
		if _, err := s.caUpdate(ctx, clusterID, caActor, func(cc *caConfig) error {
			cc.WorkloadID, cc.Applied = wl.ID, nil
			return nil
		}); err != nil {
			return err
		}
		c.WorkloadID = wl.ID
	}
	s.caLog(ctx, clusterID, "waiting for the container to start")
	if err := s.caWaitRunning(ctx, clusterID, c.WorkloadID); err != nil {
		return err
	}
	_, _ = s.caUpdate(ctx, clusterID, caActor, func(cc *caConfig) error {
		cc.State = caStateInstalling
		return nil
	})
	s.caLog(ctx, clusterID, "installing Claude Code (this takes a few minutes)")
	req, err := s.caRequest(ctx, clusterID, c.WorkloadID)
	if err != nil {
		return err
	}
	var st claudeagent.Status
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if last = rpc.ClaudeAgent(ctx, "setup", req, &st); last == nil {
			break
		}
		s.caLog(ctx, clusterID, "install attempt %d failed: %v", attempt, last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Second):
		}
	}
	if last != nil {
		return last
	}
	s.caLog(ctx, clusterID, "Claude Code %s installed", firstNonEmpty(st.Version, ""))
	_, err = s.caUpdate(ctx, clusterID, caActor, func(cc *caConfig) error {
		cc.State, cc.Error = caStateReady, ""
		return nil
	})
	if err != nil {
		return err
	}
	// Grants saved before setup finished take effect now.
	if len(c.Grants) > 0 {
		s.caLog(ctx, clusterID, "applying access to %d server(s)", len(c.Grants))
		if _, err := s.caApplyGrants(ctx, clusterID, caActor, c.Grants); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) caWaitRunning(ctx context.Context, clusterID, id string) error {
	deadline := time.Now().Add(10 * time.Minute)
	rpc, _ := s.caRPC()
	for {
		s.refreshWorkloads(ctx, clusterID)
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, id); wl != nil && strings.EqualFold(wl.Status, lxc.StatusRunning) {
			var st claudeagent.Status
			// The container answers commands once it is up.
			if rpc.ClaudeAgent(ctx, "status", claudeagent.Request{WorkloadID: id}, &st) == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("the container did not start in 10 minutes; check it under Compute")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(caPollEvery):
		}
	}
}

// caRequest is the setup and configure request: settings, a fresh API
// token for ndl, and the grants for CLAUDE.md. The token is rotated on
// every call; only its hash is kept.
func (s *Server) caRequest(ctx context.Context, clusterID, workloadID string) (claudeagent.Request, error) {
	token, err := secutil.RandomHex(24)
	if err != nil {
		return claudeagent.Request{}, err
	}
	token = caTokenPrefix + token
	c, err := s.caUpdate(ctx, clusterID, caActor, func(c *caConfig) error {
		c.TokenHash = secutil.HashSHA256(token)
		return nil
	})
	if err != nil {
		return claudeagent.Request{}, err
	}
	return claudeagent.Request{
		WorkloadID: workloadID, Name: c.Name, PermissionMode: c.PermissionMode,
		APIURL: s.caAPIURL(ctx, clusterID, workloadID), APIToken: token, Grants: c.Grants,
	}, nil
}

// caAPIURL is where the container reaches this control plane: this host's
// address on the container's bridge.
func (s *Server) caAPIURL(ctx context.Context, clusterID, workloadID string) string {
	host := ""
	if wl, _ := s.Store.GetWorkload(ctx, clusterID, workloadID); wl != nil {
		nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
		for _, n := range nics {
			if netw, _ := s.Store.GetNetwork(ctx, clusterID, n.NetworkID); netw != nil && netw.BridgeName != "" {
				if ip := bridgeIPv4(netw.BridgeName); ip != "" {
					host = ip
					break
				}
			}
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if s.TLSServing {
		return "https://" + net.JoinHostPort(host, listenPort(s.TLSListen, "443"))
	}
	return "http://" + net.JoinHostPort(host, listenPort(s.Listen, "8080"))
}

func bridgeIPv4(name string) string {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := ifi.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

func listenPort(addr, fallback string) string {
	if _, port, err := net.SplitHostPort(strings.TrimSpace(addr)); err == nil && port != "" {
		return port
	}
	return fallback
}

// ---- sign-in, settings, restart -----------------------------------------

func (s *Server) caReady(w http.ResponseWriter, r *http.Request, clusterID string) (caConfig, claudeAgentRPC, bool) {
	c, err := s.caLoad(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return c, nil, false
	}
	if c.WorkloadID == "" || c.State != caStateReady {
		writeErr(w, http.StatusConflict, "set up the agent first")
		return c, nil, false
	}
	rpc, ok := s.caRPC()
	if !ok {
		writeErr(w, http.StatusBadGateway, "this node's agent cannot run the Claude Agent; update No-dal")
		return c, nil, false
	}
	return c, rpc, true
}

func (s *Server) claudeAgentLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caRequire(w, r)
	if !ok {
		return
	}
	c, rpc, ok := s.caReady(w, r, p.User.ClusterID)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Code   string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var st claudeagent.Status
	var err error
	switch req.Action {
	case "start":
		err = rpc.ClaudeAgent(r.Context(), "login-start", claudeagent.Request{WorkloadID: c.WorkloadID}, &st)
	case "code":
		if verr := claudeagent.ValidCode(strings.TrimSpace(req.Code)); verr != nil {
			writeErr(w, http.StatusBadRequest, verr.Error())
			return
		}
		err = rpc.ClaudeAgent(r.Context(), "login-code", claudeagent.Request{WorkloadID: c.WorkloadID, Code: req.Code}, &st)
		if err == nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "claude_agent.login", "ok", st.Account)
		}
	case "logout":
		err = rpc.ClaudeAgent(r.Context(), "logout", claudeagent.Request{WorkloadID: c.WorkloadID}, &st)
		if err == nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "claude_agent.logout", "ok", "")
		}
	default:
		writeErr(w, http.StatusBadRequest, "action must be start, code or logout")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) putClaudeAgentSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caRequire(w, r)
	if !ok {
		return
	}
	var req struct {
		Name           string `json:"name"`
		PermissionMode string `json:"permission_mode"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := claudeagent.ValidName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := claudeagent.ValidMode(req.PermissionMode); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.caUpdate(r.Context(), p.User.ClusterID, p.User.ID, func(c *caConfig) error {
		c.Name, c.PermissionMode = req.Name, req.PermissionMode
		return nil
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "claude_agent.settings", "ok", req.Name+" "+req.PermissionMode)
	if c.WorkloadID != "" && c.State == caStateReady {
		rpc, _ := s.caRPC()
		creq, err := s.caRequest(r.Context(), p.User.ClusterID, c.WorkloadID)
		if err == nil {
			err = rpc.ClaudeAgent(r.Context(), "configure", creq, nil)
		}
		if err == nil {
			err = rpc.ClaudeAgent(r.Context(), "restart", claudeagent.Request{WorkloadID: c.WorkloadID}, nil)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": c.Name, "permission_mode": c.PermissionMode})
}

func (s *Server) restartClaudeAgent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caRequire(w, r)
	if !ok {
		return
	}
	c, rpc, ok := s.caReady(w, r, p.User.ClusterID)
	if !ok {
		return
	}
	var st claudeagent.Status
	if err := rpc.ClaudeAgent(r.Context(), "restart", claudeagent.Request{WorkloadID: c.WorkloadID}, &st); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "claude_agent.restart", "ok", "")
	writeJSON(w, http.StatusOK, st)
}

// ---- grants -------------------------------------------------------------

func (s *Server) putClaudeAgentGrants(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caRequire(w, r)
	if !ok {
		return
	}
	var req struct {
		Grants []claudeagent.Grant `json:"grants"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	for i, g := range req.Grants {
		row, _ := s.Store.GetGameServer(ctx, clusterID, g.ID)
		if row == nil {
			writeErr(w, http.StatusBadRequest, "game server "+g.ID+" was not found")
			return
		}
		req.Grants[i].Kind, req.Grants[i].Name = claudeagent.KindGameServer, row.Name
	}
	grants, err := claudeagent.NormalizeGrants(req.Grants)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.caLoad(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c.WorkloadID == "" || c.State != caStateReady {
		// Saved now, applied when setup finishes.
		if _, err := s.caUpdate(ctx, clusterID, p.User.ID, func(cc *caConfig) error {
			cc.Grants = grants
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": grants, "applied": false})
		return
	}
	restarted, err := s.caApplyGrants(ctx, clusterID, p.User.ID, grants)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "claude_agent.grants", "ok", fmt.Sprintf("%d server(s)", len(grants)))
	writeJSON(w, http.StatusOK, map[string]any{"grants": grants, "applied": true, "restarted": restarted})
}

// caApplyGrants takes access away from folders no longer granted, gives
// access to the granted ones, sets the container's mounts (restarting it
// when they changed) and rewrites CLAUDE.md. It reports whether the
// container restarted.
func (s *Server) caApplyGrants(ctx context.Context, clusterID, by string, grants []claudeagent.Grant) (bool, error) {
	c, err := s.caLoad(ctx, clusterID)
	if err != nil {
		return false, err
	}
	rpc, ok := s.caRPC()
	if !ok {
		return false, errUnavailable("this node's agent cannot run the Claude Agent; update No-dal")
	}
	key := func(serverID, path string) string { return serverID + "\x00" + path }
	want := map[string]claudeagent.Folder{}
	var acls []claudeagent.HostACL
	targets := map[string]string{}
	for _, g := range grants {
		for _, f := range g.Folders {
			want[key(g.ID, f.Path)] = f
			acls = append(acls, claudeagent.HostACL{ServerID: g.ID, Path: f.Path, Write: f.Write})
			targets[key(g.ID, f.Path)] = claudeagent.Target(g, f)
		}
	}
	var revoke []claudeagent.HostACL
	for _, a := range c.Applied {
		if f, ok := want[key(a.ServerID, a.Path)]; !ok || f.Write != a.Write {
			revoke = append(revoke, claudeagent.HostACL{ServerID: a.ServerID, Path: a.Path, Write: a.Write})
		}
	}
	if len(revoke) > 0 {
		if err := rpc.ClaudeAgent(ctx, "revoke", claudeagent.Request{WorkloadID: c.WorkloadID, ACLs: revoke}, nil); err != nil {
			return false, errUnprocessable(err.Error())
		}
	}
	var granted claudeagent.GrantResult
	if len(acls) > 0 {
		if err := rpc.ClaudeAgent(ctx, "grant", claudeagent.Request{WorkloadID: c.WorkloadID, ACLs: acls}, &granted); err != nil {
			return false, errUnprocessable(err.Error())
		}
	}
	applied := make([]caApplied, 0, len(granted.Mounts))
	mounts := make([]lxc.Mount, 0, len(granted.Mounts))
	for _, m := range granted.Mounts {
		target := targets[key(m.ServerID, m.Path)]
		applied = append(applied, caApplied{ServerID: m.ServerID, Path: m.Path, Write: m.Write, Source: m.Source, Target: target})
		mounts = append(mounts, lxc.Mount{Source: m.Source, Target: target, ReadOnly: !m.Write, Label: "Claude Agent"})
	}
	if len(mounts) > lxc.MaxMounts {
		return false, errBadRequest(fmt.Sprintf("at most %d folders can be granted", lxc.MaxMounts))
	}
	res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: c.WorkloadID, Action: lxc.ActionMountsSet, Mounts: mounts})
	if err != nil {
		return false, errUnprocessable(err.Error())
	}
	if _, err := s.caUpdate(ctx, clusterID, by, func(cc *caConfig) error {
		cc.Grants, cc.Applied = grants, applied
		return nil
	}); err != nil {
		return false, err
	}
	restarted := false
	if res.RestartRequired {
		if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: c.WorkloadID, Action: "restart"}); err != nil {
			return false, errUnprocessable("the folders changed but restarting the agent's container failed: " + err.Error())
		}
		restarted = true
		if err := s.caWaitRunning(ctx, clusterID, c.WorkloadID); err != nil {
			return true, err
		}
	}
	req, err := s.caRequest(ctx, clusterID, c.WorkloadID)
	if err != nil {
		return restarted, err
	}
	if err := rpc.ClaudeAgent(ctx, "configure", req, nil); err != nil {
		return restarted, errUnprocessable(err.Error())
	}
	return restarted, nil
}

// ---- remove -------------------------------------------------------------

func (s *Server) removeClaudeAgent(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != caRemoveConfirm {
		writeErr(w, http.StatusPreconditionRequired, "removing the agent deletes its container and Claude sign-in; send X-Nodal-Confirm: "+caRemoveConfirm)
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	c, err := s.caLoad(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c.WorkloadID != "" {
		if rpc, ok := s.caRPC(); ok && len(c.Applied) > 0 {
			acls := make([]claudeagent.HostACL, 0, len(c.Applied))
			for _, a := range c.Applied {
				acls = append(acls, claudeagent.HostACL{ServerID: a.ServerID, Path: a.Path, Write: a.Write})
			}
			if err := rpc.ClaudeAgent(ctx, "revoke", claudeagent.Request{WorkloadID: c.WorkloadID, ACLs: acls}, nil); err != nil {
				writeErr(w, http.StatusBadGateway, "taking the agent's access away failed, so its container was kept: "+err.Error())
				return
			}
		}
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, c.WorkloadID); wl != nil {
			code, body := s.systemCallAs(ctx, clusterID, caActor, http.MethodPost, "/api/v1/workloads/"+c.WorkloadID+"/delete", nil)
			if code >= 300 {
				writeErr(w, http.StatusBadGateway, "deleting the agent's container failed: "+apiErrText(code, body))
				return
			}
		}
	}
	if _, err := s.caUpdate(ctx, clusterID, p.User.ID, func(cc *caConfig) error {
		cc.WorkloadID, cc.State, cc.Error, cc.TokenHash, cc.Applied, cc.Log = "", "", "", "", nil, nil
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "claude_agent.remove", "ok", c.WorkloadID)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// caHasContainer is checked before the feature is turned off.
func (s *Server) caHasContainer(ctx context.Context, clusterID string) bool {
	c, err := s.caLoad(ctx, clusterID)
	return err == nil && c.WorkloadID != ""
}

// ---- the agent's own API (ndl) -------------------------------------------

// caAgentAuth checks the bearer token ndl sends and returns the config and
// the grant for the server named in the path (slug or id).
func (s *Server) caAgentAuth(w http.ResponseWriter, r *http.Request) (string, caConfig, bool) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !strings.HasPrefix(token, caTokenPrefix) {
		writeErr(w, http.StatusUnauthorized, "agent token required")
		return "", caConfig{}, false
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusUnauthorized, "agent token is not valid")
		return "", caConfig{}, false
	}
	c, err := s.caLoad(r.Context(), cluster.ID)
	if err != nil || c.TokenHash == "" || subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(secutil.HashSHA256(token))) != 1 || !s.caEnabled(r.Context(), cluster.ID) {
		writeErr(w, http.StatusUnauthorized, "agent token is not valid")
		return "", caConfig{}, false
	}
	return cluster.ID, c, true
}

func (s *Server) caAgentGrant(w http.ResponseWriter, r *http.Request) (string, claudeagent.Grant, bool) {
	clusterID, c, ok := s.caAgentAuth(w, r)
	if !ok {
		return "", claudeagent.Grant{}, false
	}
	name := r.PathValue("server")
	for _, g := range c.Grants {
		if g.Slug == name || g.ID == name {
			return clusterID, g, true
		}
	}
	writeErr(w, http.StatusNotFound, "no server "+name+" is shared with this agent; run ndl servers")
	return "", claudeagent.Grant{}, false
}

func (s *Server) caAgentServers(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.caAgentAuth(w, r)
	if !ok {
		return
	}
	items := make([]map[string]any, 0, len(c.Grants))
	for _, g := range c.Grants {
		folders := make([]map[string]any, 0, len(g.Folders))
		for _, f := range g.Folders {
			folders = append(folders, map[string]any{"path": claudeagent.Target(g, f), "server_path": f.Path, "write": f.Write})
		}
		items = append(items, map[string]any{"server": g.Slug, "name": g.Name, "folders": folders, "log": g.Log, "console": g.Console, "restart": g.Restart})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": items})
}

func (s *Server) caAgentLog(w http.ResponseWriter, r *http.Request) {
	_, g, ok := s.caAgentGrant(w, r)
	if !ok {
		return
	}
	if !g.Log {
		writeErr(w, http.StatusForbidden, "reading the log of "+g.Slug+" is not granted")
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	log, err := s.gameHost().Logs(r.Context(), g.ID, lines)
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"log": log})
}

func (s *Server) caAgentConsole(w http.ResponseWriter, r *http.Request) {
	clusterID, g, ok := s.caAgentGrant(w, r)
	if !ok {
		return
	}
	if !g.Console {
		writeErr(w, http.StatusForbidden, "console commands on "+g.Slug+" are not granted")
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Command) == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if len(cmd) > 4000 || strings.ContainsAny(cmd, "\r\n\x00") {
		writeErr(w, http.StatusBadRequest, "send one console line at a time")
		return
	}
	if err := s.gameHost().Send(r.Context(), g.ID, cmd); err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	_ = s.Store.AppendGameConsoleHist(r.Context(), g.ID, cmd)
	s.audit(r, clusterID, caActor, "claude_agent.console", "ok", g.Slug+": "+clipGame(cmd, 120))
	writeJSON(w, http.StatusOK, map[string]any{"message": "sent to " + g.Slug + "; read the result with ndl log " + g.Slug})
}

func (s *Server) caAgentRestart(w http.ResponseWriter, r *http.Request) {
	clusterID, g, ok := s.caAgentGrant(w, r)
	if !ok {
		return
	}
	if !g.Restart {
		writeErr(w, http.StatusForbidden, "restarting "+g.Slug+" is not granted")
		return
	}
	code, body := s.systemCallAs(r.Context(), clusterID, caActor, http.MethodPost, "/api/v1/game-servers/"+g.ID+"/restart", nil)
	if code >= 300 {
		writeErr(w, http.StatusBadGateway, apiErrText(code, body))
		return
	}
	s.audit(r, clusterID, caActor, "claude_agent.restart_server", "ok", g.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"message": g.Slug + " is restarting"})
}
