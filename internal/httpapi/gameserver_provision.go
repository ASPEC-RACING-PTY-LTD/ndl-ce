package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"syscall"

	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// gamePlanNode is the placement target as seen by preflight.
type gamePlanNode struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Architecture     string `json:"architecture,omitempty"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes,omitempty"`
	MemoryCommitted  int64  `json:"memory_committed_bytes"`
	CPUs             int    `json:"cpus,omitempty"`
	DiskFreeBytes    uint64 `json:"disk_free_bytes,omitempty"`
}

// gamePlan is the single source of truth for what create will do. Preflight
// returns it; create refuses when Errors is non-empty.
type gamePlan struct {
	OK              bool                     `json:"ok"`
	Errors          []string                 `json:"errors"`
	Warnings        []string                 `json:"warnings"`
	Ports           []gameserver.Port        `json:"ports"`
	EnvUpdates      map[string]string        `json:"env_updates"`
	Image           string                   `json:"image"`
	ImageLabel      string                   `json:"image_label,omitempty"`
	Dependencies    []string                 `json:"dependencies,omitempty"`
	Node            *gamePlanNode            `json:"node"`
	Requirements    []gameserver.Requirement `json:"requirements,omitempty"`
	InstallMethod   string                   `json:"install_method"`
	UpdateProcedure string                   `json:"update_procedure"`

	env    map[string]string
	nodeID string
	cpus   int
	mem    int64
	disk   int64
}

func normalizeArch(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "aarch64", "arm64", "armv8":
		return "arm64"
	}
	return strings.ToLower(strings.TrimSpace(a))
}

