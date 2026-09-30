package gameserver

import "strconv"

// SteamCMD-installed, Linux-native dedicated servers for survival, sandbox,
// crafting, colony and space games. Every app ID, executable, argument and
// port below comes from the LinuxGSM default config or Pelican egg linked in
// SourceRef (or the official docs in DocsURL). Games whose dedicated server
// ships only a Windows build (Enshrouded, V Rising, Icarus, Abiotic Factor,
// Sons Of The Forest and others) are deliberately absent.

const survPelicanBase = "https://github.com/pelican-eggs/games-steamcmd/tree/main/"

func survPelican(dir string) string { return survPelicanBase + dir }

// survSteam fills the game key and the world capability every template in
// this file shares, then hands off to steamServer.
func survSteam(t Template, appID, help string) Template {
	if t.Game == "" {
		t.Game = gameKey(t.ID)
	}
	if t.DefaultCPUs == 0 {
		t.DefaultCPUs = 2
	}
	t.Capabilities = append([]string{CapWorlds}, t.Capabilities...)
	return steamServer(t, appID, help)
}

func steamSurvivalTemplates() []Template {
	return []Template{
		survAvorion(),
		survBarotrauma(),
		survColonySurvival(),
		survCraftopia(),
		survEco(),
		survFrozenFlame(),
		survHumanitZ(),
		survHurtworld(),
		survNecesse(),
		survNightingale(),
		survRisingWorld(),
		survRisingWorldLegacy(),
		survSmalland(),
		survSolaceCrafting(),
		survSoulmask(),
		survStarbound(),
		survStationeers(),
		survTheFront(),
		survTheIsle(),
		survVein(),
		survWurm(),
		survModiverse(),
		survAstroColony(),
		survBrickadia(),
		survCryoFall(),
		survDayOfDragons(),
		survSurviveTheNights(),
		survDragonwilds(),
	}
}

func survAvorion() Template {
	return survSteam(Template{
		ID: "ndl-avorion", Name: "Avorion", GameTitle: "Avorion",
		Category: "sandbox", Engine: "custom",
		Summary: "Avorion space sandbox dedicated server. Build ships, mine and fight across a procedurally generated galaxy.",
		Startup: "export LD_LIBRARY_PATH=./linux64:$LD_LIBRARY_PATH; exec ./bin/AvorionServer --galaxy-name \"{{GALAXY_NAME}}\" --datapath galaxy --port {{SERVER_PORT}} --query-port {{QUERY_PORT}} --steam-query-port {{STEAM_QUERY_PORT}} --steam-master-port {{STEAM_MASTER_PORT}} --max-players {{MAX_PLAYERS}} --difficulty {{DIFFICULTY}} --server-name \"{{SERVER_NAME}}\" --listed {{SERVER_LISTED}}",
		Stop:    "/stop", Done: "Server startup complete", StopTimeout: 60,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27000, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "game-tcp", ContainerPort: 27000, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27003, Protocol: "udp", Env: "QUERY_PORT"},
			{Name: "steam-query", ContainerPort: 27020, Protocol: "udp", Env: "STEAM_QUERY_PORT"},
			{Name: "steam-master", ContainerPort: 27021, Protocol: "udp", Env: "STEAM_MASTER_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:    []string{"space", "sandbox", "building", "coop", "pvp"},
		Aliases: []string{"avorion", "av"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Avorion", true),
			envText("Galaxy name", "GALAXY_NAME", "Galaxy save folder under galaxy/.", "avorion_galaxy", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "10", true),
			{Name: "Difficulty", Env: "DIFFICULTY", Description: "Galaxy difficulty from -3 (easiest) to 3 (hardest).", Default: "0", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"-3", "-2", "-1", "0", "1", "2", "3"}},
			{Name: "Public listing", Env: "SERVER_LISTED", Description: "Show the server in the public server list.", Default: "true", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"true", "false"}},
			envNumber("Game port", "SERVER_PORT", "Game port (UDP and TCP).", "27000", true),
			envNumber("Query port", "QUERY_PORT", "Avorion query port.", "27003", true),
			envNumber("Steam query port", "STEAM_QUERY_PORT", "Steam query port.", "27020", true),
			envNumber("Steam master port", "STEAM_MASTER_PORT", "Steam master server port.", "27021", true),
		},
		ConfigFiles: []ConfigFile{{Path: "galaxy/avorion_galaxy/server.ini", Format: "ini", Restart: true}},
		SourceRef:   survPelican("avorion"),
		Notes:       []string{"Galaxy settings live in galaxy/<galaxy name>/server.ini after the first start. Grant admin rights with the in-game /admin command or --admin <SteamID64> in the startup line."},
	}, "565060", "Avorion dedicated server.")
}

func survBarotrauma() Template {
	return survSteam(Template{
		ID: "ndl-barotrauma", Name: "Barotrauma", GameTitle: "Barotrauma",
		Category: "survival", Engine: "dotnet",
		Summary: "Barotrauma co-op submarine survival dedicated server.",
		Startup: "exec ./DedicatedServer -batchmode",
		Stop:    "exit", Done: "Server started",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096,
		Dependencies: []string{"libicu76"},
		Tags:         []string{"coop", "survival", "space", "submarine"},
		Aliases:      []string{"barotrauma", "baro", "bt"},
		ConfigFiles:  []ConfigFile{{Path: "serversettings.xml", Format: "xml", Restart: true}},
		SourceRef:    lgsmRef("bt"),
		DocsURL:      "https://barotraumagame.com/wiki/Hosting_a_Dedicated_Server",
		Notes:        []string{"Name, password, player count and the game and query ports are set in serversettings.xml (port and queryport attributes)."},
	}, "1026340", "Barotrauma dedicated server.")
}

const survColonyScript = `
if [ -f /mnt/server/.steam/sdk64/steamclient.so ]; then
  cp -f /mnt/server/.steam/sdk64/steamclient.so /mnt/server/steamclient.so
fi
`

func survColonySurvival() Template {
	return survSteam(Template{
		ID: "ndl-colony-survival", Name: "Colony Survival", GameTitle: "Colony Survival",
		Category: "sandbox", Engine: "unity",
		Summary: "Colony Survival dedicated server. Build and defend a voxel colony with friends.",
		Startup: "exec ./colonyserver.x86_64 -batchmode -nographics +server.world \"{{WORLD_NAME}}\" +server.networktype SteamOnline +server.name \"{{SERVER_NAME}}\" +server.maxplayers {{MAX_PLAYERS}} +server.gameport {{SERVER_PORT}} +server.ip 0.0.0.0 +server.steamport {{STEAM_PORT}}",
		Stop:    "^C", Done: "Starting networking type",
		InstallScript: survColonyScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27004, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27004, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "steam", ContainerPort: 27005, Protocol: "tcp", Env: "STEAM_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 2048, DefaultDiskMB: 4096,
		Tags:    []string{"colony", "voxel", "coop", "building"},
		Aliases: []string{"colony survival", "colonysurvival", "col"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Colony Survival", true),
			envText("World name", "WORLD_NAME", "World save loaded or created at start.", "world", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "10", true),
			envNumber("Game port", "SERVER_PORT", "Game port.", "27004", true),
			envNumber("Steam port", "STEAM_PORT", "Steam port.", "27005", true),
		},
		SourceRef: survPelican("colony_survival"),
		Notes:     []string{"steamclient.so is copied next to the server binary on install, as LinuxGSM does for this game."},
	}, "748090", "Colony Survival dedicated server.")
}

