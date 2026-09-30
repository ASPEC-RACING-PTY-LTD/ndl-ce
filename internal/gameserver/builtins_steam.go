package gameserver

func rustTemplate() Template {
	return Template{
		ID:             "ndl-rust",
		SourceRef:      lgsmRef("rust"),
		GameTitle:      "Rust",
		Category:       "survival",
		Engine:         "unity",
		Name:           "Rust",
		Game:           "rust",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Rust dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds, CapRCON),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "export LD_LIBRARY_PATH=$HOME/RustDedicated_Data/Plugins/x86_64:$LD_LIBRARY_PATH; exec ./RustDedicated -batchmode +server.port {{SERVER_PORT}} +server.queryport {{QUERY_PORT}} +server.identity \"{{IDENTITY}}\" +server.seed {{SEED}} +server.worldsize {{WORLD_SIZE}} +server.maxplayers {{MAX_PLAYERS}} +server.hostname \"{{SERVER_NAME}}\" +server.description \"{{DESCRIPTION}}\" +rcon.port {{RCON_PORT}} +rcon.password \"$RCON_PASSWORD\" +rcon.web 1",
		Stop:           "quit",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 28015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 28017, Protocol: "udp", Env: "QUERY_PORT"},
			{Name: "rcon", ContainerPort: 28016, Protocol: "tcp", Env: "RCON_PORT"},
		},
		DefaultMemoryMB: 8192,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "steam"},
		Aliases:         []string{"rust"},
		Variables: append([]Variable{
			steamAppVar("258550", "Rust dedicated server."),
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
		}, steamLoginVars()...),
		FriendlyConfig: []Setting{
			{ID: "name", Label: "Server name", Help: "Shown in the Rust browser.", Kind: "text", Env: "SERVER_NAME", Restart: true},
			{ID: "slots", Label: "Player slots", Help: "How many clients can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
		},
	}
}

func palworldTemplate() Template {
	return Template{
		ID:              "ndl-palworld",
		SourceRef:       lgsmRef("pw"),
		GameTitle:       "Palworld",
		Category:        "survival",
		Engine:          "unreal5",
		Name:            "Palworld",
		Game:            "palworld",
		Implementation:  "steamcmd",
		Family:          "steam",
		Summary:         "Palworld dedicated server via SteamCMD.",
		Capabilities:    baseCaps(CapSteamCMD, CapWorlds),
		Images:          steamImage(),
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./PalServer.sh -port={{SERVER_PORT}} -players={{MAX_PLAYERS}} -useperfthreads -NoAsyncLoadingThread -UseMultithreadForDS",
		Stop:            "^C",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 8211, Protocol: "udp", Primary: true, Env: "SERVER_PORT"}},
		DefaultMemoryMB: 8192,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "steam", "coop"},
		Aliases:         []string{"palworld", "pal", "pals"},
		Variables: append([]Variable{
			steamAppVar("2394010", "Palworld dedicated server."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "8211", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count. Palworld also has its own config cap.", "16", true),
		}, steamLoginVars()...),
		ConfigFiles: []ConfigFile{{Path: "Pal/Saved/Config/LinuxServer/PalWorldSettings.ini", Format: "ini", Restart: true}},
	}
}

func zomboidTemplate() Template {
	return Template{
		ID:             "ndl-zomboid",
		SourceRef:      lgsmRef("pz"),
		GameTitle:      "Project Zomboid",
		Category:       "survival",
		Engine:         "custom",
		Name:           "Project Zomboid",
		Game:           "zomboid",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Project Zomboid dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./start-server.sh -servername \"{{SERVER_NAME}}\" -adminpassword \"$ADMIN_PASSWORD\" -port {{SERVER_PORT}}",
		Stop:           "quit",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 16261, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "direct", ContainerPort: 16262, Protocol: "udp"},
		},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   16384,
		DefaultCPUs:     2,
		Tags:            []string{"survival", "zombies", "steam"},
		Aliases:         []string{"zomboid", "pz", "projectzomboid", "project zomboid"},
		Variables: append([]Variable{
			steamAppVar("380870", "Project Zomboid dedicated server."),
			envText("Server name", "SERVER_NAME", "Save and browser name.", "servertest", true),
			generatedSecret("Admin password", "ADMIN_PASSWORD", "In-game admin password. Generated when left empty."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "16261", true),
		}, steamLoginVars()...),
	}
}

