package gameserver

import "strconv"

// Builders keep the large catalogue declarative. Each one only fills the
// fields every template of that family shares; per-game facts (app IDs,
// executables, ports, arguments) are always written out at the call site.

const workDir = "/home/container"

// steamServer completes a SteamCMD-installed Linux dedicated server: the
// SteamCMD image and installer, the locked app ID variable and the optional
// Steam login fields. t.Variables are kept after the app ID.
func steamServer(t Template, appID, appHelp string) Template {
	if t.Game == "" {
		t.Game = gameKey(t.ID)
	}
	if t.Family == "" {
		t.Family = "steam"
	}
	if t.Implementation == "" {
		t.Implementation = "steamcmd"
	}
	if t.Engine == "" {
		t.Engine = "custom"
	}
	if len(t.Images) == 0 {
		t.Images = steamImage()
		t.DefaultImage = "steamcmd/steamcmd:debian"
	}
	if t.DefaultImage == "" {
		t.DefaultImage = "steamcmd/steamcmd:debian"
	}
	t.InstallBuiltin = "steamcmd"
	if t.WorkingDir == "" {
		t.WorkingDir = workDir
	}
	t.Capabilities = normalizeCapabilities(append(baseCaps(CapSteamCMD), t.Capabilities...))
	vars := []Variable{steamAppVar(appID, appHelp)}
	vars = append(vars, t.Variables...)
	vars = append(vars, steamLoginVars()...)
	t.Variables = vars
	return t
}

// requireSteamOwner marks a SteamCMD template whose files only download for
// an account that owns the game. Creation is refused without STEAM_USER.
func requireSteamOwner(t Template, label string) Template {
	t.Requirements = append(t.Requirements, Requirement{
		Kind: ReqSteamAccount, Stage: StageInstall, Env: "STEAM_USER",
		Label: label, URL: "https://store.steampowered.com/",
	})
	for i := range t.Variables {
		if t.Variables[i].Env == "STEAM_USER" {
			t.Variables[i].Required = true
			t.Variables[i].Description = "Steam account that owns the game. Anonymous SteamCMD cannot download this server. Use a dedicated account with Steam Guard email codes."
		}
	}
	if t.Hint == "" {
		t.Hint = "Steam account that owns the game required"
	}
	return t
}

// gsltOptional documents a Valve Game Server Login Token that is only needed
// for public listing, bound to STEAM_TOKEN.
func gsltOptional(appID string) Requirement {
	return Requirement{
		Kind: ReqGSLT, Stage: StageOptional, Env: "STEAM_TOKEN",
		Label: "Steam Game Server Login Token for app " + appID + " to list the server publicly",
		URL:   "https://steamcommunity.com/dev/managegameservers",
	}
}

// SourceGame is the compact description of a Source (srcds) or GoldSrc
// (hlds) game. Everything a player would recognise is explicit.
type SourceGame struct {
	ID, Name, GameTitle, Summary string
	// AppID is the dedicated server app. GoldSrc mods on app 90 also set
	// Mod, which becomes +app_set_config 90 mod <Mod>.
	AppID      string
	GameDir    string
	Mod        string
	Map        string
	MaxPlayers int
	Category   string
	Tags       []string
	Aliases    []string
	MemoryMB   int
	DiskMB     int
	// Binary overrides srcds_run / hlds_run (for example srcds_run_64).
	Binary string
	// ExtraArgs are appended before the +hostname argument.
	ExtraArgs string
	// GSLT marks Valve titles where public listing needs a token.
	GSLT         bool
	Dependencies []string
	SourceRef    string
	DocsURL      string
	Notes        []string
	Beta         string
}

func sourceVars(g SourceGame) []Variable {
	vars := []Variable{
		envText("Server name", "SERVER_NAME", "Hostname in the server browser.", "No-DAL "+g.Name, true),
		envText("Start map", "MAP", "Map loaded at boot.", g.Map, true),
		envNumber("Max players", "MAX_PLAYERS", "Slot count.", strconv.Itoa(g.MaxPlayers), true),
		envNumber("Game port", "SERVER_PORT", "UDP game port. RCON uses the same TCP port.", "27015", true),
	}
	if g.GSLT {
		vars = append(vars, envSecret("GSLT token", "STEAM_TOKEN", "Only needed to list the server publicly. Create one at steamcommunity.com/dev/managegameservers for app "+g.AppID+".", false))
	}
	return vars
}

