package gameserver

import (
	"encoding/json"
	"strings"
)

// Template is No-DAL's internal game-server blueprint. External eggs are
// normalized into this shape and never executed as raw upstream JSON.
type Template struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Game            string            `json:"game"`
	Implementation  string            `json:"implementation"`
	Summary         string            `json:"summary"`
	Family          string            `json:"family"`
	Capabilities    []string          `json:"capabilities"`
	Images          map[string]string `json:"images"`
	DefaultImage    string            `json:"default_image"`
	Startup         string            `json:"startup"`
	Stop            string            `json:"stop"`
	Done            string            `json:"startup_done"`
	WorkingDir      string            `json:"working_dir"`
	InstallImage    string            `json:"install_image"`
	InstallScript   string            `json:"install_script,omitempty"`
	InstallBuiltin  string            `json:"install_builtin,omitempty"`
	Variables       []Variable        `json:"variables"`
	ConfigFiles     []ConfigFile      `json:"config_files"`
	FriendlyConfig  []Setting         `json:"friendly_config"`
	Content         ContentSpec       `json:"content"`
	DefaultPorts    []Port            `json:"default_ports"`
	DefaultMemoryMB int               `json:"default_memory_mb"`
	DefaultDiskMB   int               `json:"default_disk_mb"`
	DefaultCPUs     int               `json:"default_cpus"`
	SourceURL       string            `json:"source_url,omitempty"`
	SourceKind      string            `json:"source_kind,omitempty"`
	UpdateURL       string            `json:"update_url,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Aliases         []string          `json:"aliases,omitempty"`
	StartRequires   []string          `json:"start_requires,omitempty"`
	Hint            string            `json:"hint,omitempty"`
}

// Variable is a user-facing startup/environment field.
type Variable struct {
	Name        string `json:"name"`
	Env         string `json:"env"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Viewable    bool   `json:"viewable"`
	Editable    bool   `json:"editable"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Rules       string `json:"rules,omitempty"`
	FieldType   string `json:"field_type,omitempty"`
}

// ConfigFile describes a structured or key=value file the UI may edit safely.
type ConfigFile struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	Restart  bool   `json:"restart"`
	Parser   string `json:"parser,omitempty"`
	DenyEdit bool   `json:"deny_edit,omitempty"`
}

// Setting is a friendly control mapped onto a config file or env var.
type Setting struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Help     string   `json:"help"`
	Kind     string   `json:"kind"`
	File     string   `json:"file,omitempty"`
	Key      string   `json:"key,omitempty"`
	Env      string   `json:"env,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Restart  bool     `json:"restart"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
}

// ContentSpec names the content ecosystem for this template.
type ContentSpec struct {
	Provider     string `json:"provider,omitempty"`
	Kind         string `json:"kind,omitempty"`
	InstallDir   string `json:"install_dir,omitempty"`
	Loader       string `json:"loader,omitempty"`
	GameID       string `json:"game_id,omitempty"`
	Dependencies bool   `json:"dependencies,omitempty"`
}

// Port is a published server port.
type Port struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Primary       bool   `json:"primary,omitempty"`
}

func (t Template) imageFor(label string) string {
	label = strings.TrimSpace(label)
	if label != "" {
		if img, ok := t.Images[label]; ok && strings.TrimSpace(img) != "" {
			return img
		}
	}
	if strings.TrimSpace(t.DefaultImage) != "" {
		return t.DefaultImage
	}
	for _, img := range t.Images {
		if strings.TrimSpace(img) != "" {
			return img
		}
	}
	return ""
}

func (t Template) variable(env string) (Variable, bool) {
	want := strings.ToUpper(strings.TrimSpace(env))
	for _, v := range t.Variables {
		if strings.ToUpper(strings.TrimSpace(v.Env)) == want {
			return v, true
		}
	}
	return Variable{}, false
}

func cloneTemplate(t Template) Template {
	raw, _ := json.Marshal(t)
	var out Template
	_ = json.Unmarshal(raw, &out)
	return out
}

// RuntimeKind is the compact catalogue label for how this template installs.
func (t Template) RuntimeKind() string {
	switch strings.ToLower(strings.TrimSpace(t.InstallBuiltin)) {
	case "steamcmd":
		return "SteamCMD"
	case "paper", "minecraft-vanilla", "minecraft-fabric", "minecraft-purpur", "minecraft-velocity", "mindustry":
		return "Java"
	case "fivem", "terraria", "factorio":
		return "Standalone"
	default:
		if strings.TrimSpace(t.InstallBuiltin) != "" {
			return "Built in"
		}
		if strings.TrimSpace(t.SourceURL) != "" || strings.EqualFold(t.SourceKind, "egg") {
			return "Imported"
		}
		return "Built in"
	}
}