func sevenDaysTemplate() Template {
	return Template{
		ID:             "ndl-7dtd",
		SourceRef:      lgsmRef("sdtd"),
		GameTitle:      "7 Days to Die",
		Category:       "survival",
		Engine:         "unity",
		Name:           "7 Days to Die",
		Game:           "7dtd",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "7 Days to Die dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./7DaysToDieServer.x86_64 -configfile=serverconfig.xml -quit -batchmode -nographics -dedicated",
		Stop:           "shutdown",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 26900, Protocol: "tcp", Primary: true, Fixed: true},
			{Name: "game-udp", ContainerPort: 26900, Protocol: "udp", Fixed: true},
			{Name: "game-udp-1", ContainerPort: 26901, Protocol: "udp", Fixed: true},
			{Name: "game-udp-2", ContainerPort: 26902, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 6144,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "zombies", "steam"},
		Aliases:         []string{"7dtd", "7days", "seven days", "7 days to die", "sdtd"},
		Variables: append([]Variable{
			steamAppVar("294420", "7 Days to Die dedicated server."),
		}, steamLoginVars()...),
		ConfigFiles: []ConfigFile{{Path: "serverconfig.xml", Format: "xml", Restart: true}},
	}
}

func cs2Template() Template {
	return Template{
		ID:             "ndl-cs2",
		SourceRef:      lgsmRef("cs2"),
		GameTitle:      "Counter-Strike 2",
		Category:       "shooter",
		Engine:         "source2",
		Name:           "Counter-Strike 2",
		Game:           "cs2",
		Implementation: "srcds",
		Family:         "source",
		Summary:        "Counter-Strike 2 dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./game/cs2.sh -dedicated -port {{SERVER_PORT}} +map {{MAP}} +game_type {{GAME_TYPE}} +game_mode {{GAME_MODE}} ${STEAM_TOKEN:++sv_setsteamaccount $STEAM_TOKEN}",
		Stop:           "quit",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "rcon", ContainerPort: 27015, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "gotv", ContainerPort: 27020, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   40960,
		DefaultCPUs:     2,
		Tags:            []string{"fps", "source", "steam"},
		Aliases:         []string{"cs2", "counter-strike", "counterstrike", "csgo", "cs"},
		Hint:            "GSLT only to list publicly",
		Variables: append([]Variable{
			steamAppVar("730", "Counter-Strike 2. Dedicated files install from the game app."),
			envText("Start map", "MAP", "Map loaded at boot.", "de_dust2", true),
			envNumber("Game type", "GAME_TYPE", "0 classic, 1 gun game, 3 custom.", "0", true),
			envNumber("Game mode", "GAME_MODE", "With classic type: 0 casual, 1 competitive.", "1", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27015", true),
			envSecret("GSLT token", "STEAM_TOKEN", "Required only to list a public server. Create one at steamcommunity.com/dev/managegameservers.", false),
		}, steamLoginVars()...),
		FriendlyConfig: []Setting{
			{ID: "map", Label: "Start map", Help: "Map loaded at boot.", Kind: "text", Env: "MAP", Restart: true},
		},
	}
}

func satisfactoryTemplate() Template {
	return Template{
		ID:             "ndl-satisfactory",
		SourceRef:      lgsmRef("sf"),
		GameTitle:      "Satisfactory",
		Category:       "simulation",
		Engine:         "unreal5",
		Name:           "Satisfactory",
		Game:           "satisfactory",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Satisfactory dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./FactoryServer.sh -Port={{SERVER_PORT}}",
		Stop:           "^C",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "game-tcp", ContainerPort: 7777, Protocol: "tcp", Env: "SERVER_PORT"},
		},
		DefaultMemoryMB: 8192,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"factory", "coop", "steam"},
		Aliases:         []string{"satisfactory", "satis", "sf"},
		Variables: append([]Variable{
			steamAppVar("1690800", "Satisfactory dedicated server."),
			envNumber("Game port", "SERVER_PORT", "Game port.", "7777", true),
		}, steamLoginVars()...),
	}
}

