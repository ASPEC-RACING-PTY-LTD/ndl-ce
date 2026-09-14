package gameserver

// BuiltinTemplates are No-DAL-owned blueprints. They are not copies of
// upstream egg JSON. Install paths use documented public APIs and images.
func BuiltinTemplates() []Template {
	return []Template{
		minecraftPaperTemplate(),
		minecraftVanillaTemplate(),
		minecraftFabricTemplate(),
		minecraftPurpurTemplate(),
		velocityTemplate(),
		steamcmdValheimTemplate(),
		fivemTemplate(),
		gmodTemplate(),
		mindustryTemplate(),
		terrariaTemplate(),
		factorioTemplate(),
		rustTemplate(),
		palworldTemplate(),
		zomboidTemplate(),
		sevenDaysTemplate(),
		cs2Template(),
		satisfactoryTemplate(),
		dstTemplate(),
		unturnedTemplate(),
		l4d2Template(),
		arkTemplate(),
		conanTemplate(),
		dayzTemplate(),
		arma3Template(),
		tf2Template(),
	}
}

func minecraftPaperTemplate() Template {
	return Template{
		ID:             "ndl-minecraft-paper",
		Name:           "Minecraft Paper",
		Game:           "minecraft",
		Implementation: "paper",
		Family:         "minecraft",
		Summary:        "Paper dedicated server. Downloads the selected Paper build and accepts the Minecraft EULA for you when you confirm it.",
		Capabilities: normalizeCapabilities([]string{
			CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules,
			CapPlugins, CapPlayers, CapWorlds, CapEULA, CapJava, CapQueries,
		}),
		Images:          map[string]string{"Java 21": "eclipse-temurin:21-jre"},
		DefaultImage:    "eclipse-temurin:21-jre",
		Startup:         "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}} --nogui",
		Stop:            "stop",
		Done:            "Done",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "paper",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 25565, Protocol: "tcp", Primary: true}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   8192,
		DefaultCPUs:     2,
		Tags:            []string{"minecraft", "survival", "plugins"},
		Aliases:         []string{"minecraft", "paper", "mc", "papermc", "java"},
		Variables: []Variable{
			{Name: "Minecraft version", Env: "MC_VERSION", Description: "Paper game version, for example 1.21.10.", Default: "1.21.10", Viewable: true, Editable: true, Required: true, FieldType: "text"},
			{Name: "Build", Env: "BUILD_NUMBER", Description: "Paper build number. latest picks the newest build for the version.", Default: "latest", Viewable: true, Editable: true, FieldType: "text"},
			{Name: "Server jar", Env: "SERVER_JARFILE", Description: "Jar file name inside the server folder.", Default: "server.jar", Viewable: true, Editable: true, Required: true, FieldType: "text"},
			{Name: "Server memory (MiB)", Env: "SERVER_MEMORY", Description: "Java heap size. Keep this at or below the RAM you allocate.", Default: "1536", Viewable: true, Editable: true, Required: true, FieldType: "number"},
			{Name: "Accept EULA", Env: "EULA", Description: "Must be true to start. This writes eula.txt.", Default: "false", Viewable: true, Editable: true, Required: true, FieldType: "toggle"},
		},
		ConfigFiles: []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}},
		FriendlyConfig: []Setting{
			{ID: "motd", Label: "Server name shown in the list", Help: "The text friends see before they join.", Kind: "text", File: "server.properties", Key: "motd", Default: "A Minecraft Server", Restart: true},
			{ID: "max-players", Label: "Player limit", Help: "How many people can be connected at once.", Kind: "number", File: "server.properties", Key: "max-players", Default: "20", Min: 1, Max: 200, Restart: true},
			{ID: "difficulty", Label: "Difficulty", Help: "How hard the world is.", Kind: "select", File: "server.properties", Key: "difficulty", Default: "easy", Options: []string{"peaceful", "easy", "normal", "hard"}, Restart: true},
			{ID: "gamemode", Label: "Default game mode", Help: "New players start in this mode.", Kind: "select", File: "server.properties", Key: "gamemode", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, Restart: true},
			{ID: "white-list", Label: "Whitelist", Help: "Only approved players can join.", Kind: "toggle", File: "server.properties", Key: "white-list", Default: "false", Restart: true},
			{ID: "pvp", Label: "Player versus player", Help: "Allow players to hurt each other.", Kind: "toggle", File: "server.properties", Key: "pvp", Default: "true", Restart: false},
			{ID: "online-mode", Label: "Online mode", Help: "Check players against Minecraft accounts. Turn off only for a private LAN-style server.", Kind: "toggle", File: "server.properties", Key: "online-mode", Default: "true", Restart: true, Advanced: true},
			{ID: "level-name", Label: "World folder", Help: "Name of the world directory.", Kind: "text", File: "server.properties", Key: "level-name", Default: "world", Restart: true},
			{ID: "level-seed", Label: "World seed", Help: "Leave blank for a random world.", Kind: "text", File: "server.properties", Key: "level-seed", Restart: true},
			{ID: "view-distance", Label: "View distance", Help: "How far the world loads around each player. Higher uses more RAM.", Kind: "number", File: "server.properties", Key: "view-distance", Default: "10", Min: 3, Max: 32, Restart: true},
		},
		Content: ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "plugins", Loader: "paper", GameID: "minecraft", Dependencies: true},
	}
}

