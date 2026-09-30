package gameserver

// Modded server distributions layered on SteamCMD games (the server
// equivalent of Paper versus Vanilla): the Steam depot installs first, then
// the modding framework's release archive is extracted over it by the
// SteamCMD add-on download pass. Every repository, asset name, launch
// variable and path comes from the LinuxGSM module, Pelican egg or upstream
// documentation linked in SourceRef. Core Keeper and SCP: Secret
// Laboratory (vanilla) live here as well.

const (
	modPelicanSteam = "https://github.com/pelican-eggs/games-steamcmd/tree/main/"
	modLGSMModsList = "https://github.com/GameServerManagers/LinuxGSM/blob/master/lgsm/modules/mods_list.sh"

	// modRustArgs are the vanilla Rust launch arguments shared with
	// ndl-rust (builtins_steam.go rustTemplate).
	modRustArgs = "-batchmode +server.port {{SERVER_PORT}} +server.queryport {{QUERY_PORT}} +server.identity \"{{IDENTITY}}\" +server.seed {{SEED}} +server.worldsize {{WORLD_SIZE}} +server.maxplayers {{MAX_PLAYERS}} +server.hostname \"{{SERVER_NAME}}\" +server.description \"{{DESCRIPTION}}\" +rcon.port {{RCON_PORT}} +rcon.password \"$RCON_PASSWORD\" +rcon.web 1"

	// modValheimArgs mirror ndl-valheim. The BepInEx exports come from the
	// start_server_bepinex.sh shipped in the packs and LinuxGSM fix_vh.sh.
	modValheimDoorstop = "export DOORSTOP_ENABLED=1 DOORSTOP_TARGET_ASSEMBLY=./BepInEx/core/BepInEx.Preloader.dll LD_LIBRARY_PATH=./doorstop_libs:./linux64:$LD_LIBRARY_PATH LD_PRELOAD=libdoorstop_x64.so:$LD_PRELOAD SteamAppId=892970; "
	modValheimArgs     = "exec ./valheim_server.x86_64 -nographics -batchmode -name \"{{SERVER_NAME}}\" -port {{SERVER_PORT}} -world \"{{WORLD}}\" -password \"$SERVER_PASSWORD\" -public {{PUBLIC}} -savedir ./saves"

	// modCS2GameInfo adds the Metamod search path under Game_LowViolence in
	// game/csgo/gameinfo.gi, as the CounterStrikeSharp getting-started guide
	// documents. It is idempotent and runs at install and before each start
	// because CS2 updates restore the stock gameinfo.gi.
	modCS2GameInfo = "grep -q 'csgo/addons/metamod' game/csgo/gameinfo.gi || sed -i '/Game_LowViolence[[:space:]]*csgo_lv/a\\\t\t\tGame\tcsgo/addons/metamod' game/csgo/gameinfo.gi"
)

func moddedTemplates() []Template {
	return []Template{
		modRustOxide(),
		modRustCarbon(),
		modValheimBepInEx(),
		modValheimPlus(),
		modCS2CounterStrikeSharp(),
		mod7DTDOxide(),
		modCoreKeeper(),
		modSCPSL(),
		modSCPSLExiled(),
	}
}