const survCraftopiaScript = `
if [ ! -f /mnt/server/ServerSetting.ini ]; then
  printf '[GameWorld]\nname=NoDAL\ndifficulty=1\ngameMode=1\n\n[Host]\nport=6587\nmaxPlayerNumber=7\nusePassword=0\nserverPassword=00000000\nbindAddress=0.0.0.0\n\n[Save]\nautoSaveSec=300\nautoSavePerHour=1\nsavePath=DedicatedServerSave/\n' > /mnt/server/ServerSetting.ini
fi
`

func survCraftopia() Template {
	return survSteam(Template{
		ID: "ndl-craftopia", Name: "Craftopia", GameTitle: "Craftopia",
		Category: "survival", Engine: "unity",
		Summary: "Craftopia dedicated server. Survival crafting, farming and automation on an island world.",
		Startup: "exec ./Craftopia.x86_64 -batchmode -showlogs",
		Stop:    "stop", Done: "World is loaded!",
		InstallScript: survCraftopiaScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 6587, Protocol: "udp", Primary: true, Fixed: true},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 12288,
		Tags:        []string{"crafting", "coop", "automation"},
		Aliases:     []string{"craftopia", "ct"},
		ConfigFiles: []ConfigFile{{Path: "ServerSetting.ini", Format: "ini", Restart: true}},
		SourceRef:   lgsmRef("ct"),
		Notes:       []string{"World name, difficulty, port, slots and the numeric PIN password are in ServerSetting.ini. A default is written on first install if the file is missing."},
	}, "1670340", "Craftopia dedicated server.")
}

const survEcoScript = `
for f in Network Localization; do
  if [ ! -f "/mnt/server/Configs/$f.eco" ] && [ -f "/mnt/server/Configs/$f.eco.template" ]; then
    cp "/mnt/server/Configs/$f.eco.template" "/mnt/server/Configs/$f.eco"
  fi
done
`

func survEco() Template {
	return survSteam(Template{
		ID: "ndl-eco", Name: "Eco", GameTitle: "Eco",
		Category: "simulation", Engine: "dotnet",
		Summary: "Eco dedicated server. A shared world where players build a civilisation without destroying the ecosystem.",
		Startup: "exec ./EcoServer -nogui $( [ -n \"$ECO_USER_TOKEN\" ] && printf -- '-userToken=%s' \"$ECO_USER_TOKEN\" )",
		Stop:    "exit", Done: "Web Server now listening on:", StopTimeout: 60,
		InstallScript: survEcoScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 3000, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "web", ContainerPort: 3001, Protocol: "tcp", Fixed: true},
		},
		DefaultMemoryMB: 6144, DefaultDiskMB: 10240,
		Dependencies: []string{"libgdiplus", "libicu76"},
		Tags:         []string{"simulation", "ecology", "economy", "coop"},
		Aliases:      []string{"eco", "eco global survival"},
		Variables: []Variable{
			envSecret("Strange Loop user token", "ECO_USER_TOKEN", "Server token from your play.eco account. Needed to publish the server to the Eco server browser.", false),
		},
		Requirements: []Requirement{{Kind: ReqToken, Stage: StageOptional, Env: "ECO_USER_TOKEN", Label: "Eco account user token to list the server publicly", URL: "https://play.eco/"}},
		Hint:         "Eco user token only to list publicly",
		ConfigFiles:  []ConfigFile{{Path: "Configs/Network.eco", Format: "json", Restart: true}},
		SourceRef:    lgsmRef("eco"),
		Notes:        []string{"Game port (GameServerPort, default 3000) and web port (WebServerPort, default 3001) are set in Configs/Network.eco, created from the shipped template on install."},
	}, "739590", "Eco dedicated server.")
}

const survFrozenFlameScript = `
CFG=/mnt/server/FrozenFlame/Saved/Config/LinuxServer
mkdir -p "$CFG"
if [ ! -f "$CFG/Game.ini" ]; then
  printf '[/Script/Engine.GameSession]\nMaxPlayers=%s\n\n[/Script/FrozenFlame.FGameSession]\nServerPassword=""\n' "${MAX_PLAYERS:-10}" > "$CFG/Game.ini"
fi
`

