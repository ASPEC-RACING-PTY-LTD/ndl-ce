package docker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/lxc"
)

const (
	defaultEventLookback  = 15 * time.Minute
	maxEventsPerContainer = 40
)

// Engine discovers Docker sockets and talks to the Engine API.
type Engine struct {
	SkipHostCmds bool
	HostSockets  []string
	LXCPath      string
	LXCInfo      func(ctx context.Context, id string) (pid int, ipv4 string, err error)
	Now          func() time.Time
	ClientFor    func(socket string) engineAPI

	mu      sync.Mutex
	updates map[string]updateNote
	events  map[string][]Event
	since   map[string]time.Time
}

type engineAPI interface {
	ping(ctx context.Context) (string, error)
	listContainers(ctx context.Context) ([]listItem, error)
	inspect(ctx context.Context, id string) (inspectJSON, error)
	stats(ctx context.Context, id string) (statsJSON, error)
	start(ctx context.Context, id string) error
	stop(ctx context.Context, id string) error
	restart(ctx context.Context, id string) error
	logs(ctx context.Context, id string, tail int) (string, error)
	pull(ctx context.Context, image string) error
	eventsSince(ctx context.Context, since, until time.Time) ([]dockerEvent, error)
	recreate(ctx context.Context, id string) error
}

type updateNote struct {
	Failed bool
	Error  string
	At     time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) client(socket string) engineAPI {
	if e.ClientFor != nil {
		return e.ClientFor(socket)
	}
	return newUnixClient(socket)
}

func (e *Engine) hostSockets() []string {
	if len(e.HostSockets) > 0 {
		return e.HostSockets
	}
	return []string{"/var/run/docker.sock", "/run/docker.sock"}
}

func (e *Engine) lxcPath() string {
	if e.LXCPath != "" {
		return e.LXCPath
	}
	return "/var/lib/ndl/runtime/lxc"
}

func (e *Engine) lookupLXC(ctx context.Context, id string) (int, string, error) {
	if e.LXCInfo != nil {
		return e.LXCInfo(ctx, id)
	}
	if e.SkipHostCmds {
		return 0, "", nil
	}
	cmd := exec.CommandContext(ctx, lxc.BinLXCInfo, "-P", e.lxcPath(), "-n", id)
	out, err := cmd.Output()
	if err != nil {
		return 0, "", err
	}
	var pid int
	var ipv4 string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		fields := strings.Fields(line)
		if strings.HasPrefix(lower, "pid:") && len(fields) >= 2 {
			pid, _ = strconv.Atoi(fields[len(fields)-1])
		}
		if strings.HasPrefix(lower, "ip:") && len(fields) >= 2 {
			cand := fields[len(fields)-1]
			if strings.Contains(cand, ".") {
				ipv4 = cand
			}
		}
	}
	return pid, ipv4, nil
}

func guestSockets(pid int) []string {
	if pid <= 0 {
		return nil
	}
	root := "/proc/" + strconv.Itoa(pid) + "/root"
	return []string{root + "/run/docker.sock", root + "/var/run/docker.sock"}
}

func firstExisting(paths []string) string {
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.Mode()&os.ModeSocket != 0 || st.Mode()&os.ModeNamedPipe != 0 {
			return p
		}
		// Some overlay/proc views report the socket as a regular file.
		conn, err := net.DialTimeout("unix", p, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return p
		}
	}
	return ""
}

func firstDialable(paths []string) (string, error) {
	var last error
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			last = err
			continue
		}
		conn, err := net.DialTimeout("unix", p, 400*time.Millisecond)
		if err != nil {
			last = err
			continue
		}
		_ = conn.Close()
		return p, nil
	}
	if last == nil {
		return "", fmt.Errorf("docker socket was not found")
	}
	return "", last
}

// Idle drops cached event and update state. Control calls this when the feature is disabled.
func (e *Engine) Idle() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updates = nil
	e.events = nil
	e.since = nil
}