func dstTemplate() Template {
	return Template{
		ID:             "ndl-dst",
		SourceRef:      lgsmRef("dst"),
		GameTitle:      "Don't Starve Together",
		Category:       "survival",
		Engine:         "custom",
		Name:           "Don't Starve Together",
		Game:           "dst",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Don't Starve Together dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "sh -c 'CLUSTER=\"{{CLUSTER_NAME}}\"; BASE=\"$HOME/.klei/DoNotStarveTogether/$CLUSTER\"; mkdir -p \"$BASE/Master\"; printf \"%s\\n\" \"$CLUSTER_TOKEN\" > \"$BASE/cluster_token.txt\"; cd bin64 && exec ./dontstarve_dedicated_server_nullrenderer_x64 -console -cluster \"$CLUSTER\" -shard Master'",
		Stop:           "c_shutdown()",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		InstallScript:  dstBootstrapScript,
		StartRequires:  []string{"CLUSTER_TOKEN"},
		Hint:           "Klei cluster token required to start",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 10999, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "steam", ContainerPort: 27016, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   12288,
		DefaultCPUs:     2,
		Tags:            []string{"survival", "coop", "steam"},
		Aliases:         []string{"dst", "dontstarve", "don't starve", "klei"},
		Variables: append([]Variable{
			steamAppVar("343050", "Don't Starve Together dedicated server."),
			envSecret("Cluster token", "CLUSTER_TOKEN", "Klei server token from accounts.klei.com. You can install without it. Start requires a real token.", false),
			envText("Cluster name", "CLUSTER_NAME", "Cluster folder name.", "Cluster_1", true),
			envText("Server name", "SERVER_NAME", "Name shown to joining players.", "No-DAL DST", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count written into cluster.ini on install.", "6", true),
		}, steamLoginVars()...),
	}
}

func unturnedTemplate() Template {
	return Template{
		ID:              "ndl-unturned",
		SourceRef:       lgsmRef("unt"),
		GameTitle:       "Unturned",
		Category:        "survival",
		Engine:          "unity",
		Name:            "Unturned",
		Game:            "unturned",
		Implementation:  "steamcmd",
		Family:          "steam",
		Summary:         "Unturned dedicated server via SteamCMD.",
		Capabilities:    baseCaps(CapSteamCMD, CapWorlds),
		Images:          steamImage(),
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./Unturned_Headless.x86_64 -nographics -batchmode -bind 0.0.0.0 -port {{SERVER_PORT}} +InternetServer/ndl",
		Stop:            "shutdown",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   12288,
		DefaultCPUs:     2,
		Tags:            []string{"survival", "steam"},
		Aliases:         []string{"unturned", "unt"},
		Variables: append([]Variable{
			steamAppVar("1110390", "Unturned dedicated server."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27015", true),
		}, steamLoginVars()...),
	}
}

func l4d2Template() Template {
	return Template{
		ID:              "ndl-l4d2",
		SourceRef:       lgsmRef("l4d2"),
		GameTitle:       "Left 4 Dead 2",
		Category:        "shooter",
		Engine:          "source",
		Name:            "Left 4 Dead 2",
		Game:            "l4d2",
		Implementation:  "srcds",
		Family:          "source",
		Summary:         "Left 4 Dead 2 dedicated server via SteamCMD.",
		Capabilities:    baseCaps(CapSteamCMD),
		Images:          steamImage(),
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./srcds_run -game left4dead2 -console -port {{SERVER_PORT}} +map {{MAP}} +maxplayers {{MAX_PLAYERS}} +hostname \"{{SERVER_NAME}}\" ${STEAM_TOKEN:++sv_setsteamaccount $STEAM_TOKEN}",
		Stop:            "quit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   20480,
		DefaultCPUs:     2,
		Tags:            []string{"coop", "source", "steam"},
		Aliases:         []string{"l4d2", "l4d", "left4dead", "left 4 dead"},
		Hint:            "GSLT only to list publicly",
		Variables: append([]Variable{
			steamAppVar("222860", "Left 4 Dead 2 dedicated server."),
			envText("Server name", "SERVER_NAME", "Hostname in the server browser.", "No-DAL L4D2", true),
			envText("Start map", "MAP", "Map loaded at boot.", "c1m1_hotel", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "8", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27015", true),
			envSecret("GSLT token", "STEAM_TOKEN", "Required only to list a public server.", false),
		}, steamLoginVars()...),
	}
}

func arkTemplate() Template {
	return Template{
		ID:             "ndl-ark",
		SourceRef:      lgsmRef("ark"),
		GameTitle:      "ARK: Survival Evolved",
		Category:       "survival",
		Engine:         "unreal4",
		Name:           "ARK: Survival Evolved",
		Game:           "ark",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "ARK: Survival Evolved dedicated server via SteamCMD. Survival Ascended is not Linux-native.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./ShooterGame/Binaries/Linux/ShooterGameServer {{MAP}}?listen?SessionName={{SERVER_NAME}}?ServerPassword=$SERVER_PASSWORD?ServerAdminPassword=$ADMIN_PASSWORD?Port={{SERVER_PORT}}?QueryPort={{QUERY_PORT}}?MaxPlayers={{MAX_PLAYERS}} -server -log",
		Stop:           "^C",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "peer", ContainerPort: 7778, Protocol: "udp"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 8192,
		DefaultDiskMB:   40960,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "steam"},
		Aliases:         []string{"ark", "ase", "evolved"},
		Variables: append([]Variable{
			steamAppVar("376030", "ARK: Survival Evolved dedicated server."),
			envText("Map", "MAP", "Map loaded at boot, for example TheIsland.", "TheIsland", true),
			envText("Session name", "SERVER_NAME", "Name shown in the ARK browser.", "No-DAL ARK", true),
			envSecret("Join password", "SERVER_PASSWORD", "Optional join password.", false),
			generatedSecret("Admin password", "ADMIN_PASSWORD", "In-game admin password. Generated when left empty."),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "20", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		}, steamLoginVars()...),
	}
}

func conanTemplate() Template {
	return Template{
		ID:             "ndl-conan",
		GameTitle:      "Conan Exiles",
		Category:       "survival",
		Engine:         "unreal4",
		Name:           "Conan Exiles",
		Game:           "conan",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Conan Exiles dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./ConanSandboxServer.sh -log -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}}",
		Stop:           "^C",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		Hidden:          true,
		Notes:           []string{"Hidden from the catalogue: the Conan Exiles dedicated server (app 443030) ships only a Windows build and needs Wine or Proton, which No-DAL does not provide. Kept so servers created from it still resolve."},
		DefaultMemoryMB: 6144,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "steam"},
		Aliases:         []string{"conan", "exiles"},
		Variables: append([]Variable{
			steamAppVar("443030", "Conan Exiles dedicated server."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		}, steamLoginVars()...),
	}
}