func survFrozenFlame() Template {
	return survSteam(Template{
		ID: "ndl-frozen-flame", Name: "Frozen Flame", GameTitle: "Frozen Flame",
		Category: "survival", Engine: "unreal4",
		Summary: "Frozen Flame dedicated server. Co-op fantasy survival RPG with building.",
		Startup: "exec ./FrozenFlame/Binaries/Linux/FrozenFlameServer-Linux-Shipping -log -MetaGameServerName=\"{{SERVER_NAME}}\" -port={{SERVER_PORT}} -queryPort={{QUERY_PORT}} -RconPort={{RCON_PORT}} -RconPassword=\"$RCON_PASSWORD\"",
		Stop:    "^C", Done: "Bringing up level for play took",
		InstallScript: survFrozenFlameScript,
		Capabilities:  []string{CapRCON},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 25575, Protocol: "udp", Env: "QUERY_PORT"},
			{Name: "rcon", ContainerPort: 27015, Protocol: "tcp", Env: "RCON_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 12288,
		Tags:    []string{"survival", "rpg", "coop", "building"},
		Aliases: []string{"frozen flame", "frozenflame"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Frozen Flame", true),
			envNumber("Max players", "MAX_PLAYERS", "Written into Game.ini on first install.", "10", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "25575", true),
			envNumber("RCON port", "RCON_PORT", "RCON TCP port.", "27015", true),
			generatedSecret("RCON password", "RCON_PASSWORD", "RCON password. Generated when left empty."),
		},
		ConfigFiles: []ConfigFile{{Path: "FrozenFlame/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
		SourceRef:   survPelican("frozen_flame"),
		DocsURL:     "https://github.com/DreamsideInteractive/FrozenFlameServer",
	}, "1348640", "Frozen Flame dedicated server.")
}

func survHumanitZ() Template {
	return survSteam(Template{
		ID: "ndl-humanitz", Name: "HumanitZ", GameTitle: "HumanitZ",
		Category: "survival", Engine: "custom",
		Summary: "HumanitZ dedicated server. Open world zombie survival with base building. Installs the Linux branch.",
		Startup: "exec ./HumanitZServer/Binaries/Linux/HumanitZServer-Linux-Shipping -log -port={{SERVER_PORT}} -queryport={{QUERY_PORT}} -steamservername=\"{{SERVER_NAME}}\"",
		Stop:    "^C", StopTimeout: 60,
		Install: &InstallSpec{Beta: "linuxbranch"},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 6144, DefaultDiskMB: 16384,
		Tags:    []string{"survival", "zombies", "coop", "pvp"},
		Aliases: []string{"humanitz", "hz"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL HumanitZ", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		ConfigFiles: []ConfigFile{{Path: "HumanitZServer/GameServerSettings.ini", Format: "ini", Restart: true}},
		SourceRef:   lgsmRef("hz"),
		Notes:       []string{"The Linux build is published on the linuxbranch beta. GameServerSettings.ini is generated on the first start."},
	}, "2728330", "HumanitZ dedicated server.")
}

func survHurtworld() Template {
	return survSteam(Template{
		ID: "ndl-hurtworld", Name: "Hurtworld", GameTitle: "Hurtworld",
		Category: "survival", Engine: "unity",
		Summary: "Hurtworld dedicated server. Hardcore multiplayer survival with vehicles and raiding.",
		Startup: "exec ./Hurtworld.x86_64 -batchmode -nographics -logfile /dev/stdout -exec \"host {{SERVER_PORT}} {{MAP}};queryport {{QUERY_PORT}};maxplayers {{MAX_PLAYERS}};servername {{SERVER_NAME}};creativemode {{CREATIVE_MODE}}\"",
		Stop:    "^C",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 12871, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 12881, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 3072, DefaultDiskMB: 8192,
		Dependencies: []string{"lib32z1"},
		Tags:         []string{"survival", "pvp", "vehicles"},
		Aliases:      []string{"hurtworld", "hw"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Hurtworld", true),
			envText("Map", "MAP", "Map loaded at boot.", "nullius", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "20", true),
			{Name: "Creative mode", Env: "CREATIVE_MODE", Description: "1 for free build, 0 for survival.", Default: "0", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"0", "1"}},
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "12871", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "12881", true),
		},
		SourceRef: lgsmRef("hw"),
	}, "405100", "Hurtworld dedicated server.")
}

const survNecesseScript = `
if [ ! -f /mnt/server/cfg/server.cfg ]; then
  mkdir -p /mnt/server/cfg
  printf 'SERVER = {\n\tport = 14159,\n\tslots = %s,\n\tpassword = ,\n\tmaxClientLatencySeconds = 30,\n\tpauseWhenEmpty = true,\n\tgiveClientsPower = true,\n\tlogging = true,\n\tlanguage = en,\n\tunloadLevelsCooldown = 30,\n\tworldBorderSize = -1,\n\tdroppedItemsLifeMinutes = 0,\n\tunloadSettlements = false,\n\tmaxSettlementsPerPlayer = -1,\n\tmaxSettlersPerSettlement = -1,\n\tjobSearchRange = 100,\n\tzipSaves = true,\n\tMOTD = \n}\n' "${MAX_PLAYERS:-10}" > /mnt/server/cfg/server.cfg
fi
`

func survNecesse() Template {
	return survSteam(Template{
		ID: "ndl-necesse", Name: "Necesse", GameTitle: "Necesse",
		Category: "survival", Engine: "java",
		Summary: "Necesse dedicated server. Top-down sandbox survival with settlements and settlers.",
		Startup: "exec ./jre/bin/java -Xms128M -Xmx$(( {{SERVER_MEMORY}} * 3 / 4 ))M -jar Server.jar -localdir -nogui -world \"{{WORLD_NAME}}\"",
		Stop:    "stop", Done: "Type help for list of commands.",
		InstallScript: survNecesseScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 14159, Protocol: "udp", Primary: true, Fixed: true},
		},
		DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096,
		Tags:    []string{"survival", "sandbox", "colony", "coop"},
		Aliases: []string{"necesse", "nec"},
		Variables: []Variable{
			envText("World name", "WORLD_NAME", "Save loaded or created at start.", "world", true),
			envNumber("Player slots", "MAX_PLAYERS", "Written into cfg/server.cfg on first install.", "10", true),
		},
		ConfigFiles: []ConfigFile{{Path: "cfg/server.cfg", Format: "cfg", Restart: true}},
		SourceRef:   survPelican("necesse"),
		Notes:       []string{"Uses the Java runtime bundled with the server. Port, slots and password live in cfg/server.cfg."},
	}, "1169370", "Necesse dedicated server.")
}

const survNightingaleScript = `
if [ ! -f /mnt/server/NWX/Config/ServerSettings.ini ] && [ -f /mnt/server/NWX/Config/ExampleServerSettings.ini ]; then
  cp /mnt/server/NWX/Config/ExampleServerSettings.ini /mnt/server/NWX/Config/ServerSettings.ini
fi
`

func survNightingale() Template {
	return survSteam(Template{
		ID: "ndl-nightingale", Name: "Nightingale", GameTitle: "Nightingale",
		Category: "survival", Engine: "unreal5",
		Summary: "Nightingale dedicated server. Gaslamp fantasy survival crafting across the Fae Realms.",
		Startup: "exec ./NWX/Binaries/Linux/NWXServer-Linux-Shipping -port={{SERVER_PORT}} -multihome=0.0.0.0 -ini:Game:[/Script/Engine.GameSession]:MaxPlayers={{MAX_PLAYERS}} -ini:ServerSettings:[/Script/NWX.NWXServerSettings]:Password=\"$SERVER_PASSWORD\" -ini:ServerSettings:[/Script/NWX.NWXServerSettings]:AdminPassword=\"$ADMIN_PASSWORD\" -ini:ServerSettings:[/Script/NWX.NWXServerSettings]:StartingDifficulty={{DIFFICULTY}}",
		Stop:    "^C", Done: "GameModeAwaiter became ready", StopTimeout: 60,
		InstallScript: survNightingaleScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		},
		DefaultMemoryMB: 8192, DefaultDiskMB: 20480, DefaultCPUs: 4,
		Tags:    []string{"survival", "crafting", "coop", "fantasy"},
		Aliases: []string{"nightingale", "nwx"},
		Variables: []Variable{
			envNumber("Max players", "MAX_PLAYERS", "Slot count. More than 6 is not supported upstream.", "6", true),
			{Name: "Starting difficulty", Env: "DIFFICULTY", Description: "Difficulty of the starting realm.", Default: "easy", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"easy", "medium", "hard", "extreme"}},
			envSecret("Join password", "SERVER_PASSWORD", "Optional connection password.", false),
			envSecret("Admin password", "ADMIN_PASSWORD", "Optional in-game admin password for kicking and banning.", false),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
		},
		ConfigFiles: []ConfigFile{{Path: "NWX/Config/ServerSettings.ini", Format: "ini", Restart: true}},
		SourceRef:   survPelican("nightingale"),
	}, "3796810", "Nightingale dedicated server.")
}

const survRisingWorldScript = `
if [ ! -f /mnt/server/server.properties ] && [ -f /mnt/server/server.example.properties ]; then
  cp /mnt/server/server.example.properties /mnt/server/server.properties
fi
`

