package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// DockerRPC is the privileged agent surface for Docker Management.
type DockerRPC interface {
	DockerSnapshot(ctx context.Context, hints []docker.MachineHint) (docker.Inventory, error)
	DockerAction(ctx context.Context, req docker.ActionRequest) (docker.ActionResult, error)
	DockerIdle(ctx context.Context) error
}

type dockerUnavailable struct{}

func (dockerUnavailable) DockerSnapshot(context.Context, []docker.MachineHint) (docker.Inventory, error) {
	return docker.Inventory{}, errUnavailable("docker agent is unavailable")
}
func (dockerUnavailable) DockerAction(context.Context, docker.ActionRequest) (docker.ActionResult, error) {
	return docker.ActionResult{}, errUnavailable("docker agent is unavailable")
}
func (dockerUnavailable) DockerIdle(context.Context) error {
	return errUnavailable("docker agent is unavailable")
}

func AdaptDocker(client any) DockerRPC {
	if v, ok := client.(DockerRPC); ok {
		return v
	}
	return dockerUnavailable{}
}

func (s *Server) dockerRPC() DockerRPC {
	if s.Docker != nil {
		return s.Docker
	}
	return AdaptDocker(s.Agent)
}

type dockerCache struct {
	mu      sync.Mutex
	inv     docker.Inventory
	at      time.Time
	enabled bool
}

func (s *Server) dockerEnabled(ctx context.Context, clusterID string) bool {
	mod, ok := features.Lookup(features.IDDocker)
	if !ok {
		return false
	}
	stored, _ := s.Store.GetFeature(ctx, clusterID, mod.ID)
	if stored != nil {
		return stored.Enabled
	}
	return false
}

func (s *Server) requireDocker(w http.ResponseWriter, r *http.Request, perm string) (*principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, false
	}
	if !s.dockerEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusNotFound, "Docker Management is not enabled. Enable it from Add Features.")
		return nil, false
	}
	return p, true
}

func (s *Server) dockerHints(ctx context.Context, clusterID string) []docker.MachineHint {
	wls, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return nil
	}
	hints := make([]docker.MachineHint, 0, len(wls))
	for _, w := range wls {
		if w.Kind != lxc.KindSystemContainer {
			continue
		}
		hints = append(hints, docker.MachineHint{ID: w.ID, Name: w.Name, Kind: lxc.KindSystemContainer})
	}
	return hints
}

func (s *Server) dockerInventory(ctx context.Context, clusterID string, refresh bool) (docker.Inventory, error) {
	if s.docker == nil {
		s.docker = &dockerCache{}
	}
	s.docker.mu.Lock()
	fresh := !refresh && s.docker.enabled && !s.docker.at.IsZero() && time.Since(s.docker.at) < 3*time.Second
	cached := s.docker.inv
	s.docker.mu.Unlock()
	if fresh {
		return cached, nil
	}
	inv, err := s.dockerRPC().DockerSnapshot(ctx, s.dockerHints(ctx, clusterID))
	if err != nil {
		return docker.Inventory{}, err
	}
	s.docker.mu.Lock()
	s.docker.inv = inv
	s.docker.at = s.now()
	s.docker.enabled = true
	s.docker.mu.Unlock()
	return inv, nil
}

func (s *Server) dockerIdle(ctx context.Context) {
	if s.docker != nil {
		s.docker.mu.Lock()
		s.docker.inv = docker.Inventory{}
		s.docker.at = time.Time{}
		s.docker.enabled = false
		s.docker.mu.Unlock()
	}
	_ = s.dockerRPC().DockerIdle(ctx)
}

func (s *Server) getDocker(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	inv, err := s.dockerInventory(r.Context(), p.User.ClusterID, refresh)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) getDockerContainer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	inv, err := s.dockerInventory(r.Context(), p.User.ClusterID, false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	c := findDockerContainer(inv, r.PathValue("machine_id"), r.PathValue("container_id"))
	if c == nil {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) dockerContainerLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	res, err := s.dockerRPC().DockerAction(r.Context(), docker.ActionRequest{
		Action: "logs", MachineID: r.PathValue("machine_id"), ContainerID: r.PathValue("container_id"), Tail: tail,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.logs", "ok", r.PathValue("container_id"))
	writeJSON(w, http.StatusOK, map[string]any{"logs": res.Logs})
}

func (s *Server) dockerContainerAction(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	_ = readJSON(r, &req)
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = strings.ToLower(strings.TrimSpace(r.PathValue("action")))
	}
	switch action {
	case "start", "stop", "restart", "pull", "recreate", "update":
	default:
		writeErr(w, http.StatusBadRequest, "unknown docker action")
		return
	}
	need := rbac.ComputeLifecycle
	if action == "start" {
		need = rbac.ComputeStart
	}
	if action == "stop" {
		need = rbac.ComputeStop
	}
	if !rbac.Authorize(p.Grants, need) && !rbac.Authorize(p.Grants, rbac.ComputeLifecycle) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	res, err := s.dockerRPC().DockerAction(r.Context(), docker.ActionRequest{
		Action: action, MachineID: r.PathValue("machine_id"), ContainerID: r.PathValue("container_id"),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker."+action, "ok", r.PathValue("container_id"))
	_, _ = s.dockerInventory(r.Context(), p.User.ClusterID, true)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) createDockerTerminal(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.TerminalOpen)
	if !ok {
		return
	}
	machine := strings.TrimSpace(r.PathValue("machine_id"))
	container := strings.TrimSpace(r.PathValue("container_id"))
	if machine == "" || container == "" {
		writeErr(w, http.StatusBadRequest, "machine and container are required")
		return
	}
	s.createTerminal(w, r, p, appdb.IOTargetDocker, docker.Ref(machine, container), "/", "")
}

func findDockerContainer(inv docker.Inventory, machineID, containerID string) *docker.Container {
	want := docker.Ref(machineID, containerID)
	for i := range inv.Containers {
		c := &inv.Containers[i]
		if c.Ref == want || (c.MachineID == machineID && (c.EngineID == containerID || strings.HasPrefix(c.EngineID, containerID))) {
			return c
		}
	}
	return nil
}
