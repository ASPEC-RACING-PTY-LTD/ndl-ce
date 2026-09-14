package gameserver

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EggDocument is the subset of Pterodactyl/Pelican egg JSON we accept.
// Fields are parsed for semantics only; we never execute upstream code as-is
// without normalizing into Template.
type EggDocument struct {
	Name         string            `json:"name"`
	Author       string            `json:"author"`
	Description  string            `json:"description"`
	Features     []string          `json:"features"`
	DockerImages map[string]string `json:"docker_images"`
	FileDenylist []string          `json:"file_denylist"`
	Startup      any               `json:"startup"`
	Config       eggConfig         `json:"config"`
	Scripts      eggScripts        `json:"scripts"`
	Variables    []eggVariable     `json:"variables"`
	Meta         eggMeta           `json:"meta"`
	UpdateURL    string            `json:"update_url"`
}

type eggConfig struct {
	Files   any `json:"files"`
	Startup any `json:"startup"`
	Logs    any `json:"logs"`
	Stop    any `json:"stop"`
}

type eggScripts struct {
	Installation eggInstall `json:"installation"`
}

type eggInstall struct {
	Script     string `json:"script"`
	Container  string `json:"container"`
	Entrypoint string `json:"entrypoint"`
}

type eggVariable struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	EnvVariable  string `json:"env_variable"`
	DefaultValue string `json:"default_value"`
	UserViewable any    `json:"user_viewable"`
	UserEditable any    `json:"user_editable"`
	Rules        string `json:"rules"`
	FieldType    string `json:"field_type"`
}

type eggMeta struct {
	Version   string `json:"version"`
	UpdateURL string `json:"update_url"`
}

// ParseEggJSON validates and normalizes an external egg into a Template.
func ParseEggJSON(raw []byte, sourceURL string) (Template, error) {
	raw = bytesTrim(raw)
	if len(raw) == 0 {
		return Template{}, fmt.Errorf("egg is empty")
	}
	if len(raw) > 2<<20 {
		return Template{}, fmt.Errorf("egg is larger than 2 MiB")
	}
	var doc EggDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Template{}, fmt.Errorf("egg JSON is not valid: %w", err)
	}
	name := strings.TrimSpace(doc.Name)
	if name == "" {
		return Template{}, fmt.Errorf("egg is missing a name")
	}
	t := Template{
		Name:          name,
		Summary:       clip(strings.TrimSpace(doc.Description), 400),
		Images:        map[string]string{},
		WorkingDir:    "/home/container",
		SourceURL:     strings.TrimSpace(sourceURL),
		SourceKind:    "egg",
		UpdateURL:     firstNonEmpty(strings.TrimSpace(doc.UpdateURL), strings.TrimSpace(doc.Meta.UpdateURL)),
		Startup:       stringifyStartup(doc.Startup),
		Stop:          stringifyAny(doc.Config.Stop),
		Done:          eggDone(doc.Config.Startup),
		InstallImage:  strings.TrimSpace(doc.Scripts.Installation.Container),
		InstallScript: strings.TrimSpace(doc.Scripts.Installation.Script),
	}
	for label, img := range doc.DockerImages {
		img = strings.TrimSpace(img)
		if img == "" || !validImageRef(img) {
			continue
		}
		t.Images[strings.TrimSpace(label)] = img
		if t.DefaultImage == "" {
			t.DefaultImage = img
		}
	}
	if t.DefaultImage == "" {
		return Template{}, fmt.Errorf("egg has no usable Docker image")
	}
	if t.InstallImage != "" && !validImageRef(t.InstallImage) {
		t.InstallImage = ""
		t.InstallScript = ""
	}
	for _, v := range doc.Variables {
		env := strings.TrimSpace(v.EnvVariable)
		if env == "" {
			continue
		}
		secret := looksSecretName(env) || looksSecretName(v.Name)
		t.Variables = append(t.Variables, Variable{
			Name:        firstNonEmpty(strings.TrimSpace(v.Name), env),
			Env:         env,
			Description: clip(strings.TrimSpace(v.Description), 400),
			Default:     v.DefaultValue,
			Viewable:    asBool(v.UserViewable, true) && !secret,
			Editable:    asBool(v.UserEditable, true),
			Required:    strings.Contains(strings.ToLower(v.Rules), "required"),
			Secret:      secret,
			Rules:       v.Rules,
			FieldType:   firstNonEmpty(v.FieldType, "text"),
		})
	}
	inferTemplateIdentity(&t, doc)
	t.Capabilities = inferCapabilities(t, doc.Features)
	t.ID = "egg-" + slug(t.Game+"-"+t.Implementation+"-"+t.Name) + "-" + shortHash(t.Name+t.DefaultImage)
	if t.DefaultMemoryMB == 0 {
		t.DefaultMemoryMB = 2048
	}
	if t.DefaultDiskMB == 0 {
		t.DefaultDiskMB = 10240
	}
	if t.DefaultCPUs == 0 {
		t.DefaultCPUs = 2
	}
	return t, nil
}

