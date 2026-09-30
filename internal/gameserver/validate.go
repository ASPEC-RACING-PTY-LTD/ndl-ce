package gameserver

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Categories are the catalogue sections. Tags carry finer detail.
var Categories = []string{
	"minecraft", "proxy", "survival", "sandbox", "shooter", "tactical",
	"simulation", "strategy", "racing", "rpg", "roleplay", "party", "sports",
}

// Engines are the engine/runtime families a template may declare.
var Engines = []string{
	"source", "source2", "goldsrc", "idtech2", "idtech3", "idtech4", "quake",
	"unreal", "unreal2", "unreal3", "unreal4", "unreal5", "unity", "java",
	"bedrock", "dotnet", "godot", "cryengine", "enfusion", "real-virtuality",
	"custom",
}

// RuntimeImages is the allowlist of container images builtin templates may
// reference. Each entry is a public image that was pulled and inspected
// when it was added, so a template cannot point at a typo or a private
// image.
var RuntimeImages = map[string]string{
	"steamcmd/steamcmd:debian":              "Debian 13 with SteamCMD and 32-bit libstdc++",
	"debian:bookworm-slim":                  "Debian 12 slim",
	"alpine:3.21":                           "Alpine 3.21",
	"eclipse-temurin:8-jre":                 "Eclipse Temurin Java 8",
	"eclipse-temurin:11-jre":                "Eclipse Temurin Java 11",
	"eclipse-temurin:17-jre":                "Eclipse Temurin Java 17",
	"eclipse-temurin:21-jre":                "Eclipse Temurin Java 21",
	"eclipse-temurin:25-jre":                "Eclipse Temurin Java 25",
	"eclipse-temurin:21-jdk":                "Eclipse Temurin Java 21 JDK (Spigot BuildTools compiles)",
	"eclipse-temurin:25-jdk":                "Eclipse Temurin Java 25 JDK (Spigot BuildTools compiles)",
	"mcr.microsoft.com/dotnet/runtime:8.0":  "Microsoft .NET 8 runtime",
	"mcr.microsoft.com/dotnet/runtime:9.0":  "Microsoft .NET 9 runtime (Debian 12)",
	"mcr.microsoft.com/dotnet/runtime:10.0": "Microsoft .NET 10 runtime (Ubuntu 24.04)",
}

