package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"time"
)

func projectID(machineID, name, workdir string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return machineID + "/" + StandaloneProject
	}
	id := machineID + "/" + sanitizeID(name)
	workdir = strings.TrimSpace(workdir)
	if workdir == "" || workdir == "/" {
		return id
	}
	sum := sha256.Sum256([]byte(workdir))
	return id + "+" + hex.EncodeToString(sum[:4])
}

func sanitizeID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return StandaloneProject
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return StandaloneProject
	}
	return out
}

func containerRef(machineID, engineID string) string {
	return strings.TrimSpace(machineID) + "/" + strings.TrimSpace(engineID)
}

// ParseRef splits machine/container identity used by the API and terminal.
func ParseRef(ref string) (machineID, containerID string) {
	return splitRef(ref)
}

// Ref joins machine and container identity.
func Ref(machineID, containerID string) string {
	return containerRef(machineID, containerID)
}

func splitRef(ref string) (machineID, containerID string) {
	ref = strings.TrimSpace(ref)
	machineID, containerID, ok := strings.Cut(ref, "/")
	if !ok {
		return "", ref
	}
	return machineID, containerID
}

func composeFields(labels map[string]string) (project, service, workdir, configs string) {
	if len(labels) == 0 {
		return "", "", "", ""
	}
	project = strings.TrimSpace(labels[LabelProject])
	service = strings.TrimSpace(labels[LabelService])
	workdir = strings.TrimSpace(labels[LabelWorkingDir])
	configs = strings.TrimSpace(labels[LabelConfig])
	return project, service, workdir, configs
}

func containerName(names []string, inspectName string) string {
	n := strings.TrimPrefix(strings.TrimSpace(inspectName), "/")
	if n != "" {
		return n
	}
	for _, raw := range names {
		n = strings.TrimPrefix(strings.TrimSpace(raw), "/")
		if n != "" {
			return n
		}
	}
	return ""
}

func groupMachines(machines []Machine) []Machine {
	out := make([]Machine, 0, len(machines))
	for _, m := range machines {
		out = append(out, groupMachine(m))
	}
	return out
}

func groupMachine(m Machine) Machine {
	flat := append([]Container(nil), m.containersOrFlat()...)
	grouped := map[string]*Project{}
	order := make([]string, 0)
	for i := range flat {
		c := flat[i]
		c.MachineID = m.ID
		c.MachineName = m.Name
		c.MachineKind = m.Kind
		if c.MachineIPv4 == "" {
			c.MachineIPv4 = m.IPv4
		}
		if c.Project == "" {
			c.ProjectID = projectID(m.ID, "", c.WorkingDir)
		} else {
			c.ProjectID = projectID(m.ID, c.Project, c.WorkingDir)
		}
		classifyContainer(&c, time.Now().UTC())
		p := grouped[c.ProjectID]
		if p == nil {
			p = &Project{
				ID:          c.ProjectID,
				MachineID:   m.ID,
				MachineName: m.Name,
				Name:        c.Project,
				WorkingDir:  c.WorkingDir,
				ConfigFiles: c.ConfigFiles,
				Standalone:  c.Project == "",
			}
			if p.Name == "" {
				p.Name = "Standalone"
			}
			grouped[c.ProjectID] = p
			order = append(order, c.ProjectID)
		}
		if p.WorkingDir == "" {
			p.WorkingDir = c.WorkingDir
		}
		if p.ConfigFiles == "" {
			p.ConfigFiles = c.ConfigFiles
		}
		p.Containers = append(p.Containers, c)
	}
	projects := make([]Project, 0, len(order))
	var healths, reasons []string
	total := 0
	for _, id := range order {
		p := grouped[id]
		finishProject(p)
		healths = append(healths, p.Health)
		reasons = append(reasons, p.HealthReason)
		total += len(p.Containers)
		projects = append(projects, *p)
	}
	if !m.DaemonOK {
		m.Health = HealthCritical
		if m.DaemonError == "" {
			m.HealthReason = "Docker daemon is not reachable"
		} else {
			m.HealthReason = m.DaemonError
		}
	} else if total == 0 {
		m.Health = HealthHealthy
		m.HealthReason = "Docker is reachable"
	} else {
		m.Health, m.HealthReason = rollupHealth(healths, reasons)
	}
	m.Projects = projects
	m.ProjectCount = len(projects)
	m.ContainerN = total
	return m
}

func (m Machine) containersOrFlat() []Container {
	if len(m.Projects) == 1 && len(m.Projects[0].Containers) > 0 && m.Projects[0].ID == "" {
		return m.Projects[0].Containers
	}
	var out []Container
	for _, p := range m.Projects {
		out = append(out, p.Containers...)
	}
	return out
}

func finishProject(p *Project) {
	if p == nil {
		return
	}
	var healths, reasons []string
	services := map[string]struct{}{}
	for i := range p.Containers {
		c := &p.Containers[i]
		if c.Service != "" {
			services[c.Service] = struct{}{}
		}
		if c.State == "running" {
			p.Running++
		} else {
			p.Stopped++
		}
		if c.UpdateFailed {
			p.UpdateFailed = true
		}
		healths = append(healths, c.Health)
		reasons = append(reasons, c.HealthReason)
	}
	p.Services = len(services)
	p.Health, p.HealthReason = rollupHealth(healths, reasons)
	if p.UpdateFailed && p.Health == HealthHealthy {
		p.Health = HealthDegraded
		p.HealthReason = "Update failed"
	}
	switch {
	case p.UpdateFailed && p.Running > 0:
		p.StatusLabel = "Running · Update Failed"
	case p.Health == HealthCritical:
		p.StatusLabel = "Critical"
	case p.Health == HealthDegraded:
		p.StatusLabel = "Degraded"
	case p.Running > 0 && p.Stopped > 0:
		p.StatusLabel = "Partial"
	case p.Running > 0:
		p.StatusLabel = "Running"
	case p.Stopped > 0:
		p.StatusLabel = "Stopped"
	default:
		p.StatusLabel = "Empty"
	}
}

func flattenInventory(machines []Machine) Inventory {
	inv := Inventory{Machines: groupMachines(machines)}
	for _, m := range inv.Machines {
		inv.Projects = append(inv.Projects, m.Projects...)
		for _, p := range m.Projects {
			inv.Containers = append(inv.Containers, p.Containers...)
		}
	}
	summarize(&inv)
	return inv
}

func relWorkingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return path.Clean(dir)
}