// Snapshot discovers Docker engines and returns the grouped inventory.
func (e *Engine) Snapshot(ctx context.Context, hints []MachineHint) (Inventory, error) {
	now := e.now()
	var machines []Machine
	host := Machine{ID: HostMachineID, Name: "Host", Kind: KindHost}
	if sock, err := firstDialable(e.hostSockets()); err == nil {
		e.fillMachine(ctx, &host, sock, now)
	} else if p := firstExisting(e.hostSockets()); p != "" {
		host.Socket = p
		host.DaemonOK = false
		host.DaemonError = "Docker daemon is not reachable"
		if err != nil {
			host.DaemonError = err.Error()
		}
		host.Health = HealthCritical
		host.HealthReason = host.DaemonError
	}
	if host.Socket != "" || host.DaemonError != "" {
		machines = append(machines, host)
	}
	for _, h := range hints {
		id := strings.TrimSpace(h.ID)
		if id == "" || id == HostMachineID {
			continue
		}
		kind := strings.TrimSpace(h.Kind)
		if kind == "" {
			kind = KindSystemContainer
		}
		if kind != KindSystemContainer && kind != "workload" {
			continue
		}
		m := Machine{ID: id, Name: strings.TrimSpace(h.Name), Kind: KindSystemContainer}
		if m.Name == "" {
			m.Name = id
		}
		pid, ipv4, err := e.lookupLXC(ctx, id)
		m.IPv4 = ipv4
		if err != nil || pid <= 0 {
			continue
		}
		paths := guestSockets(pid)
		sock, derr := firstDialable(paths)
		if derr != nil {
			if p := firstExisting(paths); p != "" {
				m.Socket = p
				m.DaemonOK = false
				m.DaemonError = "Docker daemon is not reachable"
				m.Health = HealthCritical
				m.HealthReason = m.DaemonError
				if derr != nil {
					m.DaemonError = derr.Error()
					m.HealthReason = m.DaemonError
				}
				machines = append(machines, m)
			}
			continue
		}
		e.fillMachine(ctx, &m, sock, now)
		machines = append(machines, m)
	}
	inv := flattenInventory(machines)
	inv.ObservedAt = now
	return inv, nil
}

func (e *Engine) fillMachine(ctx context.Context, m *Machine, socket string, now time.Time) {
	m.Socket = socket
	cli := e.client(socket)
	ver, err := cli.ping(ctx)
	if err != nil {
		m.DaemonOK = false
		m.DaemonError = err.Error()
		m.Health = HealthCritical
		m.HealthReason = m.DaemonError
		return
	}
	m.DaemonOK = true
	m.DockerVersion = ver
	items, err := cli.listContainers(ctx)
	if err != nil {
		m.DaemonOK = false
		m.DaemonError = err.Error()
		m.Health = HealthCritical
		m.HealthReason = err.Error()
		return
	}
	events := e.collectEvents(ctx, cli, socket, now)
	var pending []Container
	for _, item := range items {
		c := e.containerFromList(m, item, now)
		if ins, ierr := cli.inspect(ctx, item.ID); ierr == nil {
			applyInspect(&c, ins, now)
		}
		if st, serr := cli.stats(ctx, item.ID); serr == nil {
			pct := cpuPercent(st)
			c.CPUPercent = &pct
			mem := st.MemoryStats.Usage
			lim := st.MemoryStats.Limit
			c.MemoryBytes = &mem
			if lim > 0 {
				c.MemoryLimit = &lim
			}
			if st.PidsStats.Current > 0 {
				p := st.PidsStats.Current
				c.Pids = &p
			}
		}
		key := containerRef(m.ID, shortID(item.ID))
		c.RecentEvents = e.mergeEvents(key, eventsFor(events, item.ID, c.Name))
		e.applyUpdateNote(&c)
		pending = append(pending, c)
	}
	*m = attachAndGroup(*m, pending)
}

func attachAndGroup(m Machine, containers []Container) Machine {
	m.Projects = []Project{{ID: "", Containers: containers}}
	return groupMachine(m)
}