func pkgSet(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// KnownPackages are Debian package names that exist in the release behind
// each base image (Debian 13 trixie for steamcmd/steamcmd:debian, Debian 12
// bookworm for debian:bookworm-slim). Templates may only layer these, so a
// renamed package (the trixie t64 transition) cannot break start.
var KnownPackages = map[string]map[string]bool{
	"steamcmd/steamcmd:debian": pkgSet(
		"ca-certificates", "curl", "unzip", "xz-utils", "bzip2", "procps", "tzdata",
		"lib32gcc-s1", "lib32stdc++6", "lib32z1", "zlib1g", "libc6:i386", "libstdc++6:i386",
		"libsdl2-2.0-0", "libsdl2-2.0-0:i386", "libatomic1", "libgomp1",
		"libncurses6", "libtinfo6", "libicu76", "libssl3t64", "libcurl4t64",
		"libpng16-16t64", "libfreetype6", "libgdiplus", "libxi6", "libxrandr2", "libxcursor1",
		"libvorbisfile3", "libopenal1", "libjpeg62-turbo", "libglu1-mesa", "libgl1",
		"libssl3t64:i386", "zlib1g:i386", "libncursesw6", "libtbb12",
		// Listed by LinuxGSM lgsm/data/debian-13.csv for specific games.
		"speex", "speex:i386", "libc++1", "xvfb",
	),
	"debian:bookworm-slim": pkgSet(
		"ca-certificates", "curl", "unzip", "xz-utils", "bzip2", "procps", "tzdata",
		"lib32gcc-s1", "lib32stdc++6", "lib32z1", "zlib1g", "libc6:i386", "libstdc++6:i386",
		"libsdl2-2.0-0", "libsdl2-2.0-0:i386", "libatomic1", "libgomp1",
		"libncurses6", "libtinfo6", "libicu72", "libssl3", "libcurl4", "libcurl3-gnutls",
		"libpng16-16", "libfreetype6", "libgdiplus", "libxi6", "libxrandr2", "libxcursor1",
		"libvorbisfile3", "libopenal1", "libjpeg62-turbo", "libglu1-mesa", "libgl1",
		"libkrb5-3", "libgssapi-krb5-2", "liblttng-ust1", "libunwind8", "libsqlite3-0",
		"libzip4", "libfontconfig1", "libxml2", "libncursesw6",
	),
}

var (
	templateIDPattern = regexp.MustCompile(`^ndl-[a-z0-9][a-z0-9-]{1,62}$`)
	envNamePattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	placeholderRe     = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)
)

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ValidateTemplate checks a builtin template against the catalogue rules.
// It returns every problem rather than stopping at the first.
func ValidateTemplate(t Template) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{t.ID}, args...)...))
	}
	if !templateIDPattern.MatchString(t.ID) {
		add("id must match %s", templateIDPattern)
	}
	for field, v := range map[string]string{"name": t.Name, "game": t.Game, "game_title": t.GameTitle, "summary": t.Summary, "startup": t.Startup, "stop": t.Stop, "working_dir": t.WorkingDir, "default_image": t.DefaultImage, "family": t.Family, "implementation": t.Implementation} {
		if strings.TrimSpace(v) == "" {
			add("%s is required", field)
		}
	}
	if !inList(Categories, t.Category) {
		add("category %q is not one of %v", t.Category, Categories)
	}
	if !inList(Engines, t.Engine) {
		add("engine %q is not one of %v", t.Engine, Engines)
	}
	if strings.Contains(t.Summary, "\u2014") || strings.Contains(t.Name, "\u2014") {
		add("em dash characters are not allowed")
	}
	inst, ok := lookupInstaller(t.InstallBuiltin)
	if !ok {
		add("installer %q is not registered", t.InstallBuiltin)
	} else if inst.Validate != nil {
		if err := inst.Validate(t); err != nil {
			add("installer: %v", err)
		}
	}
	if script, image := installPlan(t); script == "" || image == "" {
		add("install plan is empty")
	} else if !strings.HasPrefix(script, "#!/bin/sh") {
		add("install plan must be a /bin/sh script")
	}
	// Images.
	if _, ok := RuntimeImages[t.DefaultImage]; !ok {
		add("default image %q is not in RuntimeImages", t.DefaultImage)
	}
	foundDefault := false
	for label, img := range t.Images {
		if _, ok := RuntimeImages[img]; !ok {
			add("image %s=%q is not in RuntimeImages", label, img)
		}
		if img == t.DefaultImage {
			foundDefault = true
		}
	}
	if !foundDefault {
		add("default image is not one of images")
	}
	if t.InstallImage != "" {
		if _, ok := RuntimeImages[t.InstallImage]; !ok {
			add("install image %q is not in RuntimeImages", t.InstallImage)
		}
	}
	for _, a := range t.Arches() {
		if a != "amd64" && a != "arm64" {
			add("architecture %q is not supported", a)
		}
	}
	if t.InstallBuiltin == "steamcmd" && inList(t.Arches(), "arm64") {
		add("SteamCMD servers are amd64 only")
	}
	for _, p := range t.Dependencies {
		if !debianPackagePattern.MatchString(p) {
			add("dependency %q is not a Debian package name", p)
		}
		if known := KnownPackages[t.DefaultImage]; known == nil || !known[p] {
			add("dependency %q is not in KnownPackages for %s", p, t.DefaultImage)
		}
	}
	// Variables.
	vars := map[string]Variable{}
	for _, v := range t.Variables {
		if !envNamePattern.MatchString(v.Env) {
			add("variable env %q is not UPPER_SNAKE", v.Env)
		}
		if _, dup := vars[v.Env]; dup {
			add("variable %s is declared twice", v.Env)
		}
		vars[v.Env] = v
		if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Description) == "" {
			add("variable %s needs a name and description", v.Env)
		}
		if v.Secret && v.Default != "" {
			add("secret variable %s must not ship a default value", v.Env)
		}
		if v.FieldType == "number" && v.Default != "" {
			if _, err := strconv.Atoi(v.Default); err != nil {
				add("number variable %s default %q is not a number", v.Env, v.Default)
			}
		}
		if v.FieldType == "select" {
			if len(v.Options) == 0 || (v.Default != "" && !inList(v.Options, v.Default)) {
				add("select variable %s needs options containing its default", v.Env)
			}
		}
		if v.Generate != "" && v.Generate != "password" {
			add("variable %s generate %q is not supported", v.Env, v.Generate)
		}
		switch v.FieldType {
		case "", "text", "number", "toggle", "password", "select":
		default:
			add("variable %s field type %q is not supported", v.Env, v.FieldType)
		}
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(t.Startup, -1) {
		name := m[1]
		if name == "SERVER_MEMORY" || name == "SERVER_PORT" {
			continue
		}
		if _, ok := vars[name]; !ok {
			add("startup uses {{%s}} but no variable declares it", name)
		}
	}
	if strings.Contains(t.Startup, "{{SERVER_PORT}}") {
		if _, ok := vars["SERVER_PORT"]; !ok {
			primaryHasEnv := false
			for _, p := range t.DefaultPorts {
				if p.Primary {
					primaryHasEnv = p.Env != ""
				}
			}
			if primaryHasEnv {
				add("startup uses {{SERVER_PORT}} and the primary port names an env that is not declared")
			}
		}
	}
	if strings.Contains(t.Startup, "\n") {
		add("startup must be one line")
	}
	// Secrets reach the server as environment variables. Inlining them with
	// {{VAR}} would put them in the container's argv (docker inspect, ps).
	for _, m := range placeholderRe.FindAllStringSubmatch(t.Startup, -1) {
		if v, ok := vars[m[1]]; ok && v.Secret {
			add("startup inlines secret {{%s}}; reference it as \"$%s\"", m[1], m[1])
		}
	}
	// Ports.
	if len(t.DefaultPorts) == 0 {
		add("at least one port is required")
	}
	primaries := 0
	seenPort := map[string]bool{}
	for _, p := range t.DefaultPorts {
		if p.Primary {
			primaries++
		}
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			add("port %d out of range", p.ContainerPort)
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			add("port %d protocol %q must be tcp or udp", p.ContainerPort, p.Protocol)
		}
		key := strconv.Itoa(p.ContainerPort) + "/" + p.Protocol
		if seenPort[key] {
			add("port %s is declared twice", key)
		}
		seenPort[key] = true
		if strings.TrimSpace(p.Name) == "" {
			add("port %s needs a name", key)
		}
		if p.Env != "" {
			v, ok := vars[p.Env]
			if !ok {
				add("port %s binds env %s which is not declared", key, p.Env)
			} else if v.Default != strconv.Itoa(p.ContainerPort) {
				add("port %s env %s default %q must equal the port", key, p.Env, v.Default)
			}
		}
	}
	if primaries != 1 {
		add("exactly one primary port is required, found %d", primaries)
	}
	// Resources.
	if t.DefaultMemoryMB < 256 || t.DefaultMemoryMB > 65536 {
		add("default memory %d MiB is out of range", t.DefaultMemoryMB)
	}
	if t.MinMemoryMB > t.DefaultMemoryMB {
		add("minimum memory is larger than the default")
	}
	if t.DefaultDiskMB < 512 || t.DefaultDiskMB > 262144 {
		add("default disk %d MiB is out of range", t.DefaultDiskMB)
	}
	if t.DefaultCPUs < 1 || t.DefaultCPUs > 32 {
		add("default CPUs %d is out of range", t.DefaultCPUs)
	}
	// Capabilities.
	for _, c := range t.Capabilities {
		if !knownCapability(c) {
			add("capability %q is unknown", c)
		}
	}
	if hasCap(t.Capabilities, CapSteamCMD) != (t.InstallBuiltin == "steamcmd") {
		add("steamcmd capability and installer disagree")
	}
	if hasCap(t.Capabilities, CapEULA) {
		if _, ok := vars["EULA"]; !ok {
			add("eula capability needs an EULA variable")
		}
	}
	if hasCap(t.Capabilities, CapJava) && !strings.HasPrefix(t.DefaultImage, "eclipse-temurin:") {
		add("java capability needs a Temurin image")
	}
	// Requirements.
	for _, r := range t.Requirements {
		switch r.Kind {
		case ReqSteamAccount, ReqGSLT, ReqLicenseKey, ReqToken, ReqAPIKey, ReqEULA, ReqPurchase:
		default:
			add("requirement kind %q is unknown", r.Kind)
		}
		switch r.Stage {
		case StageInstall, StageStart, StageOptional:
		default:
			add("requirement stage %q is unknown", r.Stage)
		}
		if r.Env != "" {
			if _, ok := vars[r.Env]; !ok {
				add("requirement names env %s which is not declared", r.Env)
			}
		}
		if strings.TrimSpace(r.Label) == "" {
			add("requirement needs a label")
		}
		if r.URL != "" && !strings.HasPrefix(r.URL, "https://") {
			add("requirement URL must be https")
		}
	}
	for _, env := range t.StartRequires {
		if _, ok := vars[env]; !ok {
			add("start_requires names env %s which is not declared", env)
		}
	}
	// Config.
	files := map[string]bool{}
	for _, f := range t.ConfigFiles {
		if _, err := cleanArchiveRel(f.Path); err != nil || strings.HasPrefix(f.Path, "/") {
			add("config file %q is not a safe relative path", f.Path)
		}
		files[f.Path] = true
	}
	for _, s := range t.FriendlyConfig {
		if s.Env != "" {
			if _, ok := vars[s.Env]; !ok {
				add("setting %s names env %s which is not declared", s.ID, s.Env)
			}
		}
		if s.File != "" && !files[s.File] {
			add("setting %s names file %s which is not a config file", s.ID, s.File)
		}
	}
	for _, a := range t.Aliases {
		if a != strings.ToLower(a) {
			add("alias %q must be lower case", a)
		}
	}
	for field, raw := range map[string]string{"docs_url": t.DocsURL, "source_ref": t.SourceRef} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			add("%s must be an https URL", field)
		}
	}
	return errs
}