func survRisingWorld() Template {
	return survSteam(Template{
		ID: "ndl-rising-world", Name: "Rising World", GameTitle: "Rising World",
		Category: "sandbox", Engine: "unity",
		Summary: "Rising World dedicated server (current Unity version). Open world sandbox building and survival.",
		Startup: "export LD_LIBRARY_PATH=./linux64:$LD_LIBRARY_PATH; exec ./RisingWorldServer.x64 +Server_Port={{SERVER_PORT}}",
		Stop:    "quit", Done: "Dedicated server is ready!",
		Install:       &InstallSpec{Beta: "unity"},
		InstallScript: survRisingWorldScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 4255, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "game-tcp", ContainerPort: 4255, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 4254, Protocol: "tcp"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:    []string{"sandbox", "building", "survival"},
		Aliases: []string{"rising world", "risingworld", "rw"},
		Variables: []Variable{
			envNumber("Game port", "SERVER_PORT", "Game port (UDP and TCP). The query port is always game port - 1 (TCP).", "4255", true),
		},
		ConfigFiles: []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}},
		SourceRef:   lgsmRef("rw"),
		Notes:       []string{"Installs the unity branch. Server name, password, game mode and RCON (port 4253, disabled by default) are set in server.properties."},
	}, "339010", "Rising World dedicated server.")
}

func survRisingWorldLegacy() Template {
	ports := []Port{
		{Name: "game", ContainerPort: 4255, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "game-tcp", ContainerPort: 4255, Protocol: "tcp", Env: "SERVER_PORT"},
		{Name: "query", ContainerPort: 4254, Protocol: "tcp"},
	}
	for p := 4256; p <= 4259; p++ {
		ports = append(ports,
			Port{Name: "game-" + strconv.Itoa(p) + "-udp", ContainerPort: p, Protocol: "udp"},
			Port{Name: "game-" + strconv.Itoa(p) + "-tcp", ContainerPort: p, Protocol: "tcp"})
	}
	return survSteam(Template{
		ID: "ndl-rising-world-legacy", Name: "Rising World (Java legacy)", GameTitle: "Rising World",
		Category: "sandbox", Engine: "java",
		Summary: "Rising World legacy Java version dedicated server, installed from the legacy branch.",
		Images:  map[string]string{"Java 8": "eclipse-temurin:8-jre"}, DefaultImage: "eclipse-temurin:8-jre",
		Startup: "export LD_LIBRARY_PATH=./linux64:$LD_LIBRARY_PATH; exec java -Xmx$(( {{SERVER_MEMORY}} * 3 / 4 ))M -jar server.jar +maxplayer={{MAX_PLAYERS}} +serverport={{SERVER_PORT}} +servername=\"{{SERVER_NAME}}\"",
		Stop:    "shutdown", Done: "RISING WORLD SERVER STARTED",
		Install:         &InstallSpec{Beta: "legacy"},
		DefaultPorts:    ports,
		DefaultMemoryMB: 4096, MinMemoryMB: 2048, DefaultDiskMB: 6144,
		Tags:    []string{"sandbox", "building", "classic"},
		Aliases: []string{"rising world legacy", "rising world java", "rwl"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Rising World", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "8", true),
			envNumber("Game port", "SERVER_PORT", "First game port. The server also uses the next four ports and game port - 1 for queries.", "4255", true),
		},
		ConfigFiles: []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}},
		SourceRef:   survPelican("rising_world/legacy"),
		Notes:       []string{"The legacy Java version is a separate branch of app 339010 and does not interoperate with the current Unity version. Runs on Java 8 as in the Pelican egg. RCON (4253) is configured in server.properties."},
	}, "339010", "Rising World dedicated server (legacy branch).")
}

func survSmalland() Template {
	return survSteam(Template{
		ID: "ndl-smalland", Name: "Smalland: Survive the Wilds", GameTitle: "Smalland: Survive the Wilds",
		Category: "survival", Engine: "custom",
		Summary: "Smalland dedicated server. Tiny-scale co-op survival in a giant world.",
		Startup: "exec ./SMALLAND/Binaries/Linux/SMALLANDServer-Linux-Shipping SMALLAND \"/Game/Maps/WorldGame/WorldGame_Smalland?SERVERNAME=\\\"{{SERVER_NAME}}\\\"?WORLDNAME=\\\"{{WORLD_NAME}}\\\"?lengthofdayseconds=1800?lengthofseasonseconds=10800?creaturehealthmodifier=100?creaturedamagemodifier=100?nourishmentlossmodifier=100?falldamagemodifier=100\" -ini:Engine:[EpicOnlineServices]:DeploymentId=50f2b148496e4cbbbdeefbecc2ccd6a3 -ini:Engine:[EpicOnlineServices]:DedicatedServerClientId=xyza78918KT08TkA6emolUay8yhvAAy2 -ini:Engine:[EpicOnlineServices]:DedicatedServerClientSecret=aN2GtVw7aHb6hx66HwohNM+qktFaO3vtrLSbGdTzZWk -ini:Engine:[EpicOnlineServices]:DedicatedServerPrivateKey= -port={{SERVER_PORT}} -NOSTEAM -log",
		Stop:    "^C", Done: "is being set as 'listening'",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 12288,
		Tags:    []string{"survival", "coop", "crafting"},
		Aliases: []string{"smalland", "survive the wilds"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Smalland", true),
			envText("World name", "WORLD_NAME", "Save game name.", "World", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
		},
		SourceRef: survPelican("smalland_survive_the_wilds"),
		Notes:     []string{"Uses Epic Online Services for listing. The EOS deployment and client values in the startup line are the shared ones every Smalland server uses, taken from the Pelican egg; they are not operator secrets."},
	}, "808040", "Smalland: Survive the Wilds dedicated server.")
}

const survSolaceScript = `
DIR="/mnt/server/.config/unity3d/Big Kitty Games/Solace Crafting"
mkdir -p "$DIR"
if [ ! -f "$DIR/servercfg.dat" ]; then
  printf '{\n    "name": "No-DAL Solace Crafting",\n    "description": "",\n    "port": 27015,\n    "steamQueryPort": 27016,\n    "isPrivate": false,\n    "password": "",\n    "requireSteamID": true,\n    "maxPlayers": 12,\n    "allowAdmin": false,\n    "adminPassword": "",\n    "allowModerator": false,\n    "moderatorPassword": "",\n    "worldSaveToUse": "MultiplayerWorld",\n    "autoRestart": 0\n}\n' > "$DIR/servercfg.dat"
fi
`

func survSolaceCrafting() Template {
	return survSteam(Template{
		ID: "ndl-solace-crafting", Name: "Solace Crafting", GameTitle: "Solace Crafting",
		Category: "survival", Engine: "unity",
		Summary: "Solace Crafting dedicated server. Open world survival crafting RPG.",
		Startup: "exec \"./Solace Crafting.x86_64\"",
		Stop:    "^C", Done: "server started",
		InstallScript: survSolaceScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:        []string{"survival", "crafting", "rpg"},
		Aliases:     []string{"solace crafting", "solace"},
		ConfigFiles: []ConfigFile{{Path: ".config/unity3d/Big Kitty Games/Solace Crafting/servercfg.dat", Format: "json", Restart: true}},
		SourceRef:   survPelican("solace_crafting"),
		Notes:       []string{"Name, ports, slots and passwords are in servercfg.dat under .config/unity3d/Big Kitty Games/Solace Crafting, written on first install."},
	}, "1086950", "Solace Crafting dedicated server.")
}

func survSoulmask() Template {
	return survSteam(Template{
		ID: "ndl-soulmask", Name: "Soulmask", GameTitle: "Soulmask",
		Category: "survival", Engine: "unreal4",
		Summary: "Soulmask dedicated server. Tribal survival where you lead a clan of recruited followers.",
		Startup: "exec ./WS/Binaries/Linux/WSServer-Linux-Shipping WS Level01_Main -server -SILENT -SteamServerName=\"{{SERVER_NAME}}\" -MaxPlayers={{MAX_PLAYERS}} -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -EchoPort=18888 -PSW=\"$SERVER_PASSWORD\" -adminpsw=\"$ADMIN_PASSWORD\" -{{GAME_MODE}} -initbackup -backupinterval=15 -UTF8Output -forcepassthrough -MULTIHOME=0.0.0.0 -log",
		Stop:    "^C", Done: "Create Dungeon Successed", StopTimeout: 90,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 8777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "game-tcp", ContainerPort: 8777, Protocol: "tcp", Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 16384, MinMemoryMB: 10240, DefaultDiskMB: 20480, DefaultCPUs: 4,
		Tags:    []string{"survival", "tribe", "pve", "pvp"},
		Aliases: []string{"soulmask", "sm"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Soulmask", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "50", true),
			{Name: "Game mode", Env: "GAME_MODE", Description: "pve or pvp.", Default: "pve", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"pve", "pvp"}},
			envSecret("Join password", "SERVER_PASSWORD", "Optional server password.", false),
			generatedSecret("Admin password", "ADMIN_PASSWORD", "GM password (gm key <password> in game). Generated when left empty."),
			envNumber("Game port", "SERVER_PORT", "Game port (UDP and TCP).", "8777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		SourceRef: lgsmRef("sm"),
		Notes:     []string{"Upstream recommends 16 GB RAM and 2 to 4 cores per server. The maintenance telnet port 18888 is bound inside the container and not published. LinuxGSM stops the server over that telnet port; No-DAL sends Ctrl-C."},
	}, "3017300", "Soulmask dedicated server.")
}

func survStarbound() Template {
	return requireSteamOwner(survSteam(Template{
		ID: "ndl-starbound", Name: "Starbound", GameTitle: "Starbound",
		Category: "sandbox", Engine: "custom",
		Summary: "Starbound dedicated server. 2D space exploration sandbox.",
		Startup: "cd linux && exec ./starbound_server",
		Stop:    "^C", Done: "Starting UniverseServer",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 21025, Protocol: "tcp", Primary: true, Fixed: true},
		},
		DefaultMemoryMB: 2048, DefaultDiskMB: 8192,
		Tags:        []string{"space", "sandbox", "2d", "coop"},
		Aliases:     []string{"starbound", "sb"},
		ConfigFiles: []ConfigFile{{Path: "storage/starbound_server.config", Format: "json", Restart: true}},
		SourceRef:   lgsmRef("sb"),
		Notes:       []string{"The server ships inside the game app (211820), so SteamCMD needs an account that owns Starbound. storage/starbound_server.config is generated on first start; the game port is gameServerPort there."},
	}, "211820", "Starbound (game app, contains the dedicated server)."), "Steam account that owns Starbound")
}

