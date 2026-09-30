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

	// Catalogue and provisioning metadata. Every field below is optional and
	// additive so templates stored by older releases still decode unchanged.
	GameTitle     string        `json:"game_title,omitempty"`
	Category      string        `json:"category,omitempty"`
	Engine        string        `json:"engine,omitempty"`
	Architectures []string      `json:"architectures,omitempty"`
	Install       *InstallSpec  `json:"install,omitempty"`
	Dependencies  []string      `json:"dependencies,omitempty"`
	Requirements  []Requirement `json:"requirements,omitempty"`
	StopTimeout   int           `json:"stop_timeout,omitempty"`
	MinMemoryMB   int           `json:"min_memory_mb,omitempty"`
	DocsURL       string        `json:"docs_url,omitempty"`
	SourceRef     string        `json:"source_ref,omitempty"`
	Notes         []string      `json:"notes,omitempty"`
	// Hidden keeps a template resolvable for servers created from it while
	// removing it from the catalogue (for example a template found to be
	// unsupported on Linux).
	Hidden bool `json:"hidden,omitempty"`
}

// InstallSpec carries installer parameters for the reusable installers.
// SteamCMD reads Platform, AppConfig and Beta. The download and
// github-release installers read Downloads. PaperMC-style APIs read Project.
type InstallSpec struct {
	Platform   string     `json:"platform,omitempty"`
	AppConfig  string     `json:"app_config,omitempty"`
	Beta       string     `json:"beta,omitempty"`
	Project    string     `json:"project,omitempty"`
	VersionEnv string     `json:"version_env,omitempty"`
	Downloads  []Download `json:"downloads,omitempty"`
}

// Download is one artifact fetched by the download or github-release
// installer. URL may reference template variables as {{NAME}}.
type Download struct {
	Repo        string   `json:"repo,omitempty"`
	Asset       string   `json:"asset,omitempty"`
	URL         string   `json:"url,omitempty"`
	Archive     string   `json:"archive,omitempty"`
	Strip       int      `json:"strip,omitempty"`
	Dest        string   `json:"dest,omitempty"`
	Subdir      string   `json:"subdir,omitempty"`
	Executables []string `json:"executables,omitempty"`
	SHA256URL   string   `json:"sha256_url,omitempty"`
	// Keep lists files (relative to the extraction folder) that an update
	// must not overwrite once they exist, such as shipped configs.
	Keep []string `json:"keep,omitempty"`
}

// Requirement is a prerequisite the operator must satisfy. Stage decides
// when it is enforced: install blocks creation, start blocks power on,
// optional is informational (for example a token only needed for public
// listing).
type Requirement struct {
	Kind  string `json:"kind"`
	Stage string `json:"stage"`
	Env   string `json:"env,omitempty"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

const (
	ReqSteamAccount = "steam_account"
	ReqGSLT         = "gslt"
	ReqLicenseKey   = "license_key"
	ReqToken        = "token"
	ReqAPIKey       = "api_key"
	ReqEULA         = "eula"
	ReqPurchase     = "purchase"

	StageInstall  = "install"
	StageStart    = "start"
	StageOptional = "optional"
)

// Variable is a user-facing startup/environment field.
type Variable struct {
	Name        string   `json:"name"`
	Env         string   `json:"env"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Viewable    bool     `json:"viewable"`
	Editable    bool     `json:"editable"`
	Required    bool     `json:"required"`
	Secret      bool     `json:"secret"`
	Rules       string   `json:"rules,omitempty"`
	FieldType   string   `json:"field_type,omitempty"`
	Generate    string   `json:"generate,omitempty"`
	Options     []string `json:"options,omitempty"`
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
	// Env names the variable that tells the server which port to bind.
	// Allocation keeps the container and host port equal and updates it.
	Env string `json:"env,omitempty"`
	// Fixed marks a port the game always binds at this number (set in a
	// config file). Allocation maps a different host port onto it. Ports
	// with neither Env nor Fixed are derived by offset from another port
	// (for example query = game + 1) and move with the primary port.
	Fixed bool `json:"fixed,omitempty"`
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
	if inst, ok := lookupInstaller(t.InstallBuiltin); ok {
		return inst.Kind
	}
	if strings.TrimSpace(t.InstallBuiltin) != "" {
		return "Built in"
	}
	if strings.TrimSpace(t.SourceURL) != "" || strings.EqualFold(t.SourceKind, "egg") {
		return "Imported"
	}
	return "Built in"
}

// InstallMethod is the human label of the installer that provisions files.
func (t Template) InstallMethod() string {
	if inst, ok := lookupInstaller(t.InstallBuiltin); ok {
		return inst.Label
	}
	if strings.TrimSpace(t.InstallScript) != "" {
		return "Imported script"
	}
	return "None"
}

// Arches returns the CPU architectures this template can run on.
func (t Template) Arches() []string {
	if len(t.Architectures) == 0 {
		return []string{"amd64"}
	}
	return t.Architectures
}

// RequirementsAt lists the requirements enforced at a stage, including the
// legacy StartRequires list.
func (t Template) RequirementsAt(stage string) []Requirement {
	var out []Requirement
	seen := map[string]bool{}
	for _, r := range t.Requirements {
		if r.Stage == stage {
			out = append(out, r)
			seen[r.Env] = true
		}
	}
	if stage == StageStart {
		for _, env := range t.StartRequires {
			env = strings.TrimSpace(env)
			if env != "" && !seen[env] {
				out = append(out, Requirement{Kind: ReqToken, Stage: StageStart, Env: env, Label: env})
			}
		}
	}
	return out
}