func inferTemplateIdentity(t *Template, doc EggDocument) {
	hay := strings.ToLower(t.Name + " " + t.Summary + " " + strings.Join(doc.Features, " "))
	switch {
	case strings.Contains(hay, "fivem") || strings.Contains(hay, "fxserver") || strings.Contains(hay, "citizenfx"):
		t.Game, t.Implementation, t.Family = "fivem", "fxserver", "fivem"
	case strings.Contains(hay, "garrys") || strings.Contains(hay, "gmod") || strings.Contains(hay, "garry"):
		t.Game, t.Implementation, t.Family = "gmod", "srcds", "source"
	case strings.Contains(hay, "paper"):
		t.Game, t.Implementation, t.Family = "minecraft", "paper", "minecraft"
	case strings.Contains(hay, "purpur"):
		t.Game, t.Implementation, t.Family = "minecraft", "purpur", "minecraft"
	case strings.Contains(hay, "fabric"):
		t.Game, t.Implementation, t.Family = "minecraft", "fabric", "minecraft"
	case strings.Contains(hay, "forge") || strings.Contains(hay, "neoforge"):
		t.Game, t.Implementation, t.Family = "minecraft", "forge", "minecraft"
	case strings.Contains(hay, "spigot") || strings.Contains(hay, "bukkit"):
		t.Game, t.Implementation, t.Family = "minecraft", "spigot", "minecraft"
	case strings.Contains(hay, "vanilla") && strings.Contains(hay, "minecraft"):
		t.Game, t.Implementation, t.Family = "minecraft", "vanilla", "minecraft"
	case strings.Contains(hay, "minecraft"):
		t.Game, t.Implementation, t.Family = "minecraft", "java", "minecraft"
	case strings.Contains(hay, "mindustry"):
		t.Game, t.Implementation, t.Family = "mindustry", "vanilla", "mindustry"
	case strings.Contains(hay, "terraria"):
		t.Game, t.Implementation, t.Family = "terraria", "vanilla", "terraria"
	case strings.Contains(hay, "valheim"):
		t.Game, t.Implementation, t.Family = "valheim", "steamcmd", "steam"
	case strings.Contains(hay, "palworld"):
		t.Game, t.Implementation, t.Family = "palworld", "steamcmd", "steam"
	default:
		t.Game = slug(t.Name)
		t.Implementation = "dedicated"
		t.Family = "generic"
	}
	if strings.Contains(hay, "steamcmd") || strings.Contains(strings.ToLower(t.Startup+t.InstallScript), "steamcmd") {
		if t.Family == "generic" {
			t.Family = "steam"
		}
	}
}

func inferCapabilities(t Template, features []string) []string {
	caps := []string{CapConsole, CapFiles, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules, CapConfig}
	hay := strings.ToLower(t.Name + " " + t.Game + " " + t.Implementation + " " + t.Family + " " + strings.Join(features, " "))
	if strings.Contains(hay, "eula") || t.Family == "minecraft" {
		caps = append(caps, CapEULA, CapJava)
	}
	if t.Family == "minecraft" {
		switch t.Implementation {
		case "paper", "purpur", "spigot", "bukkit":
			caps = append(caps, CapPlugins, CapPlayers, CapWorlds, CapQueries)
		case "fabric", "forge", "neoforge", "quilt":
			caps = append(caps, CapMods, CapPlayers, CapWorlds, CapQueries)
		default:
			caps = append(caps, CapPlayers, CapWorlds, CapQueries)
		}
	}
	if t.Family == "fivem" {
		caps = append(caps, CapPlugins, CapPlayers, CapLicenseKey, CapQueries)
	}
	if t.Family == "source" || t.Game == "gmod" {
		caps = append(caps, CapWorkshop, CapSteamCMD, CapPlayers, CapQueries)
	}
	if t.Family == "steam" || strings.Contains(hay, "steamcmd") {
		caps = append(caps, CapSteamCMD)
	}
	if t.Game == "mindustry" {
		caps = append(caps, CapPlayers, CapQueries)
	}
	if t.Game == "terraria" {
		caps = append(caps, CapWorlds, CapPlayers)
	}
	for _, f := range features {
		switch canon(f) {
		case "eula":
			caps = append(caps, CapEULA)
		case "java_version":
			caps = append(caps, CapJava)
		}
	}
	return normalizeCapabilities(caps)
}

func stringifyStartup(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		for _, key := range []string{"DEFAULT", "default"} {
			if s, ok := x[key].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		for _, s := range x {
			if str, ok := s.(string); ok && strings.TrimSpace(str) != "" {
				return strings.TrimSpace(str)
			}
		}
	}
	return ""
}

func stringifyAny(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func eggDone(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["done"].(string); ok {
			return s
		}
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(x), &m) == nil {
			if s, ok := m["done"].(string); ok {
				return s
			}
		}
	}
	return ""
}

func asBool(v any, fallback bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no":
			return false
		}
	case float64:
		return x != 0
	}
	return fallback
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func validImageRef(img string) bool {
	img = strings.TrimSpace(img)
	if img == "" || strings.ContainsAny(img, " \t\n\r") {
		return false
	}
	if strings.Contains(img, "://") {
		return false
	}
	return true
}