func dayzTemplate() Template {
	return requireSteamOwner(Template{
		ID:             "ndl-dayz",
		SourceRef:      lgsmRef("dayz"),
		GameTitle:      "DayZ",
		Category:       "survival",
		Engine:         "enfusion",
		Name:           "DayZ",
		Game:           "dayz",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "DayZ dedicated server via SteamCMD.",
		Capabilities:   baseCaps(CapSteamCMD, CapWorlds),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./DayZServer -config=serverDZ.cfg -port={{SERVER_PORT}} -dologs -adminlog -netlog -freezecheck",
		Stop:           "^C",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		InstallScript:  dayzBootstrapScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 2302, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "reserved", ContainerPort: 2303, Protocol: "udp"},
			{Name: "battleye", ContainerPort: 2304, Protocol: "udp"},
			{Name: "rcon", ContainerPort: 2305, Protocol: "udp"},
		},
		DefaultMemoryMB: 6144,
		DefaultDiskMB:   20480,
		DefaultCPUs:     4,
		Tags:            []string{"survival", "steam"},
		Aliases:         []string{"dayz"},
		Variables: append([]Variable{
			steamAppVar("223350", "DayZ dedicated server."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "2302", true),
		}, steamLoginVars()...),
		ConfigFiles: []ConfigFile{{Path: "serverDZ.cfg", Format: "cfg", Restart: true, Parser: "cfg"}},
	}, "Steam account that owns DayZ")
}

func arma3Template() Template {
	return requireSteamOwner(Template{
		ID:             "ndl-arma3",
		SourceRef:      lgsmRef("arma3"),
		GameTitle:      "Arma 3",
		Category:       "tactical",
		Engine:         "real-virtuality",
		Name:           "Arma 3",
		Game:           "arma3",
		Implementation: "steamcmd",
		Family:         "steam",
		Summary:        "Arma 3 dedicated server via SteamCMD. Anonymous install often fails; a Steam account that owns Arma 3 is usually required.",
		Capabilities:   baseCaps(CapSteamCMD),
		Images:         steamImage(),
		DefaultImage:   "steamcmd/steamcmd:debian",
		Startup:        "./arma3server_x64 -name=server -config=server.cfg -port={{SERVER_PORT}}",
		Stop:           "^C",
		WorkingDir:     "/home/container",
		InstallBuiltin: "steamcmd",
		InstallScript:  arma3BootstrapScript,
		Hint:           "Steam account often required to install",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 2302, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "reserved", ContainerPort: 2303, Protocol: "udp"},
			{Name: "steam", ContainerPort: 2304, Protocol: "udp"},
		},
		DefaultMemoryMB: 4096,
		DefaultDiskMB:   40960,
		DefaultCPUs:     4,
		Tags:            []string{"milsim", "steam"},
		Aliases:         []string{"arma3", "arma", "a3"},
		Variables: append([]Variable{
			steamAppVar("233780", "Arma 3 dedicated server."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "2302", true),
		}, steamLoginVars()...),
		ConfigFiles: []ConfigFile{{Path: "server.cfg", Format: "cfg", Restart: true, Parser: "cfg"}},
	}, "Steam account that owns Arma 3")
}

