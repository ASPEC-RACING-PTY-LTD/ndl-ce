package appmanifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const APIVersion = "nodal.store/v1"

const (
	ClassCommunity = "community"
	ClassVerified  = "verified"
	ClassOfficial  = "official"
)

var prohibitedKeys = []string{
	"run", "script", "bash", "exec", "helper", "postinst", "preinst", "command_script", "host_exec",
}

// Manifest is a declarative Store package. It is not a shell script.
type Manifest struct {
	APIVersion  string     `yaml:"apiVersion" json:"api_version"`
	Name        string     `yaml:"name" json:"name"`
	Version     string     `yaml:"version" json:"version"`
	Class       string     `yaml:"class" json:"class"`
	Title       string     `yaml:"title" json:"title"`
	Summary     string     `yaml:"summary" json:"summary"`
	Resources   Resources  `yaml:"resources" json:"resources"`
	Storage     []Volume   `yaml:"storage" json:"storage,omitempty"`
	Devices     Devices    `yaml:"devices" json:"devices"`
	Ports       []Port     `yaml:"ports" json:"ports,omitempty"`
	Deployment  Deployment `yaml:"deployment" json:"deployment"`
	Hooks       Hooks      `yaml:"hooks" json:"hooks"`
	AIActions   []AIAction `yaml:"ai_actions" json:"ai_actions,omitempty"`
	Permissions []string   `yaml:"permissions" json:"permissions,omitempty"`
}

type Resources struct {
	CPU         int   `yaml:"cpu" json:"cpu"`
	MemoryBytes int64 `yaml:"memory_bytes" json:"memory_bytes"`
}

type Volume struct {
	Name       string `yaml:"name" json:"name"`
	Persistent bool   `yaml:"persistent" json:"persistent"`
}

type Devices struct {
	GPU GPU `yaml:"gpu" json:"gpu"`
}

type GPU struct {
	Optional bool `yaml:"optional" json:"optional"`
}

type Port struct {
	Container int `yaml:"container" json:"container"`
	Host      int `yaml:"host" json:"host"`
}

type Deployment struct {
	Kind  string `yaml:"kind" json:"kind"`
	Image string `yaml:"image" json:"image"`
}

type Hooks struct {
	Backup  string `yaml:"backup" json:"backup"`
	Restore string `yaml:"restore" json:"restore"`
}

type AIAction struct {
	ID          string `yaml:"id" json:"id"`
	Title       string `yaml:"title" json:"title"`
	Declaration string `yaml:"declaration" json:"declaration"`
}

// ParseYAML loads a Store manifest and rejects script-shaped keys.
func ParseYAML(raw []byte) (*Manifest, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("manifest yaml is invalid")
	}
	if err := rejectProhibited(&root); err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest yaml is invalid")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func rejectProhibited(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := strings.ToLower(strings.TrimSpace(n.Content[i].Value))
			for _, bad := range prohibitedKeys {
				if key == bad {
					return fmt.Errorf("manifest must not include %q; Store packages are declarative and do not run helper scripts", bad)
				}
			}
			val := n.Content[i+1]
			if key == "run" || strings.Contains(strings.ToLower(val.Value), "bash") {
				return fmt.Errorf("manifest must not include run: bash")
			}
			if err := rejectProhibited(val); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range n.Content {
		if err := rejectProhibited(c); err != nil {
			return err
		}
	}
	return nil
}

func (m Manifest) Validate() error {
	if strings.TrimSpace(m.APIVersion) != APIVersion {
		return fmt.Errorf("apiVersion must be %s", APIVersion)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("version is required")
	}
	switch strings.TrimSpace(m.Class) {
	case "community", "verified", "official":
	default:
		return fmt.Errorf("class must be community, verified, or official")
	}
	kind := strings.TrimSpace(m.Deployment.Kind)
	if kind != "oci" && kind != "container" {
		return fmt.Errorf("deployment.kind must be oci")
	}
	if strings.TrimSpace(m.Deployment.Image) == "" {
		return fmt.Errorf("deployment.image is required")
	}
	if strings.ContainsAny(m.Deployment.Image, " \n;|&") {
		return fmt.Errorf("deployment.image is not a shell string")
	}
	if m.Resources.CPU < 0 || m.Resources.MemoryBytes < 0 {
		return fmt.Errorf("resources must be non-negative")
	}
	if m.Hooks.Backup != "" && m.Hooks.Backup != "existing-backup-api" {
		return fmt.Errorf("hooks.backup must declare existing-backup-api")
	}
	if m.Hooks.Restore != "" && m.Hooks.Restore != "existing-restore-api" {
		return fmt.Errorf("hooks.restore must declare existing-restore-api")
	}
	return nil
}

func (m Manifest) UnsignedCommunity() bool {
	return m.Class == "community"
}