// planGameServer validates a create request against the template, the
// target node and the servers already placed there, and allocates ports.
func (s *Server) planGameServer(ctx context.Context, clusterID string, tmpl gameserver.Template, req createGameReq) *gamePlan {
	plan := &gamePlan{
		Errors: []string{}, Warnings: []string{}, EnvUpdates: map[string]string{},
		Requirements: tmpl.Requirements, InstallMethod: tmpl.InstallMethod(), UpdateProcedure: tmpl.UpdateProcedure(),
		Dependencies: tmpl.Dependencies,
	}
	fail := func(format string, args ...any) { plan.Errors = append(plan.Errors, fmt.Sprintf(format, args...)) }
	warn := func(format string, args ...any) { plan.Warnings = append(plan.Warnings, fmt.Sprintf(format, args...)) }

	// Environment: defaults, generated secrets, operator values.
	env := gameserver.MergeEnv(tmpl, req.Env)
	// Credentials the operator must supply are reported once, by the
	// requirement check below, with the reason they are needed.
	envForShape := map[string]string{}
	for k, v := range env {
		envForShape[k] = v
	}
	for _, r := range tmpl.RequirementsAt(gameserver.StageInstall) {
		if r.Env != "" && strings.TrimSpace(envForShape[r.Env]) == "" {
			envForShape[r.Env] = "pending"
		}
	}
	if err := gameserver.ValidateEnv(tmpl, envForShape); err != nil {
		fail("%s", err.Error())
	}
	for _, v := range tmpl.Variables {
		if v.FieldType == "select" && len(v.Options) > 0 && env[v.Env] != "" {
			ok := false
			for _, o := range v.Options {
				if o == env[v.Env] {
					ok = true
				}
			}
			if !ok {
				fail("%s must be one of %s", v.Name, strings.Join(v.Options, ", "))
			}
		}
	}
	if err := gameserver.CheckInstallRequirements(tmpl, env); err != nil {
		fail("%s", err.Error())
	}
	if err := gameserver.CheckStartRequirements(tmpl, env); err != nil {
		warn("The server can install, but it will not start until this is resolved: %s", err.Error())
	}
	for _, r := range tmpl.RequirementsAt(gameserver.StageOptional) {
		if r.Env != "" && strings.TrimSpace(env[r.Env]) == "" {
			warn("Optional: %s", r.Label)
		}
	}

	// Runtime image.
	label := strings.TrimSpace(req.ImageLabel)
	if label != "" {
		if _, ok := tmpl.Images[label]; !ok {
			fail("runtime %q is not offered by %s", label, tmpl.Name)
		}
	}
	plan.Image = tmpl.ImageFor(label)
	plan.ImageLabel = label
	if plan.Image == "" {
		fail("%s has no runtime image", tmpl.Name)
	}

	// Resources: keep what the operator asked for, fall back to template.
	plan.cpus = req.CPUs
	if plan.cpus <= 0 {
		plan.cpus = tmpl.DefaultCPUs
	}
	plan.mem = req.MemoryBytes
	if plan.mem <= 0 {
		plan.mem = int64(tmpl.DefaultMemoryMB) << 20
	}
	plan.disk = req.DiskBytes
	if plan.disk <= 0 {
		plan.disk = int64(tmpl.DefaultDiskMB) << 20
	}
	memMB := int(plan.mem >> 20)
	if tmpl.MinMemoryMB > 0 && memMB < tmpl.MinMemoryMB {
		warn("%d MiB RAM is below the %d MiB minimum for %s; expect crashes or out-of-memory kills.", memMB, tmpl.MinMemoryMB, tmpl.Name)
	} else if tmpl.DefaultMemoryMB > 0 && memMB < tmpl.DefaultMemoryMB {
		warn("%d MiB RAM is below the recommended %d MiB.", memMB, tmpl.DefaultMemoryMB)
	}
	if heap, err := parsePositiveInt(env["SERVER_MEMORY"]); err == nil && heap > memMB {
		fail("Java heap (SERVER_MEMORY %d MiB) is larger than the RAM limit (%d MiB).", heap, memMB)
	}

	// Placement. Game server containers run through the control node's
	// container runtime, so another node cannot be targeted yet.
	control, _ := s.Store.GetNode(ctx, clusterID)
	nodeID := strings.TrimSpace(req.NodeID)
	if control != nil {
		if nodeID == "" {
			nodeID = control.ID
		}
		if nodeID != control.ID {
			name := nodeID
			if nodes, err := s.Store.ListClusterNodes(ctx, clusterID); err == nil {
				for _, n := range nodes {
					if n.ID == nodeID {
						name = firstNonEmptyStr(n.Name, n.ID)
					}
				}
			}
			fail("Game servers run on the control node (%s) today. Placement on %s is not supported yet.", firstNonEmptyStr(control.Name, control.ID), name)
		}
		node := &gamePlanNode{ID: control.ID, Name: control.Name}
		var hp struct {
			Architecture string `json:"architecture"`
		}
		if len(control.HostPlatform) > 0 && json.Unmarshal(control.HostPlatform, &hp) == nil {
			node.Architecture = normalizeArch(hp.Architecture)
		}
		if inv, _ := s.Store.GetInventory(ctx, control.ID); inv != nil {
			if parsed, ok := decodeInv(inv); ok {
				node.MemoryTotalBytes = parsed.Memory.TotalBytes
				node.CPUs = parsed.CPU.Threads
				if node.Architecture == "" {
					node.Architecture = normalizeArch(firstNonEmptyStr(parsed.Host.Architecture, parsed.CPU.Architecture))
				}
			}
		}
		plan.Node = node
	}
	plan.nodeID = nodeID

	if plan.Node != nil && plan.Node.Architecture != "" {
		supported := false
		for _, a := range tmpl.Arches() {
			if a == plan.Node.Architecture {
				supported = true
			}
		}
		if !supported {
			fail("%s runs on %s only; node %s is %s.", tmpl.Name, strings.Join(tmpl.Arches(), ", "), plan.Node.Name, plan.Node.Architecture)
		}
	}

	// Ports and memory already committed to servers on this node.
	used := map[string]string{}
	var committed int64
	rows, _ := s.Store.ListGameServers(ctx, clusterID)
	for _, row := range rows {
		if row.NodeID != "" && nodeID != "" && row.NodeID != nodeID {
			continue
		}
		committed += row.MemoryBytes
		var ports []gameserver.Port
		_ = json.Unmarshal(row.PortsJSON, &ports)
		for _, p := range ports {
			used[gameserver.PortKey(gameserver.HostPortOf(p), p.Protocol)] = row.Name
		}
	}
	if plan.Node != nil {
		plan.Node.MemoryCommitted = committed
		if total := plan.Node.MemoryTotalBytes; total > 0 {
			if uint64(plan.mem) > total {
				fail("%d MiB RAM is more than node %s has (%d MiB).", memMB, plan.Node.Name, total>>20)
			} else if uint64(plan.mem+committed) > total {
				warn("Game servers on %s would reserve %d MiB of %d MiB RAM; running them all at once will overcommit memory.", plan.Node.Name, (plan.mem+committed)>>20, total>>20)
			}
		}
		if plan.Node.CPUs > 0 && plan.cpus > plan.Node.CPUs {
			fail("%d CPUs requested but node %s has %d.", plan.cpus, plan.Node.Name, plan.Node.CPUs)
		}
		if free := diskFree(s.gameRuntime().Root); free > 0 {
			plan.Node.DiskFreeBytes = free
			if uint64(plan.disk) > free {
				warn("Only %d MiB disk is free on the game server volume; the install needs about %d MiB.", free>>20, plan.disk>>20)
			}
		}
	}

	rt := s.gameRuntime()
	hostNet := rt.NetworkMode == "host"
	probe := gameserver.PortProbe(gameserver.LocalPortFree)
	if rt.Run != nil {
		probe = nil // tests drive a fake runtime; do not probe the test host
	}
	if len(req.Ports) > 0 {
		if err := gameserver.CheckExplicitPorts(req.Ports, used, probe); err != nil {
			fail("%s", err.Error())
		}
		plan.Ports = req.Ports
	} else {
		alloc, err := gameserver.AllocatePorts(tmpl, used, probe, hostNet)
		if err != nil {
			fail("%s", err.Error())
		} else {
			plan.Ports = alloc.Ports
			plan.Warnings = append(plan.Warnings, alloc.Notes...)
			for k, v := range alloc.Env {
				if env[k] != v {
					plan.EnvUpdates[k] = v
				}
				env[k] = v
			}
		}
	}
	plan.env = env
	plan.OK = len(plan.Errors) == 0
	return plan
}

func parsePositiveInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("not a positive number")
	}
	return n, nil
}

func firstNonEmptyStr(parts ...string) string {
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return ""
}

func diskFree(path string) uint64 {
	var st syscall.Statfs_t
	for p := path; p != "" && p != "/"; {
		if err := syscall.Statfs(p, &st); err == nil {
			return st.Bavail * uint64(st.Bsize)
		}
		idx := strings.LastIndex(p, "/")
		if idx <= 0 {
			break
		}
		p = p[:idx]
	}
	if err := syscall.Statfs("/", &st); err == nil {
		return st.Bavail * uint64(st.Bsize)
	}
	return 0
}

func (s *Server) preflightGameServer(w http.ResponseWriter, r *http.Request) {
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
	plan := s.planGameServer(r.Context(), p.User.ClusterID, tmpl, req)
	writeJSON(w, http.StatusOK, plan)
}

// gameTemplateView is a template plus computed catalogue facts.
type gameTemplateView struct {
	gameserver.Template
	InstallMethod   string                 `json:"install_method"`
	UpdateProcedure string                 `json:"update_procedure"`
	Verification    string                 `json:"verification"`
	LiveTest        *gameserver.LiveResult `json:"live_test,omitempty"`
}

func templateView(t gameserver.Template) gameTemplateView {
	v := gameTemplateView{Template: t, InstallMethod: t.InstallMethod(), UpdateProcedure: t.UpdateProcedure(), Verification: gameserver.VerificationLevel(t)}
	if r, ok := gameserver.LiveVerification(t.ID); ok {
		v.LiveTest = &r
	}
	return v
}