func tf2Template() Template {
	return Template{
		ID:              "ndl-tf2",
		SourceRef:       lgsmRef("tf2"),
		GameTitle:       "Team Fortress 2",
		Category:        "shooter",
		Engine:          "source",
		Name:            "Team Fortress 2",
		Game:            "tf2",
		Implementation:  "srcds",
		Family:          "source",
		Summary:         "Team Fortress 2 dedicated server via SteamCMD.",
		Capabilities:    baseCaps(CapSteamCMD),
		Images:          steamImage(),
		DefaultImage:    "steamcmd/steamcmd:debian",
		Startup:         "./srcds_run -game tf -console -port {{SERVER_PORT}} +map {{MAP}} +maxplayers {{MAX_PLAYERS}} +hostname \"{{SERVER_NAME}}\" ${STEAM_TOKEN:++sv_setsteamaccount $STEAM_TOKEN}",
		Stop:            "quit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "steamcmd",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   20480,
		DefaultCPUs:     2,
		Tags:            []string{"fps", "source", "steam"},
		Aliases:         []string{"tf2", "team fortress", "teamfortress"},
		Hint:            "GSLT only to list publicly",
		Variables: append([]Variable{
			steamAppVar("232250", "Team Fortress 2 dedicated server."),
			envText("Server name", "SERVER_NAME", "Hostname in the server browser.", "No-DAL TF2", true),
			envText("Start map", "MAP", "Map loaded at boot.", "ctf_2fort", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "24", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27015", true),
			envSecret("GSLT token", "STEAM_TOKEN", "Required only to list a public server.", false),
		}, steamLoginVars()...),
	}
}

const dstBootstrapScript = `
CLUSTER="${CLUSTER_NAME:-Cluster_1}"
BASE="/mnt/server/.klei/DoNotStarveTogether/${CLUSTER}"
mkdir -p "$BASE/Master"
if [ ! -f "$BASE/cluster.ini" ]; then
  printf '[GAMEPLAY]\ngame_mode = survival\nmax_players = %s\npvp = false\n\n[NETWORK]\ncluster_name = %s\ncluster_description = A No-DAL DST server\ncluster_password = \ncluster_intention = cooperative\n\n[MISC]\nconsole_enabled = true\n\n[SHARD]\nshard_enabled = false\n' "${MAX_PLAYERS:-6}" "${SERVER_NAME:-No-DAL DST}" > "$BASE/cluster.ini"
fi
if [ ! -f "$BASE/Master/server.ini" ]; then
  printf '[NETWORK]\nserver_port = 10999\n\n[SHARD]\nis_master = true\n\n[STEAM]\nmaster_server_port = 27016\nauthentication_port = 8766\n' > "$BASE/Master/server.ini"
fi
`

const dayzBootstrapScript = `
if [ ! -f /mnt/server/serverDZ.cfg ]; then
  printf 'hostname = "No-DAL DayZ";\npassword = "";\npasswordAdmin = "";\nmaxPlayers = 60;\nverifySignatures = 2;\nforceSameBuild = 1;\ndisableVoN = 0;\nvonCodecQuality = 20;\nserverTime = "SystemTime";\ninstanceId = 1;\nclass Missions\n{\n\tclass DayZ\n\t{\n\t\ttemplate="dayzOffline.chernarusplus";\n\t};\n};\n' > /mnt/server/serverDZ.cfg
fi
`

const arma3BootstrapScript = `
if [ ! -f /mnt/server/server.cfg ]; then
  printf 'hostname = "No-DAL Arma 3";\nmaxPlayers = 32;\npassword = "";\npasswordAdmin = "";\n' > /mnt/server/server.cfg
fi
`