// modRustBase carries the ndl-rust ports, variables and resources so the
// modded Rust templates behave exactly like the vanilla one.
func modRustBase(t Template, extra ...Variable) Template {
	t.GameTitle = "Rust"
	t.Category = "survival"
	t.Engine = "unity"
	t.Stop = "quit"
	t.Done = "Server startup complete"
	t.DefaultPorts = []Port{
		{Name: "game", ContainerPort: 28015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "query", ContainerPort: 28017, Protocol: "udp", Env: "QUERY_PORT"},
		{Name: "rcon", ContainerPort: 28016, Protocol: "tcp", Env: "RCON_PORT"},
	}
	t.DefaultMemoryMB, t.DefaultDiskMB, t.DefaultCPUs = 8192, 20480, 4
	t.Capabilities = append(t.Capabilities, CapWorlds, CapRCON, CapPlugins)
	vars := []Variable{
		envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Rust", true),
		envText("Identity", "IDENTITY", "Save folder name.", "server", true),
		envText("Description", "DESCRIPTION", "Short browser description.", "A No-DAL Rust server", false),
		envNumber("World size", "WORLD_SIZE", "Procedural map size. 1000 to 6000.", "3000", true),
		envNumber("Seed", "SEED", "Map seed.", "12345", true),
		envNumber("Max players", "MAX_PLAYERS", "Slot count.", "50", true),
		envNumber("Game port", "SERVER_PORT", "UDP game port.", "28015", true),
		envNumber("Query port", "QUERY_PORT", "Steam query port.", "28017", true),
		envNumber("RCON port", "RCON_PORT", "RCON port.", "28016", true),
		generatedSecret("RCON password", "RCON_PASSWORD", "Required by Rust RCON. Generated when left empty."),
	}
	t.Variables = append(vars, extra...)
	t.FriendlyConfig = []Setting{
		{ID: "name", Label: "Server name", Help: "Shown in the Rust browser.", Kind: "text", Env: "SERVER_NAME", Restart: true},
		{ID: "slots", Label: "Player slots", Help: "How many clients can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
	}
	return steamServer(t, "258550", "Rust dedicated server.")
}

func modRustOxide() Template {
	return modRustBase(Template{
		ID: "ndl-rust-oxide", Name: "Rust (Oxide / uMod)",
		Summary: "Rust dedicated server with the Oxide (uMod) plugin framework installed over the Steam files.",
		Startup: "export LD_LIBRARY_PATH=$HOME/RustDedicated_Data/Plugins/x86_64:$LD_LIBRARY_PATH; exec ./RustDedicated " + modRustArgs,
		Install: &InstallSpec{VersionEnv: "OXIDE_VERSION", Downloads: []Download{
			{Repo: "OxideMod/Oxide.Rust", Asset: `^Oxide\.Rust-linux\.zip$`},
		}},
		Content:   ContentSpec{Provider: "local", Kind: "plugin", InstallDir: "oxide/plugins"},
		Tags:      []string{"survival", "steam", "modded", "oxide", "umod", "plugins"},
		Aliases:   []string{"rust oxide", "oxide", "umod", "rust umod", "rust modded"},
		SourceRef: modLGSMModsList,
		DocsURL:   "https://umod.org/games/rust",
		Notes: []string{
			"Oxide replaces Rust managed assemblies, so it only works with the Rust build it was released for. After a Rust update, Reinstall once a matching Oxide release is out.",
			"Plugins go in oxide/plugins; configs and data are created under oxide/.",
		},
	}, envText("Oxide version", "OXIDE_VERSION", "Oxide.Rust release tag, or latest.", "latest", true))
}

func modRustCarbon() Template {
	return modRustBase(Template{
		ID: "ndl-rust-carbon", Name: "Rust (Carbon)",
		Summary: "Rust dedicated server with the Carbon modding framework, which runs Oxide plugins and Harmony mods.",
		// carbon.sh (shipped in the archive) sources carbon/tools/environment.sh,
		// which sets the Unity Doorstop preload, then starts RustDedicated.
		Startup: "exec bash ./carbon.sh " + modRustArgs,
		Install: &InstallSpec{VersionEnv: "CARBON_VERSION", Downloads: []Download{
			{Repo: "CarbonCommunity/Carbon", Asset: `^Carbon\.Linux\.Release\.tar\.gz$`, Executables: []string{"carbon.sh"}},
		}},
		Content:   ContentSpec{Provider: "local", Kind: "plugin", InstallDir: "carbon/plugins"},
		Tags:      []string{"survival", "steam", "modded", "carbon", "plugins"},
		Aliases:   []string{"rust carbon", "carbon", "rust modded"},
		SourceRef: "https://github.com/GameServerManagers/LinuxGSM/blob/master/lgsm/modules/fix_rust.sh",
		DocsURL:   "https://carbonmod.gg",
		Notes: []string{
			"Carbon is loaded by Unity Doorstop from carbon.sh and carbon/tools/environment.sh in the release archive.",
			"production_build is Carbon's stable channel. Other tags such as preview_build or edge_build track newer builds.",
			"Plugins go in carbon/plugins.",
		},
	}, envText("Carbon build", "CARBON_VERSION", "Carbon release tag: production_build (stable), preview_build, edge_build, or latest.", "production_build", true))
}

// modValheimBase mirrors ndl-valheim (ports, variables, resources).
func modValheimBase(t Template, extra ...Variable) Template {
	t.GameTitle = "Valheim"
	t.Category = "survival"
	t.Engine = "unity"
	t.Stop = "^C"
	t.Done = "Game server connected"
	t.Startup = modValheimDoorstop + modValheimArgs
	t.DefaultPorts = []Port{
		{Name: "game", ContainerPort: 2456, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "query", ContainerPort: 2457, Protocol: "udp"},
	}
	t.DefaultMemoryMB, t.DefaultDiskMB, t.DefaultCPUs = 4096, 12288, 2
	t.Capabilities = append(t.Capabilities, CapPlayers, CapWorlds, CapMods)
	t.Content = ContentSpec{Provider: "local", Kind: "mod", InstallDir: "BepInEx/plugins"}
	vars := []Variable{
		envText("Server name", "SERVER_NAME", "Name shown in the in-game browser.", "No-DAL Valheim", true),
		envText("World", "WORLD", "World save name.", "Dedicated", true),
		generatedSecret("Join password", "SERVER_PASSWORD", "Must be at least 5 characters. Valheim requires a password. Generated when left empty."),
		envNumber("Game port", "SERVER_PORT", "UDP game port. The query port is the next port.", "2456", true),
		envNumber("Listed publicly", "PUBLIC", "1 lists the server. 0 keeps it unlisted.", "1", true),
	}
	t.Variables = append(vars, extra...)
	t.FriendlyConfig = []Setting{
		{ID: "name", Label: "Server name", Help: "Shown to players in the join list.", Kind: "text", Env: "SERVER_NAME", Restart: true},
		{ID: "world", Label: "World name", Help: "Save folder for this world.", Kind: "text", Env: "WORLD", Restart: true},
	}
	return steamServer(t, "896660", "Valheim dedicated server.")
}

func modValheimBepInEx() Template {
	return modValheimBase(Template{
		ID: "ndl-valheim-bepinex", Name: "Valheim (BepInEx)",
		Summary: "Valheim dedicated server with BepInExPack_Valheim, the community BepInEx pack most Valheim mods require.",
		Install: &InstallSpec{VersionEnv: "BEPINEX_VERSION", Downloads: []Download{
			// Thunderstore package download; the zip holds BepInExPack_Valheim/
			// whose contents go in the server root (LinuxGSM modsubdirs).
			{URL: "https://thunderstore.io/package/download/denikson/BepInExPack_Valheim/{{BEPINEX_VERSION}}/", Archive: "zip", Strip: 1},
		}},
		Tags:      []string{"survival", "steam", "coop", "modded", "bepinex"},
		Aliases:   []string{"valheim bepinex", "bepinex", "valheim modded", "vh modded"},
		SourceRef: modPelicanSteam + "valheim/valheim_bepinex",
		DocsURL:   "https://thunderstore.io/c/valheim/p/denikson/BepInExPack_Valheim/",
		Notes: []string{
			"Drop mod DLLs into BepInEx/plugins. Most mods must also be installed on every client.",
			"The pack version is pinned in BEPINEX_VERSION (a Thunderstore version number such as 5.4.2351); change it and Reinstall to update.",
		},
	}, envText("BepInExPack version", "BEPINEX_VERSION", "Thunderstore version of denikson/BepInExPack_Valheim.", "5.4.2351", true))
}

func modValheimPlus() Template {
	return modValheimBase(Template{
		ID: "ndl-valheim-plus", Name: "Valheim Plus",
		Summary: "Valheim dedicated server with Valheim Plus, a large gameplay overhaul that ships its own BepInEx.",
		Install: &InstallSpec{VersionEnv: "VPLUS_VERSION", Downloads: []Download{
			{Repo: "Grantapher/ValheimPlus", Asset: `^UnixServer\.tar\.gz$`},
		}},
		ConfigFiles: []ConfigFile{{Path: "BepInEx/config/valheim_plus.cfg", Format: "ini", Restart: true}},
		Tags:        []string{"survival", "steam", "coop", "modded", "bepinex", "valheim-plus"},
		Aliases:     []string{"valheim plus", "valheim+", "v+", "vplus"},
		SourceRef:   modPelicanSteam + "valheim/valheim_plus",
		DocsURL:     "https://github.com/Grantapher/ValheimPlus",
		Notes: []string{
			"Players need the matching Valheim Plus client when the server enforces it (see BepInEx/config/valheim_plus.cfg).",
			"Valheim Plus is maintained in the Grantapher fork; each release targets a specific Valheim version.",
		},
	}, envText("Valheim Plus version", "VPLUS_VERSION", "Grantapher/ValheimPlus release tag, or latest.", "latest", true))
}

func modCS2CounterStrikeSharp() Template {
	t := Template{
		ID: "ndl-cs2-counterstrikesharp", Name: "Counter-Strike 2 (Metamod + CounterStrikeSharp)",
		GameTitle: "Counter-Strike 2", Category: "shooter", Engine: "source2",
		Implementation: "srcds", Family: "source",
		Summary:       "Counter-Strike 2 dedicated server with Metamod:Source 2 and the CounterStrikeSharp C# plugin framework.",
		Startup:       modCS2GameInfo + "; exec ./game/cs2.sh -dedicated -port {{SERVER_PORT}} +map {{MAP}} +game_type {{GAME_TYPE}} +game_mode {{GAME_MODE}} ${STEAM_TOKEN:++sv_setsteamaccount $STEAM_TOKEN}",
		Stop:          "quit",
		InstallScript: "cd /mnt/server\nif [ -f game/csgo/gameinfo.gi ]; then " + modCS2GameInfo + "; fi",
		Install: &InstallSpec{VersionEnv: "CSS_VERSION", Downloads: []Download{
			// Metamod:Source 2.0 dev builds (CounterStrikeSharp needs build
			// 1467 or newer) are GitHub pre-releases, so the build is pinned.
			{URL: "https://github.com/alliedmodders/metamod-source/releases/download/2.0.0.{{METAMOD_BUILD}}/mmsource-2.0.0-git{{METAMOD_BUILD}}-linux.tar.gz", Subdir: "game/csgo"},
			{Repo: "roflmuffin/CounterStrikeSharp", Asset: `^counterstrikesharp-with-runtime-(linux-[0-9.]+|build-[0-9]+-linux-[0-9a-f]+)\.zip$`, Subdir: "game/csgo"},
		}},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "rcon", ContainerPort: 27015, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "gotv", ContainerPort: 27020, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 40960, DefaultCPUs: 2,
		Capabilities: []string{CapPlugins},
		Dependencies: []string{"libicu76"},
		Content:      ContentSpec{Provider: "local", Kind: "plugin", InstallDir: "game/csgo/addons/counterstrikesharp/plugins"},
		Tags:         []string{"fps", "source", "steam", "modded", "metamod", "counterstrikesharp", "plugins"},
		Aliases:      []string{"cs2 modded", "counterstrikesharp", "cssharp", "css#", "metamod", "cs2 plugins"},
		Hint:         "GSLT only to list publicly",
		Requirements: []Requirement{gsltOptional("730")},
		Variables: []Variable{
			envText("Start map", "MAP", "Map loaded at boot.", "de_dust2", true),
			envNumber("Game type", "GAME_TYPE", "0 classic, 1 gun game, 3 custom.", "0", true),
			envNumber("Game mode", "GAME_MODE", "With classic type: 0 casual, 1 competitive.", "1", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27015", true),
			envSecret("GSLT token", "STEAM_TOKEN", "Required only to list a public server. Create one at steamcommunity.com/dev/managegameservers.", false),
			envText("CounterStrikeSharp version", "CSS_VERSION", "roflmuffin/CounterStrikeSharp release tag (for example v1.0.376), or latest.", "latest", true),
			envNumber("Metamod:Source build", "METAMOD_BUILD", "Metamod:Source 2.0 dev build number from github.com/alliedmodders/metamod-source releases (tag 2.0.0.<build>). CounterStrikeSharp needs 1467 or newer.", "1472", true),
		},
		FriendlyConfig: []Setting{
			{ID: "map", Label: "Start map", Help: "Map loaded at boot.", Kind: "text", Env: "MAP", Restart: true},
		},
		SourceRef: "https://github.com/roflmuffin/CounterStrikeSharp/blob/main/docfx/docs/guides/getting-started.md",
		DocsURL:   "https://docs.cssharp.dev/docs/guides/getting-started.html",
		Notes: []string{
			"Metamod is enabled by adding Game csgo/addons/metamod to game/csgo/gameinfo.gi; the server re-applies this before every start because CS2 updates restore the file.",
			"CS2 updates often break Metamod and CounterStrikeSharp until they publish matching builds. Update METAMOD_BUILD and Reinstall when that happens.",
			"The with-runtime package bundles .NET. Plugins go in game/csgo/addons/counterstrikesharp/plugins/<Name>/.",
		},
	}
	return steamServer(t, "730", "Counter-Strike 2. Dedicated files install from the game app.")
}

func mod7DTDOxide() Template {
	return steamServer(Template{
		ID: "ndl-7dtd-oxide", Name: "7 Days to Die (Oxide / uMod)",
		GameTitle: "7 Days to Die", Category: "survival", Engine: "unity",
		Summary: "7 Days to Die dedicated server with the Oxide (uMod) plugin framework installed over the Steam files.",
		Startup: "export LD_LIBRARY_PATH=$HOME/7DaysToDieServer_Data/Plugins/x86_64:$LD_LIBRARY_PATH; exec ./7DaysToDieServer.x86_64 -configfile=serverconfig.xml -quit -batchmode -nographics -dedicated",
		Stop:    "shutdown",
		Install: &InstallSpec{VersionEnv: "OXIDE_VERSION", Downloads: []Download{
			{Repo: "OxideMod/Oxide.SevenDaysToDie", Asset: `^Oxide\.SevenDaysToDie-linux\.zip$`},
		}},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 26900, Protocol: "tcp", Primary: true, Fixed: true},
			{Name: "game-udp", ContainerPort: 26900, Protocol: "udp", Fixed: true},
			{Name: "game-udp-1", ContainerPort: 26901, Protocol: "udp", Fixed: true},
			{Name: "game-udp-2", ContainerPort: 26902, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 6144, DefaultDiskMB: 20480, DefaultCPUs: 4,
		Capabilities: []string{CapWorlds, CapPlugins},
		Content:      ContentSpec{Provider: "local", Kind: "plugin", InstallDir: "oxide/plugins"},
		Variables: []Variable{
			envText("Oxide version", "OXIDE_VERSION", "Oxide.SevenDaysToDie release tag, or latest.", "latest", true),
		},
		ConfigFiles: []ConfigFile{{Path: "serverconfig.xml", Format: "xml", Restart: true}},
		Tags:        []string{"survival", "zombies", "steam", "modded", "oxide", "umod", "plugins"},
		Aliases:     []string{"7dtd oxide", "7 days to die oxide", "sdtd oxide", "7dtd umod"},
		SourceRef:   modLGSMModsList,
		DocsURL:     "https://umod.org/games/7-days-to-die",
		Notes: []string{
			"Oxide replaces the game's Assembly-CSharp.dll, so it only works with the 7 Days to Die build it was released for. After a game update, Reinstall once a matching Oxide release is out.",
			"Plugins go in oxide/plugins.",
		},
	}, "294420", "7 Days to Die dedicated server.")
}

func modCoreKeeper() Template {
	return steamServer(Template{
		ID: "ndl-core-keeper", Name: "Core Keeper", GameTitle: "Core Keeper",
		Category: "sandbox", Engine: "unity",
		Summary: "Core Keeper dedicated server, a co-op mining and crafting sandbox. Runs headless under a virtual X display.",
		// LinuxGSM runs CoreKeeperServer under Xvfb (preexecutable xvfb-run);
		// the server logs to a file, which is followed on the console.
		Startup: "export DISPLAY=:99; Xvfb :99 -screen 0 1x1x24 -ac -nolisten tcp -nolisten unix >/dev/null 2>&1 & XV=$!; touch CoreKeeperServerLog.txt; tail -n 0 -F CoreKeeperServerLog.txt & TL=$!; ./CoreKeeperServer -batchmode -logfile CoreKeeperServerLog.txt -ip 0.0.0.0 -port {{SERVER_PORT}} -world {{WORLD_INDEX}} -worldname \"{{WORLD_NAME}}\" -worldseed {{WORLD_SEED}} -worldmode {{WORLD_MODE}} -maxplayers {{MAX_PLAYERS}} ${GAME_ID:+-gameid $GAME_ID} & CK=$!; trap 'kill -INT $CK' INT TERM; wait $CK; wait $CK; kill $TL $XV",
		Stop:    "^C", Done: "Started session", StopTimeout: 60,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27016, Protocol: "udp"},
		},
		DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 2,
		Capabilities: []string{CapWorlds},
		Dependencies: []string{"xvfb", "libxi6"},
		Variables: []Variable{
			envText("World name", "WORLD_NAME", "Name of the world and server.", "No-DAL Core Keeper", true),
			envNumber("World index", "WORLD_INDEX", "Which world slot to load.", "0", true),
			envNumber("World seed", "WORLD_SEED", "Seed for a new world. 0 picks a random seed.", "0", true),
			{Name: "World mode", Env: "WORLD_MODE", Description: "0 normal, 1 hard. Used when the world is created.", Default: "0", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"0", "1"}},
			envNumber("Max players", "MAX_PLAYERS", "Slot count, up to 100.", "8", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port for direct connections. The query port is the next port.", "27015", true),
			envText("Game ID", "GAME_ID", "Optional fixed Game ID (at least 28 alphanumeric characters, excluding Y, y, x, 0 and O). Empty keeps the ID the server generates in GameID.txt.", "", false),
		},
		FriendlyConfig: []Setting{
			{ID: "worldname", Label: "World name", Help: "Name of the world and server.", Kind: "text", Env: "WORLD_NAME", Restart: true},
			{ID: "slots", Label: "Player slots", Help: "How many players can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
		},
		Tags:      []string{"sandbox", "coop", "crafting", "mining"},
		Aliases:   []string{"core keeper", "corekeeper", "ck"},
		SourceRef: lgsmRef("ck"),
		Notes: []string{
			"Players can join by the Game ID written to GameID.txt on first start (Steam relay) or directly by IP and game port.",
			"Worlds and ServerConfig.json are stored under .config/unity3d/Pugstorm/Core Keeper/DedicatedServer.",
		},
	}, "1963720", "Core Keeper dedicated server.")
}

// modSCPSLBase is the SCP: Secret Laboratory dedicated server started through
// LocalAdmin, with the EULA and first-run config wizard answered by the
// documented ACCEPT_SCPSL_EULA variable and --useDefault flag.
func modSCPSLBase(t Template, extra ...Variable) Template {
	t.GameTitle = "SCP: Secret Laboratory"
	t.Category = "shooter"
	t.Engine = "unity"
	t.Startup = "exec ./LocalAdmin {{SERVER_PORT}} --useDefault --noSetCursor --noTerminalTitle"
	t.Stop = "exit"
	t.Done = "Waiting for players"
	t.StopTimeout = 60
	t.DefaultPorts = []Port{
		{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "game-tcp", ContainerPort: 7777, Protocol: "tcp", Env: "SERVER_PORT"},
	}
	t.DefaultMemoryMB, t.MinMemoryMB, t.DefaultDiskMB, t.DefaultCPUs = 4096, 3072, 8192, 2
	t.Capabilities = append(t.Capabilities, CapPlayers)
	t.Dependencies = []string{"lib32gcc-s1", "libicu76"}
	vars := []Variable{
		envNumber("Game port", "SERVER_PORT", "Server port. LocalAdmin keeps configs per port under .config/SCP Secret Laboratory/config/<port>.", "7777", true),
		{Name: "Accept SCP:SL EULA", Env: "ACCEPT_SCPSL_EULA", Description: "Must be true to start. Read the EULA at https://link.scpslgame.com/eula first; LocalAdmin reads this variable.", Default: "false", Viewable: true, Editable: true, Required: true, FieldType: "toggle"},
	}
	t.Variables = append(vars, extra...)
	t.Requirements = append(t.Requirements, Requirement{Kind: ReqEULA, Stage: StageStart, Env: "ACCEPT_SCPSL_EULA", Label: "Accept the SCP: Secret Laboratory EULA", URL: "https://link.scpslgame.com/eula"})
	t.Hint = "Accept the SCP:SL EULA before starting"
	return steamServer(t, "996560", "SCP: Secret Laboratory dedicated server.")
}

func modSCPSL() Template {
	return modSCPSLBase(Template{
		ID: "ndl-scpsl", Name: "SCP: Secret Laboratory",
		Summary:     "SCP: Secret Laboratory dedicated server run through Northwood's LocalAdmin.",
		ConfigFiles: []ConfigFile{{Path: ".config/SCP Secret Laboratory/config/7777/config_gameplay.txt", Format: "yaml", Restart: true}},
		Tags:        []string{"horror", "multiplayer", "pvp", "asymmetric"},
		Aliases:     []string{"scpsl", "scp sl", "scp", "secret laboratory", "scp secret laboratory"},
		SourceRef:   lgsmRef("scpsl"),
		DocsURL:     "https://techwiki.scpslgame.com/books/server-guides/page/1-how-to-create-a-dedicated-server",
		Notes: []string{
			"Configs live under .config/SCP Secret Laboratory/config/<port>/ and are created with defaults on first start.",
			"To appear in the public server list the server must be verified by Northwood (see the official server guide).",
		},
	})
}

func modSCPSLExiled() Template {
	return modSCPSLBase(Template{
		ID: "ndl-scpsl-exiled", Name: "SCP: Secret Laboratory (EXILED)",
		Summary: "SCP: Secret Laboratory dedicated server with the EXILED plugin framework loaded through LabAPI.",
		Install: &InstallSpec{VersionEnv: "EXILED_VERSION", Downloads: []Download{
			// Exiled.tar.gz holds EXILED/ and SCP Secret Laboratory/LabAPI/,
			// which the Pelican egg copies into ~/.config.
			{Repo: "ExMod-Team/EXILED", Asset: `^Exiled\.tar\.gz$`, Subdir: ".config"},
		}},
		Capabilities: []string{CapPlugins},
		Content:      ContentSpec{Provider: "local", Kind: "plugin", InstallDir: ".config/EXILED/Plugins"},
		Tags:         []string{"horror", "multiplayer", "pvp", "asymmetric", "modded", "exiled", "plugins"},
		Aliases:      []string{"scpsl exiled", "exiled", "scp exiled", "scpsl modded"},
		SourceRef:    modPelicanSteam + "scpsl/exiled",
		DocsURL:      "https://github.com/ExMod-Team/EXILED",
		Notes: []string{
			"EXILED targets specific SCP:SL versions. After a game update, Reinstall once a matching EXILED release is out.",
			"Plugins go in .config/EXILED/Plugins; EXILED configs are created under .config/EXILED/Configs on first start.",
		},
	}, envText("EXILED version", "EXILED_VERSION", "ExMod-Team/EXILED release tag, or latest.", "latest", true))
}