func (e *Engine) containerFromList(m *Machine, item listItem, now time.Time) Container {
	project, service, workdir, configs := composeFields(item.Labels)
	id := shortID(item.ID)
	c := Container{
		Ref:         containerRef(m.ID, id),
		MachineID:   m.ID,
		MachineName: m.Name,
		MachineKind: m.Kind,
		MachineIPv4: m.IPv4,
		EngineID:    item.ID,
		Name:        containerName(item.Names, ""),
		Image:       item.Image,
		ImageID:     item.ImageID,
		State:       strings.ToLower(item.State),
		Status:      item.Status,
		Project:     project,
		Service:     service,
		WorkingDir:  relWorkingDir(workdir),
		ConfigFiles: configs,
		Labels:      item.Labels,
		CreatedAt:   time.Unix(item.Created, 0).UTC().Format(time.RFC3339),
	}
	for _, p := range item.Ports {
		c.Ports = append(c.Ports, Port{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
	}
	for name := range item.NetworkSettings.Networks {
		c.Networks = append(c.Networks, name)
	}
	for _, mt := range item.Mounts {
		c.Mounts = append(c.Mounts, Mount{Type: mt.Type, Name: mt.Name, Source: mt.Source, Destination: mt.Destination, RW: mt.RW, Mode: mt.Mode})
	}
	_ = now
	return c
}

func applyInspect(c *Container, ins inspectJSON, now time.Time) {
	c.EngineID = ins.ID
	c.Name = containerName(nil, ins.Name)
	c.State = strings.ToLower(ins.State.Status)
	c.ExitCode = ins.State.ExitCode
	c.OOMKilled = ins.State.OOMKilled
	c.Restarting = ins.State.Restarting
	c.Dead = ins.State.Dead
	c.Error = strings.TrimSpace(ins.State.Error)
	c.RestartCount = ins.RestartCount
	c.StartedAt = ins.State.StartedAt
	c.FinishedAt = ins.State.FinishedAt
	c.CreatedAt = ins.Created
	if ins.Config.Image != "" {
		c.Image = ins.Config.Image
	}
	if ins.State.Health != nil {
		c.HealthStatus = ins.State.Health.Status
		if ins.State.Health.FailingStreak > 0 && len(ins.State.Health.Log) > 0 {
			last := ins.State.Health.Log[len(ins.State.Health.Log)-1]
			if strings.TrimSpace(last.Output) != "" {
				c.Problems = append(c.Problems, Problem{
					Kind: "healthcheck", Level: HealthDegraded, Message: strings.TrimSpace(last.Output),
				})
			}
		}
	}
	project, service, workdir, configs := composeFields(ins.Config.Labels)
	if project != "" {
		c.Project = project
	}
	if service != "" {
		c.Service = service
	}
	if workdir != "" {
		c.WorkingDir = relWorkingDir(workdir)
	}
	if configs != "" {
		c.ConfigFiles = configs
	}
	c.Labels = ins.Config.Labels
	if c.State == "running" {
		c.Uptime = formatUptime(ins.State.StartedAt, now)
	}
	if len(c.Networks) == 0 {
		for name := range ins.NetworkSettings.Networks {
			c.Networks = append(c.Networks, name)
		}
	}
	if len(c.Mounts) == 0 {
		for _, mt := range ins.Mounts {
			c.Mounts = append(c.Mounts, Mount{Type: mt.Type, Name: mt.Name, Source: mt.Source, Destination: mt.Destination, RW: mt.RW, Mode: mt.Mode})
		}
	}
	if len(c.Ports) == 0 {
		for spec, binds := range ins.NetworkSettings.Ports {
			priv, proto := splitPortSpec(spec)
			if len(binds) == 0 {
				c.Ports = append(c.Ports, Port{PrivatePort: priv, Type: proto})
				continue
			}
			for _, b := range binds {
				pub, _ := strconv.Atoi(b.HostPort)
				c.Ports = append(c.Ports, Port{IP: b.HostIP, PrivatePort: priv, PublicPort: pub, Type: proto})
			}
		}
	}
}

func splitPortSpec(spec string) (int, string) {
	spec = strings.TrimSpace(spec)
	typ := "tcp"
	if i := strings.LastIndex(spec, "/"); i >= 0 {
		typ = spec[i+1:]
		spec = spec[:i]
	}
	n, _ := strconv.Atoi(spec)
	return n, typ
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (e *Engine) collectEvents(ctx context.Context, cli engineAPI, socket string, now time.Time) []dockerEvent {
	e.mu.Lock()
	if e.since == nil {
		e.since = map[string]time.Time{}
	}
	since := e.since[socket]
	if since.IsZero() {
		since = now.Add(-defaultEventLookback)
	}
	e.mu.Unlock()
	evs, err := cli.eventsSince(ctx, since, now)
	if err != nil {
		return nil
	}
	e.mu.Lock()
	e.since[socket] = now
	e.mu.Unlock()
	return evs
}

func eventsFor(evs []dockerEvent, engineID, name string) []Event {
	var out []Event
	short := shortID(engineID)
	name = strings.TrimPrefix(name, "/")
	for _, ev := range evs {
		id := ev.Actor.ID
		if id == "" {
			id = ev.ID
		}
		if id != engineID && shortID(id) != short && ev.Actor.Attributes["name"] != name {
			continue
		}
		extra := ev.Actor.Attributes["exitCode"]
		if extra != "" {
			extra = "exit " + extra
		}
		if ev.Actor.Attributes["image"] != "" && extra == "" {
			extra = ev.Actor.Attributes["image"]
		}
		ts := time.Unix(ev.Time, 0).UTC()
		if ev.TimeNano > 0 {
			ts = time.Unix(0, ev.TimeNano).UTC()
		}
		out = append(out, classifyEvent(ev.Type, ev.Action, ev.Status, shortID(id), extra, ts))
	}
	return out
}

func (e *Engine) mergeEvents(key string, incoming []Event) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.events == nil {
		e.events = map[string][]Event{}
	}
	cur := append(append([]Event{}, e.events[key]...), incoming...)
	if len(cur) > maxEventsPerContainer {
		cur = cur[len(cur)-maxEventsPerContainer:]
	}
	e.events[key] = cur
	return cur
}

func (e *Engine) applyUpdateNote(c *Container) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.updates != nil {
		note, ok := e.updates[c.Ref]
		if !ok {
			note, ok = e.updates[containerRef(c.MachineID, shortID(c.EngineID))]
		}
		if ok {
			c.UpdateFailed = note.Failed
			c.UpdateError = note.Error
		}
	}
	for _, ev := range c.RecentEvents {
		if eventKind(ev) == "update" && ev.Severe {
			c.UpdateFailed = true
			if c.UpdateError == "" {
				c.UpdateError = ev.Message
			}
		}
	}
}

func (e *Engine) rememberUpdate(ref string, failed bool, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.updates == nil {
		e.updates = map[string]updateNote{}
	}
	e.updates[ref] = updateNote{Failed: failed, Error: msg, At: e.now()}
}

func (e *Engine) resolveSocket(ctx context.Context, machineID string, hints []MachineHint) (string, error) {
	if machineID == "" || machineID == HostMachineID {
		sock, err := firstDialable(e.hostSockets())
		if err != nil {
			return "", fmt.Errorf("host docker is not reachable")
		}
		return sock, nil
	}
	pid, _, err := e.lookupLXC(ctx, machineID)
	if err != nil || pid <= 0 {
		return "", fmt.Errorf("workload docker is not reachable")
	}
	sock, err := firstDialable(guestSockets(pid))
	if err != nil {
		return "", fmt.Errorf("workload docker is not reachable")
	}
	return sock, nil
}

// Action runs a typed container operation.
func (e *Engine) Action(ctx context.Context, req ActionRequest, hints []MachineHint) (ActionResult, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "idle" {
		e.Idle()
		return ActionResult{OK: true, Message: "idle"}, nil
	}
	machineID, containerID := req.MachineID, req.ContainerID
	if containerID == "" && strings.Contains(req.MachineID, "/") {
		machineID, containerID = splitRef(req.MachineID)
	}
	if action == "logs" {
		sock, err := e.resolveSocket(ctx, machineID, hints)
		if err != nil {
			return ActionResult{}, err
		}
		text, err := e.client(sock).logs(ctx, containerID, req.Tail)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, Logs: text}, nil
	}
	sock, err := e.resolveSocket(ctx, machineID, hints)
	if err != nil {
		return ActionResult{}, err
	}
	cli := e.client(sock)
	ref := containerRef(machineID, shortID(containerID))
	var opErr error
	switch action {
	case "start":
		opErr = cli.start(ctx, containerID)
	case "stop":
		opErr = cli.stop(ctx, containerID)
	case "restart":
		opErr = cli.restart(ctx, containerID)
	case "pull":
		ins, ierr := cli.inspect(ctx, containerID)
		if ierr != nil {
			opErr = ierr
			break
		}
		opErr = cli.pull(ctx, ins.Config.Image)
	case "recreate", "update":
		opErr = cli.recreate(ctx, containerID)
	default:
		return ActionResult{}, fmt.Errorf("unknown docker action %s", action)
	}
	if opErr != nil {
		if action == "pull" || action == "recreate" || action == "update" {
			e.rememberUpdate(ref, true, opErr.Error())
		}
		return ActionResult{}, opErr
	}
	if action == "pull" || action == "recreate" || action == "update" {
		e.rememberUpdate(ref, false, "")
	}
	return ActionResult{OK: true, Message: action}, nil
}

// SocketFor resolves a docker.sock path for exec.
func (e *Engine) SocketFor(ctx context.Context, machineID string) (string, error) {
	return e.resolveSocket(ctx, machineID, nil)
}