func steamcmdValheimTemplate() Template {
	return Template{
		ID:             "ndl-valheim",
		Name:           "Valheim",
		Game:           "valheim",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Valheim dedicated server installed with SteamCMD. No Steam account is required for the anonymous install.",
		Capabilities: normalizeCapabilities([]string{
			CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules, CapSteamCMD, CapPlayers, CapWorlds,
		}),
		Images:          map[string]string{"Debian": "steamcmd/steamcmd:debian"},
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./valheim_server.x86_64 -name \"{{SERVER_NAME}}\" -port {{SERVER_PORT}} -world \"{{WORLD}}\" -password \"{{SERVER_PASSWORD}}\" -public {{PUBLIC}}",
		Stop:            "^C",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 2456, Protocol: "udp", Primary: true}, {Name: "query", ContainerPort: 2457, Protocol: "udp"}},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   12288,
		DefaultCPUs:     2,
		Tags:            []string{"survival", "steam", "coop"},
		Aliases:         []string{"valheim", "vh", "val"},
		Variables: []Variable{
			{Name: "Steam app ID", Env: "SRCDS_APPID", Description: "Valheim dedicated server app.", Default: "896660", Viewable: true, Editable: false, Required: true},
			{Name: "Server name", Env: "SERVER_NAME", Description: "Name shown in the in-game browser.", Default: "No-DAL Valheim", Viewable: true, Editable: true, Required: true},
			{Name: "World", Env: "WORLD", Description: "World save name.", Default: "Dedicated", Viewable: true, Editable: true, Required: true},
			{Name: "Join password", Env: "SERVER_PASSWORD", Description: "Must be at least 5 characters. Valheim requires a password.", Default: "secret", Viewable: false, Editable: true, Required: true, Secret: true, FieldType: "password"},
			{Name: "Game port", Env: "SERVER_PORT", Description: "UDP game port.", Default: "2456", Viewable: true, Editable: true, Required: true, FieldType: "number"},
			{Name: "Listed publicly", Env: "PUBLIC", Description: "1 lists the server. 0 keeps it unlisted.", Default: "1", Viewable: true, Editable: true, FieldType: "number"},
		},
		FriendlyConfig: []Setting{
			{ID: "name", Label: "Server name", Help: "Shown to players in the join list.", Kind: "text", Env: "SERVER_NAME", Restart: true},
			{ID: "world", Label: "World name", Help: "Save folder for this world.", Kind: "text", Env: "WORLD", Restart: true},
			{ID: "public", Label: "Public listing", Help: "Show the server in the community list.", Kind: "toggle", Env: "PUBLIC", Restart: true},
		},
		Content: ContentSpec{},
	}
}