func sourcePorts() []Port {
	return []Port{
		{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "rcon", ContainerPort: 27015, Protocol: "tcp", Env: "SERVER_PORT"},
	}
}

func sourceCommon(g SourceGame, family, engine string) Template {
	t := Template{
		ID: g.ID, Name: g.Name, Game: gameKey(g.ID), GameTitle: g.GameTitle,
		Implementation: family, Family: "source", Summary: g.Summary,
		Category: g.Category, Engine: engine,
		Capabilities: []string{CapPlayers, CapQueries, CapRCON},
		Stop:         "quit", Done: "VAC secure mode",
		DefaultPorts:    sourcePorts(),
		DefaultMemoryMB: orInt(g.MemoryMB, 1024), DefaultDiskMB: orInt(g.DiskMB, 8192), DefaultCPUs: 2,
		Tags: g.Tags, Aliases: g.Aliases, Dependencies: g.Dependencies,
		SourceRef: g.SourceRef, DocsURL: g.DocsURL, Notes: g.Notes,
		Variables: sourceVars(g),
		FriendlyConfig: []Setting{
			{ID: "hostname", Label: "Server name", Help: "Shown in the server browser.", Kind: "text", Env: "SERVER_NAME", Restart: true},
			{ID: "map", Label: "Start map", Help: "Map loaded at boot.", Kind: "text", Env: "MAP", Restart: true},
			{ID: "slots", Label: "Player slots", Help: "How many clients can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
		},
	}
	if g.GSLT {
		t.Requirements = append(t.Requirements, gsltOptional(g.AppID))
		t.Hint = "GSLT only to list publicly"
	}
	if g.Beta != "" {
		t.Install = &InstallSpec{Beta: g.Beta}
	}
	return t
}

// srcdsTemplate builds a Source engine dedicated server run with srcds_run.
func srcdsTemplate(g SourceGame) Template {
	t := sourceCommon(g, "srcds", "source")
	bin := g.Binary
	if bin == "" {
		bin = "srcds_run"
	}
	startup := "./" + bin + " -game " + g.GameDir + " -console -usercon -norestart -port {{SERVER_PORT}} +maxplayers {{MAX_PLAYERS}} +map {{MAP}}"
	if g.ExtraArgs != "" {
		startup += " " + g.ExtraArgs
	}
	startup += " +hostname \"{{SERVER_NAME}}\""
	if g.GSLT {
		startup += " ${STEAM_TOKEN:++sv_setsteamaccount $STEAM_TOKEN}"
	}
	t.Startup = startup
	t.ConfigFiles = []ConfigFile{{Path: g.GameDir + "/cfg/server.cfg", Format: "cfg", Restart: true, Parser: "cfg"}}
	return steamServer(t, g.AppID, g.GameTitle+" dedicated server.")
}

// goldsrcTemplate builds a GoldSrc (Half-Life engine) server run with
// hlds_run. Mods of app 90 need app_set_config, and SteamCMD often needs
// more than one pass for app 90, which the SteamCMD installer retries.
func goldsrcTemplate(g SourceGame) Template {
	t := sourceCommon(g, "hlds", "goldsrc")
	t.Done = "Connection to Steam servers successful"
	bin := g.Binary
	if bin == "" {
		bin = "hlds_run"
	}
	startup := "./" + bin + " -game " + g.GameDir + " -console -norestart -port {{SERVER_PORT}} +maxplayers {{MAX_PLAYERS}} +map {{MAP}}"
	if g.ExtraArgs != "" {
		startup += " " + g.ExtraArgs
	}
	t.Startup = startup + " +hostname \"{{SERVER_NAME}}\""
	if g.Mod != "" {
		if t.Install == nil {
			t.Install = &InstallSpec{}
		}
		t.Install.AppConfig = "90 mod " + g.Mod
	}
	t.ConfigFiles = []ConfigFile{{Path: g.GameDir + "/server.cfg", Format: "cfg", Restart: true, Parser: "cfg"}}
	return steamServer(t, g.AppID, g.GameTitle+" dedicated server (HLDS).")
}

// nativeServer fills defaults for a Linux server installed by the download
// or github-release installer and run on a plain Debian image.
func nativeServer(t Template) Template {
	if len(t.Images) == 0 {
		t.Images = map[string]string{"Debian 12": "debian:bookworm-slim"}
		t.DefaultImage = "debian:bookworm-slim"
	}
	if t.WorkingDir == "" {
		t.WorkingDir = workDir
	}
	if t.Implementation == "" {
		t.Implementation = "native"
	}
	if t.Family == "" {
		t.Family = t.Game
	}
	t.Capabilities = normalizeCapabilities(append(baseCaps(), t.Capabilities...))
	return t
}

// minecraftJava completes a Minecraft Java Edition server or proxy. Proxies
// pass proxy=true and get no EULA, worlds or server.properties.
func minecraftJava(t Template, proxy bool) Template {
	t.Game = "minecraft"
	t.Family = "minecraft"
	if t.GameTitle == "" {
		t.GameTitle = "Minecraft: Java Edition"
	}
	if t.Engine == "" {
		t.Engine = "java"
	}
	if t.Category == "" {
		t.Category = "minecraft"
		if proxy {
			t.Category = "proxy"
		}
	}
	if len(t.Images) == 0 {
		t.Images = javaImages()
	}
	if t.DefaultImage == "" {
		t.DefaultImage = "eclipse-temurin:21-jre"
	}
	if t.WorkingDir == "" {
		t.WorkingDir = workDir
	}
	if t.Stop == "" {
		t.Stop = "stop"
	}
	if t.Done == "" {
		t.Done = "Done"
	}
	if len(t.Architectures) == 0 {
		t.Architectures = []string{"amd64", "arm64"}
	}
	extra := []string{CapJava}
	if !proxy {
		extra = append(extra, CapWorlds, CapEULA, CapPlayers)
	}
	t.Capabilities = normalizeCapabilities(append(baseCaps(extra...), t.Capabilities...))
	if len(t.DefaultPorts) == 0 {
		t.DefaultPorts = []Port{{Name: "game", ContainerPort: 25565, Protocol: "tcp", Primary: true, Fixed: true}}
	}
	if t.DefaultMemoryMB == 0 {
		t.DefaultMemoryMB = 2048
	}
	if t.DefaultDiskMB == 0 {
		t.DefaultDiskMB = 8192
	}
	if t.DefaultCPUs == 0 {
		t.DefaultCPUs = 2
	}
	if t.MinMemoryMB == 0 {
		t.MinMemoryMB = 1024
	}
	if !hasVar(t.Variables, "SERVER_MEMORY") {
		heap := strconv.Itoa(t.DefaultMemoryMB * 3 / 4)
		t.Variables = append(t.Variables, envNumber("Server memory (MiB)", "SERVER_MEMORY", "Java heap size. Keep this at or below the RAM you allocate.", heap, true))
	}
	if !proxy {
		if !hasVar(t.Variables, "EULA") {
			t.Variables = append(t.Variables, minecraftEULAVar())
		}
		t.Requirements = append(t.Requirements, Requirement{Kind: ReqEULA, Stage: StageStart, Env: "EULA", Label: "Accept the Minecraft EULA", URL: "https://aka.ms/MinecraftEULA"})
		if len(t.ConfigFiles) == 0 {
			t.ConfigFiles = []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}}
		}
		if len(t.FriendlyConfig) == 0 {
			t.FriendlyConfig = minecraftWorldSettings()
		}
	}
	return t
}

func hasVar(vars []Variable, env string) bool {
	for _, v := range vars {
		if v.Env == env {
			return true
		}
	}
	return false
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func gameKey(id string) string {
	if len(id) > 4 && id[:4] == "ndl-" {
		return id[4:]
	}
	return id
}