func survStationeers() Template {
	return survSteam(Template{
		ID: "ndl-stationeers", Name: "Stationeers", GameTitle: "Stationeers",
		Category: "simulation", Engine: "unity",
		Summary: "Stationeers dedicated server. Detailed space station construction and management.",
		Startup: "exec ./rocketstation_DedicatedServer.x86_64 -file start \"{{SAVE_NAME}}\" {{WORLD}} {{DIFFICULTY}} {{START_CONDITION}} {{START_LOCATION}} -settings StartLocalHost true ServerVisible true GamePort {{SERVER_PORT}} UpdatePort {{QUERY_PORT}} UPNPEnabled false ServerName \"{{SERVER_NAME}}\" ServerPassword \"$SERVER_PASSWORD\" ServerMaxPlayers {{MAX_PLAYERS}} AutoSave true SaveInterval 300 ServerAuthSecret \"$SERVER_AUTH_SECRET\" AutoPauseServer true UseSteamP2P false LocalIpAddress 0.0.0.0",
		Stop:    "quit", Done: "Host override address provided", StopTimeout: 60,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27500, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 1024, DefaultDiskMB: 8192,
		Tags:    []string{"space", "simulation", "engineering", "coop"},
		Aliases: []string{"stationeers", "st"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Stationeers", true),
			envText("Save name", "SAVE_NAME", "Save auto-loaded and auto-saved by the server.", "station", true),
			{Name: "World", Env: "WORLD", Description: "World used when the save is first created.", Default: "Mars2", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"Lunar", "Mars2", "Europa3", "MimasHerschel", "Vulcan", "Venus"}},
			{Name: "Difficulty", Env: "DIFFICULTY", Description: "Difficulty used when the save is first created.", Default: "Normal", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"Creative", "Easy", "Normal", "Stationeer"}},
			{Name: "Start condition", Env: "START_CONDITION", Description: "Starting kit.", Default: "DefaultStart", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"DefaultStart", "DefaultStartCommunity", "Brutal", "BrutalCommunity"}},
			envText("Start location", "START_LOCATION", "Spawn location. Must belong to the chosen world (MarsSpawnButchersFlat is a Mars2 location).", "MarsSpawnButchersFlat", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "10", true),
			envSecret("Join password", "SERVER_PASSWORD", "Optional server password.", false),
			generatedSecret("Auth secret", "SERVER_AUTH_SECRET", "Server auth secret used for remote administration. Generated when left empty."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "27500", true),
			envNumber("Update/query port", "QUERY_PORT", "Steam update (query) port.", "27015", true),
		},
		SourceRef: survPelican("stationeers/stationeers_vanilla"),
	}, "600760", "Stationeers dedicated server.")
}

func survTheFront() Template {
	return survSteam(Template{
		ID: "ndl-the-front", Name: "The Front", GameTitle: "The Front",
		Category: "survival", Engine: "unreal4",
		Summary: "The Front dedicated server. Open world survival shooter with base building.",
		Startup: "exec ./ProjectWar/Binaries/Linux/TheFrontServer \"ProjectWar_Start?DedicatedServer?MaxPlayers={{MAX_PLAYERS}}\" -server -game -log -MultiHome=0.0.0.0 -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -BeaconPort={{BEACON_PORT}} -ShutDownServicePort={{SHUTDOWN_PORT}} -ConfigServerName=\"{{SAVE_NAME}}\" -UserDir=\"$HOME/TheFrontManager/{{SAVE_NAME}}\" -ServerName=\"{{SERVER_NAME}}\" -ServerPassword=\"$SERVER_PASSWORD\" -ServerFightModeType={{GAME_MODE}} -EnableParallelCharacterMovementTickFunction -EnableParallelCharacterTickFunction -UseDynamicPhysicsScene -Game.PhysicsVehicle=false -ansimalloc -Game.MaxFrameRate=35 $( [ -n \"{{PUBLIC_IP}}\" ] && echo -OutIPAddress={{PUBLIC_IP}} )",
		Stop:    "^C", StopTimeout: 60,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 5001, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "beacon", ContainerPort: 5002, Protocol: "udp", Env: "BEACON_PORT"},
			{Name: "shutdown", ContainerPort: 5003, Protocol: "tcp", Env: "SHUTDOWN_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 8192, DefaultDiskMB: 24576, DefaultCPUs: 4,
		Tags:    []string{"survival", "pvp", "pve", "building"},
		Aliases: []string{"the front", "thefront", "tf"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL The Front", true),
			envText("Save name", "SAVE_NAME", "Save and config folder name. No spaces.", "server", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "40", true),
			{Name: "Game mode", Env: "GAME_MODE", Description: "0 PvP, 1 PvE.", Default: "0", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"0", "1"}},
			envSecret("Join password", "SERVER_PASSWORD", "Optional server password.", false),
			envText("Public IP", "PUBLIC_IP", "Optional public IP passed as -OutIPAddress for server listing.", "", false),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "5001", true),
			envNumber("Beacon port", "BEACON_PORT", "UDP beacon port.", "5002", true),
			envNumber("Shutdown service port", "SHUTDOWN_PORT", "TCP shutdown service port.", "5003", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		SourceRef: lgsmRef("tf"),
	}, "2334200", "The Front dedicated server.")
}

