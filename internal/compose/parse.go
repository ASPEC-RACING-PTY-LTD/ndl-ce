package compose

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/no-dal/ndl-ce/internal/oci"
	"gopkg.in/yaml.v3"
)

// File is a subset of Compose used for No-dal stack import.
// The result becomes inspectable stack objects; Compose is not runtime SoT.
type File struct {
	Services map[string]Service `yaml:"services"`
	Volumes  map[string]any     `yaml:"volumes"`
}

// Service is one Compose service mapped to an OCI workload desired state.
type Service struct {
	Image       string       `yaml:"image"`
	Environment yaml.Node    `yaml:"environment"`
	Ports       []yaml.Node  `yaml:"ports"`
	Volumes     []yaml.Node  `yaml:"volumes"`
	Privileged  bool         `yaml:"privileged"`
	Command     yaml.Node    `yaml:"command"`
	Healthcheck *Healthcheck `yaml:"healthcheck"`
}

// Healthcheck is the Compose healthcheck subset we import.
type Healthcheck struct {
	Test []string `yaml:"test"`
}

// PortMapping is host/container publish desired state.
type PortMapping struct {
	ContainerPort int
	HostPort      int
	Protocol      string
}

// VolumeMapping is a named volume bind into the container.
type VolumeMapping struct {
	Name          string // compose named volume key
	VolumeID      string // optional pre-mapped No-dal UUID
	ContainerPath string
	ReadOnly      bool
	HostPath      string // set only for rejected bind mounts
	Anonymous     bool
}

// ParsedService is the inspectable import of one service.
type ParsedService struct {
	Name       string
	Image      string
	Env        []oci.EnvVar
	Ports      []PortMapping
	Volumes    []VolumeMapping
	Privileged bool
	Command    []string
	Health     *oci.Healthcheck
}

// ParseResult is the full imported desired document before volume UUID resolution.
type ParseResult struct {
	Services     []ParsedService
	NamedVolumes []string
}

// ParseYAML parses Compose YAML into No-dal stack desired members.
// Rejects anonymous volumes and host bind of /. Privileged is flagged for the caller.
func ParseYAML(raw []byte) (*ParseResult, error) {
	var file File
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("compose yaml: %w", err)
	}
	if len(file.Services) == 0 {
		return nil, fmt.Errorf("compose has no services")
	}
	named := map[string]bool{}
	for name := range file.Volumes {
		named[name] = true
	}
	out := &ParseResult{}
	for name, svc := range file.Services {
		ps, err := parseService(name, svc, named)
		if err != nil {
			return nil, err
		}
		out.Services = append(out.Services, *ps)
		for _, v := range ps.Volumes {
			if v.Name != "" {
				named[v.Name] = true
			}
		}
	}
	for name := range named {
		out.NamedVolumes = append(out.NamedVolumes, name)
	}
	// Stable-ish order by service name for deterministic apply.
	sortServices(out.Services)
	sortStrings(out.NamedVolumes)
	return out, nil
}

func parseService(name string, svc Service, named map[string]bool) (*ParsedService, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("service name is required")
	}
	image := strings.TrimSpace(svc.Image)
	if image == "" {
		return nil, fmt.Errorf("service %q: image is required", name)
	}
	if err := oci.ValidateImageRef(image); err != nil {
		return nil, fmt.Errorf("service %q: %w", name, err)
	}
	env, err := parseEnv(svc.Environment)
	if err != nil {
		return nil, fmt.Errorf("service %q: %w", name, err)
	}
	ports, err := parsePorts(svc.Ports)
	if err != nil {
		return nil, fmt.Errorf("service %q: %w", name, err)
	}
	vols, err := parseVolumes(svc.Volumes, named)
	if err != nil {
		return nil, fmt.Errorf("service %q: %w", name, err)
	}
	cmd, err := parseStringOrList(svc.Command)
	if err != nil {
		return nil, fmt.Errorf("service %q: command: %w", name, err)
	}
	var health *oci.Healthcheck
	if svc.Healthcheck != nil && len(svc.Healthcheck.Test) > 0 {
		// Import is declarative; HTTP path health is preferred when present as curl-ish test.
		health = healthFromTest(svc.Healthcheck.Test)
	}
	return &ParsedService{
		Name: name, Image: image, Env: env, Ports: ports, Volumes: vols,
		Privileged: svc.Privileged, Command: cmd, Health: health,
	}, nil
}

func parseEnv(node yaml.Node) ([]oci.EnvVar, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		var out []oci.EnvVar
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := strings.TrimSpace(node.Content[i].Value)
			v := node.Content[i+1].Value
			if k == "" {
				continue
			}
			out = append(out, oci.EnvVar{Name: k, Value: v})
		}
		return out, nil
	case yaml.SequenceNode:
		var out []oci.EnvVar
		for _, item := range node.Content {
			raw := strings.TrimSpace(item.Value)
			if raw == "" {
				continue
			}
			k, v, ok := strings.Cut(raw, "=")
			if !ok {
				out = append(out, oci.EnvVar{Name: raw})
				continue
			}
			out = append(out, oci.EnvVar{Name: k, Value: v})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("environment must be a map or list")
	}
}