func fivemTemplate() Template {
	return Template{
		ID:             "ndl-fivem",
		Name:           "FiveM FXServer",
		Game:           "fivem",
		Implementation: "fxserver",
		Family:         "fivem",
		Summary:        "CitizenFX FXServer. A Cfx.re license key is required before the server can start.",
		Capabilities: normalizeCapabilities([]string{
			CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules,
			CapPlugins, CapPlayers, CapLicenseKey, CapQueries,
		}),
		Images:          map[string]string{"Alpine": "alpine:3.21"},
		DefaultImage:    "alpine:3.21",
		Startup:         "./run.sh +exec server.cfg",
		Stop:            "quit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "fivem",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 30120, Protocol: "udp", Primary: true}, {Name: "http", ContainerPort: 40120, Protocol: "tcp"}},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   16384,
		DefaultCPUs:     2,
		Tags:            []string{"gta", "roleplay", "fivem"},
		Aliases:         []string{"fivem", "fxserver", "cfx", "gta", "gtav", "five m"},
		Hint:            "Cfx.re key required to start",
		Variables: []Variable{
			{Name: "License key", Env: "FIVEM_LICENSE", Description: "Key from portal.cfx.re. You can install without it. Start requires a real key.", Default: "", Viewable: true, Editable: true, Required: false, Secret: true, FieldType: "password"},
			{Name: "Server name", Env: "SERVER_NAME", Description: "Name in the FiveM server list.", Default: "No-DAL FiveM", Viewable: true, Editable: true, Required: true},
			{Name: "Max clients", Env: "MAX_CLIENTS", Description: "Player slots. The license decides the real cap.", Default: "32", Viewable: true, Editable: true, FieldType: "number"},
			{Name: "Artifact", Env: "FIVEM_ARTIFACT", Description: "FXServer recommended artifact, or latest.", Default: "latest", Viewable: true, Editable: true},
		},
		ConfigFiles: []ConfigFile{{Path: "server.cfg", Format: "cfg", Restart: true, Parser: "cfg"}},
		FriendlyConfig: []Setting{
			{ID: "sv_hostname", Label: "Server name", Help: "Public name in FiveM.", Kind: "text", File: "server.cfg", Key: "sv_hostname", Restart: true},
			{ID: "sv_maxclients", Label: "Player slots", Help: "Cannot exceed your Cfx.re license.", Kind: "number", File: "server.cfg", Key: "sv_maxclients", Default: "32", Restart: true},
		},
		Content: ContentSpec{Provider: "local", Kind: "resource", InstallDir: "resources", Dependencies: false},
	}
}