const survTheIsleScript = `
CFG=/mnt/server/TheIsle/Saved/Config/LinuxServer
mkdir -p "$CFG"
if [ ! -f "$CFG/Game.ini" ]; then
  printf '[/Script/TheIsle.TIGameSession]\nServerName=%s\nMaxPlayerCount=%s\nbRconEnabled=false\nRconPort=9999\nbQueueEnabled=false\nQueuePort=9998\nbServerPassword=false\nServerPassword=\nbServerDynamicWeather=true\nbAllowReplay=true\nbEnableHumans=false\n' "${SERVER_NAME:-No-DAL The Isle}" "${MAX_PLAYERS:-100}" > "$CFG/Game.ini"
fi
`

func survTheIsle() Template {
	return survSteam(Template{
		ID: "ndl-the-isle", Name: "The Isle (Evrima)", GameTitle: "The Isle",
		Category: "survival", Engine: "unreal5",
		Summary: "The Isle Evrima dedicated server. Dinosaur survival. Installs the evrima branch.",
		Startup: "exec ./TheIsle/Binaries/Linux/TheIsleServer-Linux-Shipping /Game/TheIsle/Maps/Game/Gateway/Gateway -Port={{SERVER_PORT}} -log -ini:Engine:[EpicOnlineServices]:DedicatedServerClientId=xyza7891gk5PRo3J7G9puCJGFJjmEguW -ini:Engine:[EpicOnlineServices]:DedicatedServerClientSecret=pKWl6t5i9NJK8gTpVlAxzENZ65P8hYzodV8Dqe5Rlc8",
		Stop:    "^C", Done: "Session started successfully", StopTimeout: 60,
		Install:       &InstallSpec{Beta: "evrima"},
		InstallScript: survTheIsleScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "queue", ContainerPort: 9998, Protocol: "tcp", Fixed: true},
			{Name: "rcon", ContainerPort: 9999, Protocol: "tcp", Fixed: true},
		},
		DefaultMemoryMB: 6144, MinMemoryMB: 3072, DefaultDiskMB: 20480, DefaultCPUs: 4,
		Tags:    []string{"survival", "dinosaurs", "pvp"},
		Aliases: []string{"the isle", "theisle", "evrima", "ti"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Written into Game.ini on first install.", "No-DAL The Isle", true),
			envNumber("Max players", "MAX_PLAYERS", "Written into Game.ini on first install.", "100", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
		},
		ConfigFiles: []ConfigFile{{Path: "TheIsle/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
		SourceRef:   lgsmRef("ti"),
		Notes: []string{
			"The Evrima build is published on the evrima beta branch; the legacy build is not Linux-native.",
			"The EOS client ID and secret in the startup line are the shared values from the LinuxGSM and Pelican default configs, needed for the server to register; they are not operator secrets.",
			"RCON (9999) and the queue (9998) are disabled in the default Game.ini; enable them there if you need them.",
		},
	}, "412680", "The Isle dedicated server.")
}

const survVeinScript = `
CFG=/mnt/server/Vein/Saved/Config/LinuxServer
mkdir -p "$CFG"
if [ ! -f "$CFG/Game.ini" ]; then
  printf '[/Script/Engine.GameSession]\nMaxPlayers=%s\n\n[/Script/Vein.VeinGameSession]\nbPublic=True\nServerName=%s\nBindAddr=0.0.0.0\nHeartbeatInterval=30.0\nPassword=\n\n[OnlineSubsystemSteam]\nGameServerQueryPort=%s\nbVACEnabled=0\n' "${MAX_PLAYERS:-16}" "${SERVER_NAME:-No-DAL Vein}" "${QUERY_PORT:-27015}" > "$CFG/Game.ini"
fi
`

func survVein() Template {
	return survSteam(Template{
		ID: "ndl-vein", Name: "VEIN", GameTitle: "VEIN",
		Category: "survival", Engine: "unreal5",
		Summary: "VEIN dedicated server. Post-apocalyptic survival in a large open world.",
		Startup: "exec ./Vein/Binaries/Linux/VeinServer-Linux-Test -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -log",
		Stop:    "^C", Done: "Created session GameSession",
		InstallScript: survVeinScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 3072, DefaultDiskMB: 8192,
		Tags:    []string{"survival", "zombies", "coop"},
		Aliases: []string{"vein"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Written into Game.ini on first install.", "No-DAL Vein", true),
			envNumber("Max players", "MAX_PLAYERS", "Written into Game.ini on first install.", "16", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		ConfigFiles: []ConfigFile{{Path: "Vein/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
		SourceRef:   survPelican("vein"),
		DocsURL:     "https://ramjet.notion.site/Server-Hosting-85f92f43f32548c1b5b33797ddf456ad",
	}, "2131400", "VEIN dedicated server.")
}

const survWurmScript = `
mkdir -p /mnt/server/nativelibs
if [ -f /mnt/server/.steam/sdk64/steamclient.so ]; then
  cp -f /mnt/server/.steam/sdk64/steamclient.so /mnt/server/nativelibs/steamclient.so
fi
`

func survWurm() Template {
	return survSteam(Template{
		ID: "ndl-wurm-unlimited", Name: "Wurm Unlimited", GameTitle: "Wurm Unlimited",
		Category: "sandbox", Engine: "java",
		Summary: "Wurm Unlimited dedicated server. Sandbox MMO-style world of terraforming, crafting and building.",
		Startup: "exec ./WurmServerLauncher ip=0.0.0.0 externalport={{SERVER_PORT}} queryport={{QUERY_PORT}} start={{WORLD}} maxplayers={{MAX_PLAYERS}} servername=\"{{SERVER_NAME}}\" adminpwd=\"$ADMIN_PASSWORD\" serverpassword=\"$SERVER_PASSWORD\" epicsettings=false",
		Stop:    "shutdown", Done: "Wurm Server launcher finished at", StopTimeout: 60,
		InstallScript: survWurmScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 3724, Protocol: "tcp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27016, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 2048, DefaultDiskMB: 6144,
		Tags:    []string{"sandbox", "mmo", "building", "crafting"},
		Aliases: []string{"wurm", "wurm unlimited"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Wurm", true),
			{Name: "World", Env: "WORLD", Description: "World folder to launch. The server ships dist/Creative and dist/Adventure.", Default: "dist/Creative", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"dist/Creative", "dist/Adventure"}},
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "200", true),
			generatedSecret("Admin password", "ADMIN_PASSWORD", "Unlocks in-game admin commands. Generated when left empty."),
			envSecret("Join password", "SERVER_PASSWORD", "Optional server password.", false),
			envNumber("Game port", "SERVER_PORT", "TCP game port.", "3724", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port (27016 to 27030).", "27016", true),
		},
		SourceRef: survPelican("wurm_unlimited"),
		Notes:     []string{"steamclient.so is copied into nativelibs on install, as LinuxGSM does. Uses the Java runtime bundled with the server."},
	}, "402370", "Wurm Unlimited dedicated server.")
}

const survModiverseScript = `
DIR=/mnt/server/KJMod/Binaries/Linux/ServerData
mkdir -p "$DIR"
if [ ! -f "$DIR/ServerConfiguration.json" ]; then
  printf '{\n  "version": 1,\n  "pin": "0000",\n  "motdURL": "",\n  "motdDelay": 5,\n  "rconPassword": "%s",\n  "mapCycle": [\n    {"gameMode":"2285067974","comment":"Deathrun, Deathrun Example Map","map":"2286680373","assets":["2229481041"],"duration":1800}\n  ]\n}\n' "${RCON_PASSWORD}" > "$DIR/ServerConfiguration.json"
fi
if [ -f /mnt/server/KJMod/Binaries/Linux/KJModServer ]; then
  chmod +x /mnt/server/KJMod/Binaries/Linux/KJModServer
fi
`

func survModiverse() Template {
	return survSteam(Template{
		ID: "ndl-modiverse", Name: "Modiverse", GameTitle: "Modiverse",
		Category: "sandbox", Engine: "custom",
		Summary: "Modiverse dedicated server. Community-made game modes and maps from the Modiverse workshop.",
		Startup: "exec ./KJMod/Binaries/Linux/KJModServer -port={{SERVER_PORT}} -queryport={{QUERY_PORT}} -SteamServerName=\"{{SERVER_NAME}}\" -KJModBaseUGCFolder=ServerData -DoNotRestartOnEmpty -InitUGCs -maxplayers={{MAX_PLAYERS}}",
		Stop:    "^C", Done: "listening on port",
		InstallScript: survModiverseScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:    []string{"sandbox", "party", "user-generated"},
		Aliases: []string{"modiverse", "kjmod"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Modiverse", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count, 2 to 128.", "32", true),
			generatedSecret("RCON password", "RCON_PASSWORD", "Written into ServerConfiguration.json on first install. Generated when left empty."),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		ConfigFiles: []ConfigFile{{Path: "KJMod/Binaries/Linux/ServerData/ServerConfiguration.json", Format: "json", Restart: true}},
		SourceRef:   survPelican("modiverse"),
		Notes:       []string{"The map cycle in ServerConfiguration.json is the Deathrun example from the Pelican egg; replace the workshop IDs with the game modes and maps you want."},
	}, "1549820", "Modiverse dedicated server.")
}

const survAstroColonyScript = `
CFG=/mnt/server/AstroColony/Saved/Config/LinuxServer
mkdir -p "$CFG"
if [ ! -f "$CFG/ServerSettings.ini" ]; then
  printf '[/Script/ACFeature.EHServerSubsystem]\nServerPassword=\nSeed=7300\nMapName=%s\nMaxPlayers=%s\nShouldLoadLatestSavegame=True\nAdminList=\nSharedTechnologies=True\nOxygenConsumption=True\nFreeConstruction=False\nAutosaveInterval=5.0\nAutosavesCount=10\n' "${SERVER_NAME:-No-DAL Astro Colony}" "${MAX_PLAYERS:-5}" > "$CFG/ServerSettings.ini"
fi
`

func survAstroColony() Template {
	return survSteam(Template{
		ID: "ndl-astro-colony", Name: "Astro Colony", GameTitle: "Astro Colony",
		Category: "simulation", Engine: "custom",
		Summary: "Astro Colony dedicated server. Co-op space colony building and automation.",
		Startup: "exec ./AstroColony/Binaries/Linux/AstroColonyServer -log -QueryPort={{QUERY_PORT}} -SteamServerName=\"{{SERVER_NAME}}\"",
		Stop:    "^C", Done: "server create success",
		InstallScript: survAstroColonyScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:    []string{"space", "colony", "automation", "coop"},
		Aliases: []string{"astro colony", "astrocolony"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server list.", "No-DAL Astro Colony", true),
			envNumber("Max players", "MAX_PLAYERS", "Written into ServerSettings.ini on first install.", "5", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		ConfigFiles: []ConfigFile{{Path: "AstroColony/Saved/Config/LinuxServer/ServerSettings.ini", Format: "ini", Restart: true}},
		SourceRef:   survPelican("astro_colony"),
		Notes:       []string{"The game port is fixed at 7777; upstream notes that changing it stops players from joining."},
	}, "2934900", "Astro Colony dedicated server.")
}

func survBrickadia() Template {
	return survSteam(Template{
		ID: "ndl-brickadia", Name: "Brickadia", GameTitle: "Brickadia",
		Category: "sandbox", Engine: "unreal5",
		Summary: "Brickadia dedicated server. Multiplayer brick building sandbox.",
		Startup: "exec ./Brickadia/Binaries/Linux/BrickadiaServer-Linux-Shipping -token=\"$BRICKADIA_TOKEN\" -port={{SERVER_PORT}} $( [ -n \"{{WORLD_NAME}}\" ] && printf -- '-world %s' \"{{WORLD_NAME}}\" )",
		Stop:    "quit", Done: "Posting server.",
		StartRequires: []string{"BRICKADIA_TOKEN"},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		},
		DefaultMemoryMB: 4096, DefaultDiskMB: 8192,
		Tags:    []string{"sandbox", "building"},
		Aliases: []string{"brickadia", "bricks"},
		Variables: []Variable{
			envSecret("Server token", "BRICKADIA_TOKEN", "Hosting token generated on the Brickadia website. The server cannot host without it.", false),
			envText("World", "WORLD_NAME", "Optional world to load at startup.", "", false),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
		},
		Requirements: []Requirement{{Kind: ReqToken, Stage: StageStart, Env: "BRICKADIA_TOKEN", Label: "Brickadia server hosting token", URL: "https://brickadia.com/"}},
		Hint:         "Brickadia hosting token required to start",
		SourceRef:    survPelican("brickadia"),
	}, "3017590", "Brickadia dedicated server.")
}

func survCryoFall() Template {
	return survSteam(Template{
		ID: "ndl-cryofall", Name: "CryoFall", GameTitle: "CryoFall",
		Category: "survival", Engine: "dotnet",
		Summary: "CryoFall dedicated server. Sci-fi colony survival with an economy.",
		Images:  map[string]string{".NET 8": "mcr.microsoft.com/dotnet/runtime:8.0"}, DefaultImage: "mcr.microsoft.com/dotnet/runtime:8.0",
		Startup: "export DOTNET_ROLL_FORWARD=Major; exec dotnet Binaries/Server/CryoFall_Server.dll loadOrNew",
		Stop:    "stop 10 stopping server", Done: "Socket-server listening on", StopTimeout: 45,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 6000, Protocol: "udp", Primary: true, Fixed: true},
		},
		DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096,
		Tags:      []string{"survival", "colony", "sci-fi", "pvp"},
		Aliases:   []string{"cryofall", "cryo fall"},
		SourceRef: survPelican("cryofall"),
		Notes:     []string{"The Pelican egg runs this server on .NET 6. No-DAL ships the .NET 8 runtime and sets DOTNET_ROLL_FORWARD=Major so the .NET 6 server runs on it. The game port (default 6000) is set in SettingsServer.xml."},
	}, "1061710", "CryoFall dedicated server.")
}

func survDayOfDragons() Template {
	return survSteam(Template{
		ID: "ndl-day-of-dragons", Name: "Day of Dragons", GameTitle: "Day of Dragons",
		Category: "survival", Engine: "unreal4",
		Summary: "Day of Dragons dedicated server. Multiplayer dragon survival.",
		Startup: "exec ./Dragons/Binaries/Linux/DragonsServer-Linux-Shipping -MultiHome=0.0.0.0 -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -SteamServerName=\"{{SERVER_NAME}}\" -log",
		Stop:    "^C",
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 2048, DefaultDiskMB: 12288,
		Tags:    []string{"survival", "dragons", "pvp"},
		Aliases: []string{"day of dragons", "dayofdragons", "dodr"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Name shown in the server browser.", "No-DAL Day of Dragons", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
			envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
		},
		ConfigFiles: []ConfigFile{{Path: "Dragons/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
		SourceRef:   lgsmRef("dodr"),
	}, "1088320", "Day of Dragons dedicated server.")
}

const survSTNScript = `
mkdir -p /mnt/server/Config
if [ ! -f /mnt/server/Config/ServerConfig.txt ]; then
  printf 'ServerIP=\nServerPort=7950\nServerOwner=\nServerName="%s"\nServerPassword=\n' "${SERVER_NAME:-No-DAL Survive the Nights}" > /mnt/server/Config/ServerConfig.txt
fi
`

func survSurviveTheNights() Template {
	return survSteam(Template{
		ID: "ndl-survive-the-nights", Name: "Survive the Nights", GameTitle: "Survive the Nights",
		Category: "survival", Engine: "unity",
		Summary:       "Survive the Nights dedicated server. Open world zombie survival with building.",
		Startup:       "exec ./Server_Linux_x64 -mc {{MAX_PLAYERS}} -r {{REGION}}",
		Stop:          "^C",
		InstallScript: survSTNScript,
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7950, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "query", ContainerPort: 7951, Protocol: "udp", Fixed: true},
		},
		DefaultMemoryMB: 4096, MinMemoryMB: 3072, DefaultDiskMB: 8192,
		Tags:    []string{"survival", "zombies", "coop"},
		Aliases: []string{"survive the nights", "stn"},
		Variables: []Variable{
			envText("Server name", "SERVER_NAME", "Written into Config/ServerConfig.txt on first install.", "No-DAL Survive the Nights", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count.", "20", true),
			envNumber("Region", "REGION", "Server region index passed with -r.", "0", true),
		},
		ConfigFiles: []ConfigFile{{Path: "Config/ServerConfig.txt", Format: "properties", Restart: true}},
		SourceRef:   lgsmRef("stn"),
		Notes:       []string{"The game port is ServerPort in Config/ServerConfig.txt (default 7950); the query port is game port + 1. Set ServerOwner to your SteamID64 before the first start to become owner."},
	}, "1502300", "Survive the Nights dedicated server.")
}

func survDragonwilds() Template {
	return survSteam(Template{
		ID: "ndl-dragonwilds", Name: "RuneScape: Dragonwilds", GameTitle: "RuneScape: Dragonwilds",
		Category: "survival", Engine: "unreal5",
		Summary: "RuneScape: Dragonwilds dedicated server. Co-op survival crafting RPG in the RuneScape world.",
		Startup: "CFG=RSDragonwilds/Saved/Config/LinuxServer; mkdir -p $CFG; [ -f $CFG/DedicatedServer.ini ] || printf '[/Script/Dominion.DedicatedServerSettings]\\nOwnerId=%s\\nServerName=%s\\nDefaultWorldName=%s\\nAdminPassword=%s\\nWorldPassword=%s\\n' \"$OWNER_ID\" \"{{SERVER_NAME}}\" \"{{WORLD_NAME}}\" \"$ADMIN_PASSWORD\" \"$WORLD_PASSWORD\" > $CFG/DedicatedServer.ini; exec ./RSDragonwilds/Binaries/Linux/RSDragonwildsServer-Linux-Shipping -log -port={{SERVER_PORT}} -ini:Game:[/Script/Engine.GameSession]:MaxPlayers={{MAX_PLAYERS}}",
		Stop:    "^C", Done: "CREATE SESSION - Advertise",
		StartRequires: []string{"OWNER_ID"},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		},
		DefaultMemoryMB: 8192, MinMemoryMB: 3072, DefaultDiskMB: 8192,
		Tags:    []string{"survival", "crafting", "coop", "rpg"},
		Aliases: []string{"dragonwilds", "runescape", "rs dragonwilds"},
		Variables: []Variable{
			envText("Owner ID", "OWNER_ID", "Your 32-character Dragonwilds player ID from the bottom of the in-game Settings menu. The server will not start without it.", "", false),
			envText("Server name", "SERVER_NAME", "Shown as Created By in the server browser (max 16 characters).", "NoDAL", true),
			envText("Default world name", "WORLD_NAME", "World created on first start. Players search for this exact name.", "No-DAL World", true),
			envNumber("Max players", "MAX_PLAYERS", "Slot count, 1 to 6.", "6", true),
			generatedSecret("Admin password", "ADMIN_PASSWORD", "Unlocks the Server Management tab in game. Generated when left empty."),
			envSecret("World password", "WORLD_PASSWORD", "Optional password players must enter to join.", false),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
		},
		Requirements: []Requirement{{Kind: ReqToken, Stage: StageStart, Env: "OWNER_ID", Label: "Your Dragonwilds player ID (Settings menu in game)", URL: "https://dragonwilds.runescape.com/news/how-to-dedicated-servers"}},
		Hint:         "Your Dragonwilds player ID required to start",
		ConfigFiles:  []ConfigFile{{Path: "RSDragonwilds/Saved/Config/LinuxServer/DedicatedServer.ini", Format: "ini", Restart: true}},
		SourceRef:    survPelican("runescape_dragonwilds"),
		DocsURL:      "https://dragonwilds.runescape.com/news/how-to-dedicated-servers",
		Notes:        []string{"DedicatedServer.ini is written on the first start from the variables and then left alone; edit it directly to change the owner, names or passwords later. RAM guidance is 2 GB plus 1 GB per player."},
	}, "4019830", "RuneScape: Dragonwilds dedicated server.")
}