func parsePorts(nodes []yaml.Node) ([]PortMapping, error) {
	var out []PortMapping
	for _, n := range nodes {
		switch n.Kind {
		case yaml.ScalarNode:
			p, err := parsePortShort(n.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		case yaml.MappingNode:
			p, err := parsePortLong(n)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		default:
			return nil, fmt.Errorf("invalid ports entry")
		}
	}
	return out, nil
}

func parsePortShort(raw string) (PortMapping, error) {
	raw = strings.TrimSpace(raw)
	proto := "tcp"
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		proto = strings.ToLower(raw[i+1:])
		raw = raw[:i]
	}
	parts := strings.Split(raw, ":")
	var host, container int
	var err error
	switch len(parts) {
	case 1:
		container, err = strconv.Atoi(parts[0])
		host = 0
	case 2:
		host, err = strconv.Atoi(parts[0])
		if err == nil {
			container, err = strconv.Atoi(parts[1])
		}
	case 3:
		// ip:host:container - ignore ip
		host, err = strconv.Atoi(parts[1])
		if err == nil {
			container, err = strconv.Atoi(parts[2])
		}
	default:
		return PortMapping{}, fmt.Errorf("invalid port %q", raw)
	}
	if err != nil || container < 1 || container > 65535 || host < 0 || host > 65535 {
		return PortMapping{}, fmt.Errorf("invalid port %q", raw)
	}
	return PortMapping{ContainerPort: container, HostPort: host, Protocol: proto}, nil
}

func parsePortLong(n yaml.Node) (PortMapping, error) {
	var target, published int
	proto := "tcp"
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		v := n.Content[i+1].Value
		switch k {
		case "target":
			target, _ = strconv.Atoi(v)
		case "published":
			published, _ = strconv.Atoi(v)
		case "protocol":
			proto = strings.ToLower(v)
		}
	}
	if target < 1 || target > 65535 {
		return PortMapping{}, fmt.Errorf("invalid long port target")
	}
	return PortMapping{ContainerPort: target, HostPort: published, Protocol: proto}, nil
}

func parseVolumes(nodes []yaml.Node, named map[string]bool) ([]VolumeMapping, error) {
	var out []VolumeMapping
	for _, n := range nodes {
		switch n.Kind {
		case yaml.ScalarNode:
			v, err := parseVolumeShort(n.Value, named)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		case yaml.MappingNode:
			v, err := parseVolumeLong(n)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		default:
			return nil, fmt.Errorf("invalid volumes entry")
		}
	}
	return out, nil
}

func parseVolumeShort(raw string, named map[string]bool) (VolumeMapping, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return VolumeMapping{}, fmt.Errorf("empty volume")
	}
	parts := strings.Split(raw, ":")
	switch len(parts) {
	case 1:
		// anonymous volume (container path only)
		return VolumeMapping{}, fmt.Errorf("anonymous volumes are not allowed")
	case 2, 3:
		src := parts[0]
		dst := parts[1]
		ro := len(parts) == 3 && strings.Contains(parts[2], "ro")
		if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") {
			cleaned := path.Clean(src)
			if cleaned == "/" {
				return VolumeMapping{}, fmt.Errorf("host bind to / is not allowed")
			}
			return VolumeMapping{}, fmt.Errorf("host bind mounts are not allowed; use named volumes mapped to No-dal volume UUIDs")
		}
		if dst == "" || !strings.HasPrefix(dst, "/") {
			return VolumeMapping{}, fmt.Errorf("container path must be absolute")
		}
		if path.Clean(dst) == "/" {
			return VolumeMapping{}, fmt.Errorf("container path must not be /")
		}
		_ = named
		return VolumeMapping{Name: src, ContainerPath: dst, ReadOnly: ro}, nil
	default:
		return VolumeMapping{}, fmt.Errorf("invalid volume %q", raw)
	}
}

func parseVolumeLong(n yaml.Node) (VolumeMapping, error) {
	typ := "volume"
	var source, target string
	ro := false
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		v := n.Content[i+1].Value
		switch k {
		case "type":
			typ = v
		case "source":
			source = v
		case "target":
			target = v
		case "read_only":
			ro = v == "true" || v == "True"
		}
	}
	switch typ {
	case "volume":
		if source == "" {
			return VolumeMapping{}, fmt.Errorf("anonymous volumes are not allowed")
		}
		if target == "" || !strings.HasPrefix(target, "/") || path.Clean(target) == "/" {
			return VolumeMapping{}, fmt.Errorf("invalid volume target")
		}
		return VolumeMapping{Name: source, ContainerPath: target, ReadOnly: ro}, nil
	case "bind":
		cleaned := path.Clean(source)
		if cleaned == "/" {
			return VolumeMapping{}, fmt.Errorf("host bind to / is not allowed")
		}
		return VolumeMapping{}, fmt.Errorf("host bind mounts are not allowed; use named volumes mapped to No-dal volume UUIDs")
	default:
		return VolumeMapping{}, fmt.Errorf("volume type %q is not supported", typ)
	}
}

func parseStringOrList(node yaml.Node) ([]string, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return nil, nil
		}
		return []string{node.Value}, nil
	case yaml.SequenceNode:
		var out []string
		for _, item := range node.Content {
			out = append(out, item.Value)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be string or list")
	}
}

func healthFromTest(test []string) *oci.Healthcheck {
	// Best-effort: CMD-SHELL curl http://localhost:PORT/path
	joined := strings.Join(test, " ")
	if strings.Contains(joined, "http://") || strings.Contains(joined, "https://") {
		for _, tok := range strings.Fields(joined) {
			if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
				u := strings.TrimPrefix(strings.TrimPrefix(tok, "https://"), "http://")
				hostPath := u
				port := 80
				if i := strings.Index(hostPath, "/"); i >= 0 {
					hostPort := hostPath[:i]
					p := hostPath[i:]
					if j := strings.LastIndex(hostPort, ":"); j >= 0 {
						if n, err := strconv.Atoi(hostPort[j+1:]); err == nil {
							port = n
						}
					}
					return &oci.Healthcheck{HTTPPath: p, Port: port}
				}
			}
		}
	}
	return nil
}

func sortServices(items []ParsedService) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Name < items[i].Name {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func sortStrings(items []string) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j] < items[i] {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}