// CheckStartRequirements reports the first unmet start-stage requirement.
func CheckStartRequirements(t Template, env map[string]string) error {
	for _, req := range t.RequirementsAt(StageStart) {
		if req.Env == "" {
			continue
		}
		val := strings.TrimSpace(env[req.Env])
		if req.Kind == ReqEULA {
			if !strings.EqualFold(val, "true") {
				return fmt.Errorf("eula is not accepted")
			}
			continue
		}
		if val == "" {
			switch req.Env {
			case "CLUSTER_TOKEN":
				return fmt.Errorf("klei cluster token is required")
			case "FIVEM_LICENSE":
				return fmt.Errorf("fivem license key is required")
			}
			return fmt.Errorf("%s is required to start: %s", req.Env, req.Label)
		}
	}
	return nil
}

// CheckInstallRequirements is the exported install-stage check used by the
// API before a server row is created.
func CheckInstallRequirements(t Template, env map[string]string) error {
	return checkInstallRequirements(t, env)
}

// registerRuntimeImage adds an image to the allowlist from a catalogue file.
// Only call it for an image that was pulled and inspected successfully.
func registerRuntimeImage(ref, description string) {
	if !validImageRef(ref) {
		panic("invalid runtime image " + ref)
	}
	RuntimeImages[ref] = description
}
