package docker

import (
	"sort"
	"strings"
)

// LabelDependsOn is the Compose label listing a service's dependencies, as
// "db:service_healthy:false,redis:service_started:true".
const LabelDependsOn = "com.docker.compose.depends_on"

// DependsOn returns the service names a container depends on.
func DependsOn(c Container) []string {
	raw := strings.TrimSpace(c.Labels[LabelDependsOn])
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(strings.SplitN(part, ":", 2)[0])
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func serviceKey(c Container) string {
	if c.Service != "" {
		return c.Service
	}
	return c.Name
}

// RecoveryOrder returns the containers to restart to fix targets, in
// layers: what the targets depend on comes first (deepest dependency
// first), then the targets. Containers in one layer can restart together.
// project is every container of the targets' Compose project.
func RecoveryOrder(project []Container, targets []Container) [][]Container {
	byService := map[string]Container{}
	for _, c := range project {
		byService[serviceKey(c)] = c
	}
	for _, c := range targets {
		if _, ok := byService[serviceKey(c)]; !ok {
			byService[serviceKey(c)] = c
		}
	}
	want := map[string]bool{}
	var visit func(svc string, depth int)
	visit = func(svc string, depth int) {
		if want[svc] || depth > 32 {
			return
		}
		c, ok := byService[svc]
		if !ok {
			return
		}
		want[svc] = true
		for _, dep := range DependsOn(c) {
			visit(dep, depth+1)
		}
	}
	for _, t := range targets {
		visit(serviceKey(t), 0)
	}
	level := map[string]int{}
	var depth func(svc string, seen map[string]bool) int
	depth = func(svc string, seen map[string]bool) int {
		if l, ok := level[svc]; ok {
			return l
		}
		if seen[svc] {
			return 0 // a dependency cycle; break it here
		}
		seen[svc] = true
		l := 0
		for _, dep := range DependsOn(byService[svc]) {
			if want[dep] {
				if d := depth(dep, seen) + 1; d > l {
					l = d
				}
			}
		}
		delete(seen, svc)
		level[svc] = l
		return l
	}
	max := -1
	for svc := range want {
		if l := depth(svc, map[string]bool{}); l > max {
			max = l
		}
	}
	layers := make([][]Container, max+1)
	for svc := range want {
		layers[level[svc]] = append(layers[level[svc]], byService[svc])
	}
	for _, layer := range layers {
		sort.Slice(layer, func(i, j int) bool { return layer[i].Name < layer[j].Name })
	}
	return layers
}

// NeedsFix reports whether a container counts as unhealthy for Fix.
func NeedsFix(c Container) bool {
	if c.Health == HealthCritical || c.Health == HealthDegraded {
		return true
	}
	return c.State == "exited" && c.ExitCode != 0
}