func gmodTemplate() Template {
	return Template{
		ID:             "ndl-gmod",
		Name:           "Garry's Mod",
		Game:           "gmod",
		Implementation: "srcds",
		Family:         "source",
		Summary:        "Garry's Mod dedicated server via SteamCMD. Workshop collections can be attached after install.",
		Capabilities: normalizeCapabilities([]string{
			CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules,
			CapSteamCMD, CapWorkshop, CapPlayers, CapQueries,
		}),
		Images:          map[string]string{"Debian": "steamcmd/steamcmd:debian"},
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./srcds_run -game garrysmod -console -port {{SERVER_PORT}} +map {{MAP}} +maxplayers {{MAX_PLAYERS}} +hostname \"{{SERVER_NAME}}\" +gamemode {{GAMEMODE}}",
		Stop:            "quit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true}},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   20480,
		DefaultCPUs:     2,
		Tags:            []string{"sandbox", "steam", "workshop"},
		Aliases:         []string{"gmod", "garrysmod", "garrys", "gm", "garry"},
		Hint:            "GSLT only to list publicly",
		Variables: []Variable{
			{Name: "Steam app ID", Env: "SRCDS_APPID", Description: "Garry's Mod dedicated server.", Default: "4020", Viewable: true, Editable: false, Required: true},
			{Name: "Server name", Env: "SERVER_NAME", Description: "Hostname in the server browser.", Default: "No-DAL GMod", Viewable: true, Editable: true, Required: true},
			{Name: "Map", Env: "MAP", Description: "Starting map.", Default: "gm_flatgrass", Viewable: true, Editable: true, Required: true},
			{Name: "Gamemode", Env: "GAMEMODE", Description: "sandbox, darkrp, terrortown, and others you install.", Default: "sandbox", Viewable: true, Editable: true, Required: true},
			{Name: "Max players", Env: "MAX_PLAYERS", Description: "Slot count.", Default: "16", Viewable: true, Editable: true, FieldType: "number"},
			{Name: "Game port", Env: "SERVER_PORT", Description: "UDP game port.", Default: "27015", Viewable: true, Editable: true, FieldType: "number"},
			{Name: "Workshop collection", Env: "WORKSHOP_COLLECTION", Description: "Optional Steam workshop collection ID.", Default: "", Viewable: true, Editable: true},
			{Name: "GSLT token", Env: "STEAM_TOKEN", Description: "Required to list a public Source server. Create one at steamcommunity.com/dev/managegameservers.", Default: "", Viewable: false, Editable: true, Secret: true, FieldType: "password"},
		},
		FriendlyConfig: []Setting{
			{ID: "hostname", Label: "Server name", Help: "Shown in the Garry's Mod browser.", Kind: "text", Env: "SERVER_NAME", Restart: true},
			{ID: "map", Label: "Start map", Help: "Map loaded at boot.", Kind: "text", Env: "MAP", Restart: true},
			{ID: "gamemode", Label: "Gamemode", Help: "Must match an installed gamemode folder.", Kind: "text", Env: "GAMEMODE", Restart: true},
			{ID: "slots", Label: "Player slots", Help: "How many clients can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
		},
		Content: ContentSpec{Provider: "workshop", Kind: "addon", InstallDir: "garrysmod/addons", GameID: "4000", Dependencies: false},
	}
}

func mindustryTemplate() Template {
	return Template{
		ID:             "ndl-mindustry",
		Name:           "Mindustry",
		Game:           "mindustry",
		Implementation: "vanilla",
		Family:         "mindustry",
		Summary:        "Mindustry dedicated server. Downloads the official server jar. No Steam account is required.",
		Capabilities: normalizeCapabilities([]string{
			CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules, CapPlayers, CapQueries, CapJava,
		}),
		Images:          map[string]string{"Java 17": "eclipse-temurin:17-jre"},
		DefaultImage:    "eclipse-temurin:17-jre",
		Startup:         "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar server-release.jar",
		Stop:            "exit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "mindustry",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 6567, Protocol: "tcp", Primary: true}, {Name: "game-udp", ContainerPort: 6567, Protocol: "udp"}},
		DefaultMemoryMB: 1024,
		DefaultDiskMB:   4096,
		DefaultCPUs:     1,
		Tags:            []string{"strategy", "sandbox"},
		Aliases:         []string{"mindustry", "mind", "mdty"},
		Variables: []Variable{
			{Name: "Release tag", Env: "MINDUSTRY_VERSION", Description: "GitHub release tag, or latest.", Default: "latest", Viewable: true, Editable: true},
			{Name: "Server memory (MiB)", Env: "SERVER_MEMORY", Description: "Java heap size.", Default: "768", Viewable: true, Editable: true, FieldType: "number"},
			{Name: "Server name", Env: "SERVER_NAME", Description: "Name shown to joining players.", Default: "No-DAL Mindustry", Viewable: true, Editable: true},
		},
		FriendlyConfig: []Setting{
			{ID: "name", Label: "Server name", Help: "Shown to players when they browse servers.", Kind: "text", Env: "SERVER_NAME", Restart: true},
		},
		Content: ContentSpec{Provider: "local", Kind: "map", InstallDir: "config/maps"},
	}
}

func templateByID(id string) (Template, bool) {
	for _, t := range BuiltinTemplates() {
		if t.ID == id {
			return cloneTemplate(t), true
		}
	}
	return Template{}, false
}
