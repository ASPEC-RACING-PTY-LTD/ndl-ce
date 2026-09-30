package gameserver

// SteamCMD-installed, Linux-native dedicated servers for non-Source
// shooters, tactical and milsim games, melee, racing and driving, vehicle
// sims, sports and party games. Every app ID, executable, argument and
// default port below comes from the LinuxGSM config or the Pelican egg named
// in SourceRef.

const (
	moreLGSMCfg    = "https://github.com/GameServerManagers/LinuxGSM/blob/master/lgsm/config-default/config-lgsm/"
	morePelicanDir = "https://github.com/pelican-eggs/games-steamcmd/tree/main/"
)

func moreLGSMRef(name string) string { return moreLGSMCfg + name + "server/_default.cfg" }

// moreLib32 is the 32-bit runtime LinuxGSM lists for every server.
var moreLib32 = []string{"lib32gcc-s1", "lib32stdc++6"}

// moreSteamLib32Copy copies SteamCMD's 32-bit Steam client libraries next to an
// old Unreal 2 ucc-bin server, as LinuxGSM fix_kf.sh and fix_ro.sh do.
func moreSteamLib32Copy(dir string) string {
	return `for f in steamclient.so libtier0_s.so libvstdlib_s.so; do
  for d in "$HOME/.local/share/Steam/steamcmd/linux32" "$HOME/.steam/steamcmd/linux32" /root/.local/share/Steam/steamcmd/linux32; do
    if [ -f "$d/$f" ]; then cp -f "$d/$f" "/mnt/server/` + dir + `/$f"; break; fi
  done
done
`
}

func steamMoreTemplates() []Template {
	return []Template{
		// Tactical and milsim.
		moreSteam(Template{
			ID: "ndl-arma-reforger", Name: "Arma Reforger", GameTitle: "Arma Reforger",
			Summary:  "Arma Reforger dedicated server on the Enfusion engine. Settings live in config.json, which is written on first install.",
			Category: "tactical", Engine: "enfusion",
			Capabilities: []string{CapQueries},
			Startup:      "mkdir -p profile; exec ./ArmaReforgerServer -config ./config.json -profile ./profile -maxFPS {{MAX_FPS}}",
			Stop:         "^C", Done: "Starting RPL server",
			InstallScript: moreArmaReforgerConfig,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 2001, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "a2s", ContainerPort: 17777, Protocol: "udp", Fixed: true},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Written into config.json on first install. Edit config.json afterwards.", "No-DAL Arma Reforger", true),
				envNumber("Max players", "MAX_PLAYERS", "Written into config.json on first install.", "64", true),
				envText("Scenario ID", "SCENARIO_ID", "Scenario written into config.json on first install.", "{ECC61978EDCC2B5A}Missions/23_Campaign.conf", true),
				envNumber("Max FPS", "MAX_FPS", "Server frame cap. Uncapped servers can use several CPU cores.", "120", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "config.json", Format: "json", Restart: true}},
			Dependencies:    []string{"libcurl4t64"},
			DefaultMemoryMB: 6144, MinMemoryMB: 3328, DefaultDiskMB: 12288, DefaultCPUs: 4,
			Tags:      []string{"milsim", "coop", "pvp", "modded"},
			Aliases:   []string{"reforger", "arma reforger", "armar", "arma"},
			SourceRef: moreLGSMRef("armar"),
			Notes: []string{
				"publicAddress in config.json is left empty. If the server does not appear in the browser, set it to your public IP.",
				"Workshop mods are listed in the mods array of config.json and download on start. Game ownership is not needed to host.",
			},
		}, "1874900", "Arma Reforger dedicated server."),
		moreSteam(Template{
			ID: "ndl-squad", Name: "Squad", GameTitle: "Squad",
			Summary:  "Squad large-scale combined arms dedicated server.",
			Category: "tactical", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./SquadGameServer.sh -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -beaconport={{BEACON_PORT}} -log",
			Stop:         "^C", Done: "Engine Initialization", StopTimeout: 60,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7787, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "game+1", ContainerPort: 7788, Protocol: "udp"},
				{Name: "query", ContainerPort: 27165, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "query+1", ContainerPort: 27166, Protocol: "udp"},
				{Name: "beacon", ContainerPort: 15000, Protocol: "udp", Env: "BEACON_PORT"},
				{Name: "rcon", ContainerPort: 21114, Protocol: "tcp", Fixed: true},
			},
			Variables: []Variable{
				envNumber("Game port", "SERVER_PORT", "UDP game port. The next port up is also used.", "7787", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port. The next port up is also used.", "27165", true),
				envNumber("Beacon port", "BEACON_PORT", "LAN beacon port.", "15000", true),
			},
			ConfigFiles: []ConfigFile{
				{Path: "SquadGame/ServerConfig/Server.cfg", Format: "cfg", Restart: true},
				{Path: "SquadGame/ServerConfig/Rcon.cfg", Format: "cfg", Restart: true},
			},
			DefaultMemoryMB: 8192, DefaultDiskMB: 40960, DefaultCPUs: 4,
			Tags:      []string{"milsim", "pvp", "modded"},
			Aliases:   []string{"squad", "squadgame"},
			SourceRef: moreLGSMRef("squad"),
			Notes:     []string{"RCON is configured in SquadGame/ServerConfig/Rcon.cfg (port 21114 by default) and stays off until you set a password there."},
		}, "403240", "Squad dedicated server."),
		moreSteam(Template{
			ID: "ndl-squad44", Name: "Squad 44", GameTitle: "Squad 44",
			Summary:  "Squad 44 (formerly Post Scriptum) WWII dedicated server.",
			Category: "tactical", Engine: "unreal4",
			Capabilities: []string{CapQueries, CapRCON},
			Startup:      "exec ./PostScriptumServer.sh -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -RCONPORT={{RCON_PORT}} -RCONPASSWORD=\"$RCON_PASSWORD\" -log",
			Stop:         "^C", Done: "Engine Initialization", StopTimeout: 60,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 10027, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 10037, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "rcon", ContainerPort: 21114, Protocol: "tcp", Env: "RCON_PORT"},
			},
			Variables: []Variable{
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "10027", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "10037", true),
				envNumber("RCON port", "RCON_PORT", "TCP RCON port.", "21114", true),
				generatedSecret("RCON password", "RCON_PASSWORD", "RCON password. Generated when left empty."),
			},
			ConfigFiles:     []ConfigFile{{Path: "PostScriptum/ServerConfig/Server.cfg", Format: "cfg", Restart: true}},
			DefaultMemoryMB: 8192, DefaultDiskMB: 30720, DefaultCPUs: 4,
			Tags:      []string{"milsim", "pvp", "ww2"},
			Aliases:   []string{"squad 44", "squad44", "post scriptum", "postscriptum", "ps"},
			SourceRef: moreLGSMRef("squad44"),
		}, "746200", "Squad 44 (Post Scriptum) dedicated server."),
		moreSteam(Template{
			ID: "ndl-insurgency-sandstorm", Name: "Insurgency: Sandstorm", GameTitle: "Insurgency: Sandstorm",
			Summary:  "Insurgency: Sandstorm tactical shooter dedicated server with RCON.",
			Category: "tactical", Engine: "unreal4",
			Capabilities: []string{CapQueries, CapRCON},
			Startup:      "exec ./Insurgency/Binaries/Linux/InsurgencyServer-Linux-Shipping \"{{MAP}}?Scenario={{SCENARIO}}?MaxPlayers={{MAX_PLAYERS}}\" -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -hostname=\"{{SERVER_NAME}}\" -Rcon -RconPassword=\"$RCON_PASSWORD\" -RconListenPort={{RCON_PORT}} -GSLTToken=\"$STEAM_TOKEN\" -log",
			Stop:         "^C",
			Hint:         "GSLT only to list publicly",
			Requirements: []Requirement{gsltOptional("581320")},
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27102, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27131, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "rcon", ContainerPort: 27015, Protocol: "tcp", Env: "RCON_PORT"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Hostname in the server browser.", "No-DAL Sandstorm", true),
				envText("Map", "MAP", "Map loaded at boot.", "Oilfield", true),
				envText("Scenario", "SCENARIO", "Scenario for the start map.", "Scenario_Refinery_Push_Security", true),
				envNumber("Max players", "MAX_PLAYERS", "Slot count.", "28", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "27102", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27131", true),
				envNumber("RCON port", "RCON_PORT", "TCP RCON port.", "27015", true),
				generatedSecret("RCON password", "RCON_PASSWORD", "RCON password. Generated when left empty."),
				envSecret("GSLT token", "STEAM_TOKEN", "Steam Game Server Login Token for app 581320. Needed to list the server publicly.", false),
			},
			ConfigFiles:     []ConfigFile{{Path: "Insurgency/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 4096, DefaultDiskMB: 20480, DefaultCPUs: 2,
			Tags:      []string{"fps", "pvp", "coop"},
			Aliases:   []string{"sandstorm", "insurgency sandstorm", "inss", "ins2"},
			SourceRef: moreLGSMRef("inss"),
		}, "581330", "Insurgency: Sandstorm dedicated server."),
		moreSteam(Template{
			ID: "ndl-harsh-doorstop", Name: "Operation: Harsh Doorstop", GameTitle: "Operation: Harsh Doorstop",
			Summary:  "Operation: Harsh Doorstop milsim dedicated server with RCON.",
			Category: "tactical", Engine: "unreal4",
			Capabilities: []string{CapQueries, CapRCON},
			Startup:      "exec ./HarshDoorstop/Binaries/Linux/HarshDoorstopServer-Linux-Shipping HarshDoorstop \"{{MAP}}?MaxPlayers={{MAX_PLAYERS}}\" -SteamServerName=\"{{SERVER_NAME}}\" -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -RCONPort={{RCON_PORT}} -EnableRCON -RCONPassword=\"$RCON_PASSWORD\" -log",
			Stop:         "^C", Done: "RCON server listening on",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27005, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "rcon", ContainerPort: 7779, Protocol: "tcp", Env: "RCON_PORT"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Name in the server browser.", "No-DAL Harsh Doorstop", true),
				envText("Map", "MAP", "Map loaded at boot.", "AAS-TestMap", true),
				envNumber("Max players", "MAX_PLAYERS", "Slot count.", "32", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27005", true),
				envNumber("RCON port", "RCON_PORT", "TCP RCON port.", "7779", true),
				generatedSecret("RCON password", "RCON_PASSWORD", "RCON password. Generated when left empty."),
			},
			ConfigFiles:     []ConfigFile{{Path: "HarshDoorstop/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384, DefaultCPUs: 2,
			Tags:      []string{"milsim", "pvp", "free"},
			Aliases:   []string{"ohd", "harsh doorstop", "operation harsh doorstop"},
			SourceRef: morePelicanDir + "operation_harsh_doorstop",
		}, "950900", "Operation: Harsh Doorstop dedicated server."),

		// Shooters.
		moreSteam(Template{
			ID: "ndl-killing-floor-2", Name: "Killing Floor 2", GameTitle: "Killing Floor 2",
			Summary:  "Killing Floor 2 co-op survival shooter dedicated server.",
			Category: "shooter", Engine: "unreal3",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Binaries/Win64/KFGameSteamServer.bin.x86_64 \"{{MAP}}?Game=KFGameContent.KFGameInfo_Survival?Difficulty={{DIFFICULTY}}?AdminPassword=$ADMIN_PASSWORD?Port={{SERVER_PORT}}?QueryPort={{QUERY_PORT}}\"",
			Stop:         "^C",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "webadmin", ContainerPort: 8080, Protocol: "tcp", Fixed: true},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot.", "KF-BioticsLab", true),
				{Name: "Difficulty", Env: "DIFFICULTY", Description: "0 normal, 1 hard, 2 suicidal, 3 hell on earth.", Default: "0", Viewable: true, Editable: true, Required: true, FieldType: "select", Options: []string{"0", "1", "2", "3"}},
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
				generatedSecret("Admin password", "ADMIN_PASSWORD", "In-game and web admin password. Generated when left empty."),
			},
			ConfigFiles: []ConfigFile{
				{Path: "KFGame/Config/LinuxServer-KFGame.ini", Format: "ini", Restart: true},
				{Path: "KFGame/Config/KFWeb.ini", Format: "ini", Restart: true},
			},
			DefaultMemoryMB: 4096, DefaultDiskMB: 20480, DefaultCPUs: 2,
			Tags:      []string{"coop", "zombies", "pvp"},
			Aliases:   []string{"kf2", "killing floor 2", "killingfloor2"},
			SourceRef: moreLGSMRef("kf2"),
			Notes:     []string{"The INI files under KFGame/Config are created on the first start. The web admin on 8080 is off until enabled in KFWeb.ini."},
		}, "232130", "Killing Floor 2 dedicated server."),
		requireSteamOwner(moreSteam(Template{
			ID: "ndl-killing-floor", Name: "Killing Floor", GameTitle: "Killing Floor",
			Summary:  "Original Killing Floor co-op dedicated server (Unreal Engine 2, ucc-bin).",
			Category: "shooter", Engine: "unreal2",
			Capabilities:  []string{CapQueries},
			Startup:       "cd System && exec ./ucc-bin server \"{{MAP}}?game=KFmod.KFGameType?VACSecured=true\" -nohomedir ini=ndl.ini",
			Stop:          "^C",
			InstallScript: moreSteamLib32Copy("System") + "if [ ! -f /mnt/server/System/ndl.ini ] && [ -f /mnt/server/System/Default.ini ]; then cp /mnt/server/System/Default.ini /mnt/server/System/ndl.ini; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7707, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query-unreal", ContainerPort: 7708, Protocol: "udp", Fixed: true},
				{Name: "query-steam", ContainerPort: 28852, Protocol: "udp", Fixed: true},
				{Name: "webadmin", ContainerPort: 8075, Protocol: "tcp", Fixed: true},
				{Name: "steam", ContainerPort: 20610, Protocol: "udp", Fixed: true},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot, with the .rom extension.", "KF-BioticsLab.rom", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "System/ndl.ini", Format: "ini", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 1024, DefaultDiskMB: 8192, DefaultCPUs: 1,
			Tags:      []string{"coop", "zombies", "classic"},
			Aliases:   []string{"kf", "kf1", "killing floor"},
			SourceRef: moreLGSMRef("kf"),
			Notes:     []string{"Ports are set in System/ndl.ini ([URL] Port=7707). The Steam query port is 28852 for game port 7707."},
		}, "215360", "Killing Floor dedicated server."), "Steam account that owns Killing Floor"),
		requireSteamOwner(moreSteam(Template{
			ID: "ndl-red-orchestra", Name: "Red Orchestra: Ostfront 41-45", GameTitle: "Red Orchestra: Ostfront 41-45",
			Summary:  "Red Orchestra: Ostfront 41-45 WWII dedicated server (Unreal Engine 2, ucc-bin).",
			Category: "tactical", Engine: "unreal2",
			Capabilities:  []string{CapQueries},
			Startup:       "cd system && exec ./ucc-bin server \"{{MAP}}?game=ROGame.ROTeamGame?VACSecured=true\" -nohomedir ini=ndl.ini",
			Stop:          "^C",
			InstallScript: moreSteamLib32Copy("system") + "if [ ! -f /mnt/server/system/ndl.ini ] && [ -f /mnt/server/system/default.ini ]; then cp /mnt/server/system/default.ini /mnt/server/system/ndl.ini; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7757, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query-unreal", ContainerPort: 7758, Protocol: "udp", Fixed: true},
				{Name: "query-steam", ContainerPort: 28902, Protocol: "udp", Fixed: true},
				{Name: "webadmin", ContainerPort: 8075, Protocol: "tcp", Fixed: true},
				{Name: "steam", ContainerPort: 20610, Protocol: "udp", Fixed: true},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot, with the .rom extension.", "RO-Arad.rom", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "system/ndl.ini", Format: "ini", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 1024, DefaultDiskMB: 8192, DefaultCPUs: 1,
			Tags:      []string{"milsim", "ww2", "classic"},
			Aliases:   []string{"ro", "red orchestra", "ostfront", "roost"},
			SourceRef: moreLGSMRef("ro"),
			Notes:     []string{"Ports are set in system/ndl.ini ([URL] Port=7757)."},
		}, "223250", "Red Orchestra: Ostfront 41-45 dedicated server."), "Steam account that owns Red Orchestra: Ostfront 41-45"),
		moreSteam(Template{
			ID: "ndl-battalion-legacy", Name: "BATTALION: Legacy", GameTitle: "BATTALION: Legacy",
			Summary:  "BATTALION: Legacy WWII arena shooter dedicated server.",
			Category: "shooter", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Battalion/Binaries/Linux/BattalionServer-Linux-Shipping -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -log",
			Stop:         "^C",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 7780, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "rcon", ContainerPort: 7779, Protocol: "tcp"},
			},
			Variables: []Variable{
				envNumber("Game port", "SERVER_PORT", "UDP game port. RCON uses game port + 2.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "7780", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "Battalion/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 2048, DefaultDiskMB: 12288, DefaultCPUs: 2,
			Tags:      []string{"fps", "pvp", "ww2", "free"},
			Aliases:   []string{"battalion", "battalion 1944", "btl"},
			SourceRef: moreLGSMRef("btl"),
		}, "805140", "BATTALION: Legacy dedicated server."),
		moreSteam(Template{
			ID: "ndl-hypercharge", Name: "HYPERCHARGE: Unboxed", GameTitle: "HYPERCHARGE: Unboxed",
			Summary:  "HYPERCHARGE: Unboxed toy soldier shooter dedicated server.",
			Category: "shooter", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Unboxed/Binaries/Linux/UnboxedServer-Linux-Shipping \"{{MAP}}?MaxPlayers={{MAX_PLAYERS}}\" -ServerName=\"{{SERVER_NAME}}\" -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}}",
			Stop:         "^C",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Name in the server browser.", "No-DAL Hypercharge", true),
				envText("Map", "MAP", "Map loaded at boot.", "KidsBedroom", true),
				envNumber("Max players", "MAX_PLAYERS", "Slot count.", "8", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "Unboxed/Saved/Config/LinuxServer/GameUserSettings.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 2048, DefaultDiskMB: 12288, DefaultCPUs: 2,
			Tags:      []string{"fps", "coop", "pvp"},
			Aliases:   []string{"hypercharge", "unboxed", "hcu"},
			SourceRef: moreLGSMRef("hcu"),
		}, "1045940", "HYPERCHARGE: Unboxed dedicated server."),
		moreSteam(Template{
			ID: "ndl-stickybots", Name: "StickyBots", GameTitle: "StickyBots",
			Summary:  "StickyBots arena shooter dedicated server.",
			Category: "shooter", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./blank1/Binaries/Linux/blank1Server-Linux-Shipping -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -startup_map {{MAP}} -server_name \"{{SERVER_NAME}}\"",
			Stop:         "^C",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Name in the server browser.", "No-DAL StickyBots", true),
				envText("Map", "MAP", "Map loaded at boot.", "StationKappa", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
			},
			DefaultMemoryMB: 2048, DefaultDiskMB: 8192, DefaultCPUs: 2,
			Tags:      []string{"fps", "pvp"},
			Aliases:   []string{"stickybots", "sticky bots", "sbots"},
			SourceRef: moreLGSMRef("sbots"),
		}, "974130", "StickyBots dedicated server."),
		moreSteam(Template{
			ID: "ndl-ballistic-overkill", Name: "Ballistic Overkill", GameTitle: "Ballistic Overkill",
			Summary:  "Ballistic Overkill class-based shooter dedicated server (Unity).",
			Category: "shooter", Engine: "unity",
			Capabilities:  []string{CapQueries},
			Startup:       "export LD_LIBRARY_PATH=$HOME:$HOME/BODS_Data/Plugins/x86_64:$LD_LIBRARY_PATH; exec ./BODS.x86_64 -batchmode -nographics -dedicated -configfile=config.txt",
			Stop:          "^C",
			InstallScript: moreBallisticOverkillConfig,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Written into config.txt on first install.", "No-DAL Ballistic Overkill", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "config.txt", Format: "properties", Restart: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 1,
			Tags:      []string{"fps", "pvp"},
			Aliases:   []string{"ballistic overkill", "bo", "bods"},
			SourceRef: moreLGSMRef("bo"),
		}, "416880", "Ballistic Overkill dedicated server."),
		moreSteam(Template{
			ID: "ndl-quake-live", Name: "Quake Live", GameTitle: "Quake Live",
			Summary:  "Quake Live arena shooter dedicated server with ZeroMQ RCON.",
			Category: "shooter", Engine: "idtech3",
			Capabilities: []string{CapQueries, CapRCON},
			Startup:      "export LD_LIBRARY_PATH=$HOME/linux64:$LD_LIBRARY_PATH; exec ./qzeroded.x64 +set net_port {{SERVER_PORT}} +set sv_hostname \"{{SERVER_NAME}}\" +set sv_maxClients {{MAX_PLAYERS}} +set zmq_rcon_enable 1 +set zmq_rcon_port {{RCON_PORT}} +set zmq_rcon_password \"$RCON_PASSWORD\" +set zmq_stats_enable 0",
			Stop:         "quit", Done: "zmq stats and rcon passwords updated",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27960, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "rcon", ContainerPort: 28960, Protocol: "tcp", Env: "RCON_PORT"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Hostname in the server browser.", "No-DAL Quake Live", true),
				envNumber("Max players", "MAX_PLAYERS", "sv_maxClients.", "16", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port (net_port). Steam query uses the same port.", "27960", true),
				envNumber("RCON port", "RCON_PORT", "ZeroMQ RCON TCP port.", "28960", true),
				generatedSecret("RCON password", "RCON_PASSWORD", "ZeroMQ RCON password. Generated when left empty."),
			},
			DefaultMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 1,
			Tags:      []string{"fps", "pvp", "arena", "classic"},
			Aliases:   []string{"quake live", "ql", "quakelive", "quake"},
			SourceRef: moreLGSMRef("ql"),
			Notes:     []string{"Other settings go in baseq3/server.cfg; load it by adding +exec server.cfg to the startup line."},
		}, "349090", "Quake Live dedicated server."),
		moreSteam(Template{
			ID: "ndl-warfork", Name: "Warfork", GameTitle: "Warfork",
			Summary:  "Warfork arena shooter dedicated server installed from Steam.",
			Category: "shooter", Engine: "idtech2",
			Startup: "exec ./wf_server.x86_64 +set sv_port {{SERVER_PORT}} +set sv_http_port {{HTTP_PORT}}",
			Stop:    "quit",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 44400, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "http", ContainerPort: 44444, Protocol: "tcp", Env: "HTTP_PORT"},
			},
			Variables: []Variable{
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "44400", true),
				envNumber("HTTP port", "HTTP_PORT", "Built-in HTTP server for map and asset downloads.", "44444", true),
			},
			DefaultMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 1,
			Tags:      []string{"fps", "pvp", "arena", "free"},
			Aliases:   []string{"warfork", "wf", "warsow"},
			SourceRef: moreLGSMRef("wf"),
			Notes:     []string{"Server settings can be put in basewf/server.cfg and loaded with +exec server.cfg."},
		}, "1136510", "Warfork dedicated server."),
		moreSteam(Template{
			ID: "ndl-soldat", Name: "Soldat (Steam)", GameTitle: "Soldat",
			Summary:  "Soldat 2D side-scrolling shooter dedicated server from the Steam release.",
			Category: "shooter", Engine: "custom",
			Startup:       "exec ./soldatserver",
			Stop:          "^C",
			InstallScript: "chmod +x /mnt/server/soldatserver 2>/dev/null || true\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 23073, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "files", ContainerPort: 23083, Protocol: "tcp", Fixed: true},
				{Name: "query", ContainerPort: 23083, Protocol: "udp", Fixed: true},
			},
			ConfigFiles:     []ConfigFile{{Path: "soldat.ini", Format: "ini", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 512, DefaultDiskMB: 1024, DefaultCPUs: 1,
			Tags:      []string{"2d", "pvp", "classic"},
			Aliases:   []string{"soldat"},
			SourceRef: moreLGSMRef("sol"),
			Notes:     []string{"The game port is [NETWORK] Port in soldat.ini. The file transfer and query port is game port + 10."},
		}, "638500", "Soldat dedicated server."),
		moreNS2Template(false),
		moreNS2Template(true),
		moreSteam(Template{
			ID: "ndl-pavlov-vr", Name: "Pavlov VR", GameTitle: "Pavlov VR",
			Summary:  "Pavlov VR shooter dedicated server (Linux-only server build) with RCON.",
			Category: "shooter", Engine: "unreal4",
			Capabilities: []string{CapRCON},
			Startup:      "if [ ! -f linux64/libc++.so ] && [ -f /usr/lib/x86_64-linux-gnu/libc++.so.1 ]; then mkdir -p linux64 && cp /usr/lib/x86_64-linux-gnu/libc++.so.1 linux64/libc++.so; fi; export LD_LIBRARY_PATH=$HOME:$HOME/linux64:$LD_LIBRARY_PATH; exec ./Pavlov/Binaries/Linux/PavlovServer-Linux-Shipping Pavlov {{MAP}} -Port={{SERVER_PORT}} ApiKey=\"$API_KEY\" -log",
			Stop:         "^C", Done: "Starting Server Status Helper on Port",
			InstallScript: morePavlovConfig,
			Hint:          "API key only to list publicly",
			Requirements: []Requirement{{
				Kind: ReqAPIKey, Stage: StageOptional, Env: "API_KEY",
				Label: "Pavlov master server ApiKey from vankrupt, needed for the server to show in the public list",
				URL:   "https://pavlov-ms.vankrupt.com/servers/v1/key",
			}},
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "game-tcp", ContainerPort: 7777, Protocol: "tcp", Env: "SERVER_PORT"},
				{Name: "game+400", ContainerPort: 8177, Protocol: "udp"},
				{Name: "rcon", ContainerPort: 8188, Protocol: "tcp", Fixed: true},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot. The rotation is set in Game.ini.", "datacenter", true),
				envNumber("Game port", "SERVER_PORT", "Game port. Port + 400 (UDP) is also used.", "7777", true),
				envText("Server name", "SERVER_NAME", "Written into Game.ini on first install.", "No-DAL Pavlov", true),
				envSecret("API key", "API_KEY", "Pavlov master server key. Only needed to appear in the public server list.", false),
				generatedSecret("RCON password", "RCON_PASSWORD", "Written into Pavlov/Saved/Config/RconSettings.txt on first install. Generated when left empty."),
			},
			ConfigFiles: []ConfigFile{
				{Path: "Pavlov/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true},
				{Path: "Pavlov/Saved/Config/RconSettings.txt", Format: "properties", Restart: true},
			},
			Dependencies:    []string{"libc++1"},
			DefaultMemoryMB: 2048, DefaultDiskMB: 8192, DefaultCPUs: 2,
			Tags:      []string{"vr", "fps", "pvp"},
			Aliases:   []string{"pavlov", "pavlov vr", "pvr"},
			SourceRef: moreLGSMRef("pvr"),
			Notes:     []string{"Pavlov is single threaded; about 2 GB RAM and one fast core serve roughly 24 players. Workshop maps download to /tmp at runtime and are not kept across restarts."},
		}, "622970", "Pavlov VR dedicated server."),
		moreSteam(Template{
			ID: "ndl-banana-shooter", Name: "Banana Shooter", GameTitle: "Banana Shooter",
			Summary:  "Banana Shooter party shooter dedicated server (Unity).",
			Category: "party", Engine: "unity",
			Startup: "export LD_LIBRARY_PATH=$HOME/linux64:$LD_LIBRARY_PATH; export TERM=dumb; exec ./BSDS.x86_64 -batchmode -nographics -logFile - +Server \"{{SERVER_NAME}}\" +maxplayercount {{MAX_PLAYERS}} +port {{SERVER_PORT}}",
			Stop:    "quit",
			Hint:    "GSLT only to list publicly",
			Requirements: []Requirement{{
				Kind: ReqGSLT, Stage: StageOptional,
				Label: "Steam Game Server Login Token for app 1949740, set as Login_Token in Servers/<name>/Config.json to appear in the community list",
				URL:   "https://steamcommunity.com/dev/managegameservers",
			}},
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27017, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27018, Protocol: "udp"},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Server name. Also the Servers/<name> config folder.", "NoDAL", true),
				envNumber("Max players", "MAX_PLAYERS", "Slot count.", "40", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port. Steam query uses the next port.", "27017", true),
			},
			DefaultMemoryMB: 2048, DefaultDiskMB: 4096, DefaultCPUs: 2,
			Tags:      []string{"fps", "pvp"},
			Aliases:   []string{"banana shooter", "bananashooter", "bsds"},
			SourceRef: morePelicanDir + "banana_shooter",
		}, "2406780", "Banana Shooter dedicated server."),

		// Melee.
		moreSteam(Template{
			ID: "ndl-mordhau", Name: "MORDHAU", GameTitle: "MORDHAU",
			Summary:  "MORDHAU medieval melee dedicated server (native Linux build).",
			Category: "shooter", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Mordhau/Binaries/Linux/MordhauServer-Linux-Shipping Mordhau {{MAP}} -Port={{SERVER_PORT}} -BeaconPort={{BEACON_PORT}} -QueryPort={{QUERY_PORT}} -log",
			Stop:         "^C", Done: "Session GameSession successfully created",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
				{Name: "beacon", ContainerPort: 15000, Protocol: "udp", Env: "BEACON_PORT"},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot.", "FFA_ThePit", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
				envNumber("Beacon port", "BEACON_PORT", "Beacon port.", "15000", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "Mordhau/Saved/Config/LinuxServer/Game.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384, DefaultCPUs: 2,
			Tags:      []string{"melee", "medieval", "pvp"},
			Aliases:   []string{"mordhau", "mh"},
			SourceRef: moreLGSMRef("mh"),
			DocsURL:   "https://mordhaucommunity.org/docs/en/dedicated-server-guide/",
			Notes:     []string{"Game.ini (admin password, map rotation, server name) is created on the first start. Some community maps are built for the Windows server only."},
		}, "629800", "MORDHAU dedicated server."),
		moreSteam(Template{
			ID: "ndl-chivalry-mw", Name: "Chivalry: Medieval Warfare", GameTitle: "Chivalry: Medieval Warfare",
			Summary:  "Chivalry: Medieval Warfare dedicated server (UDK).",
			Category: "shooter", Engine: "unreal3",
			Capabilities:  []string{CapQueries},
			Startup:       "cd Binaries/Linux && exec ./UDKGameServer-Linux \"{{MAP}}?steamsockets\" -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -seekfreeloadingserver",
			Stop:          "^C",
			InstallScript: "if [ -d /mnt/server/Binaries/Linux ] && [ ! -f /mnt/server/Binaries/Linux/steam_appid.txt ]; then echo 219640 > /mnt/server/Binaries/Linux/steam_appid.txt; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 7779, Protocol: "udp", Env: "QUERY_PORT"},
			},
			Variables: []Variable{
				envText("Map", "MAP", "Map loaded at boot.", "AOCTD-Frigid_p", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "7779", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "UDKGame/Config/PCServer-UDKGame.ini", Format: "ini", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 2048, DefaultDiskMB: 10240, DefaultCPUs: 2,
			Tags:      []string{"melee", "medieval", "pvp", "classic"},
			Aliases:   []string{"chivalry", "cmw", "chivalry medieval warfare"},
			SourceRef: moreLGSMRef("cmw"),
		}, "220070", "Chivalry: Medieval Warfare dedicated server."),

		// Racing and driving.
		requireSteamOwner(moreSteam(Template{
			ID: "ndl-assetto-corsa", Name: "Assetto Corsa", GameTitle: "Assetto Corsa",
			Summary:  "Assetto Corsa racing dedicated server (acServer).",
			Category: "racing", Engine: "custom",
			Startup: "exec ./acServer",
			Stop:    "^C",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 9600, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "game-tcp", ContainerPort: 9600, Protocol: "tcp", Fixed: true},
				{Name: "http", ContainerPort: 8081, Protocol: "tcp", Fixed: true},
			},
			ConfigFiles: []ConfigFile{
				{Path: "cfg/server_cfg.ini", Format: "ini", Restart: true},
				{Path: "cfg/entry_list.ini", Format: "ini", Restart: true},
			},
			DefaultMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 1,
			Tags:      []string{"racing", "sim"},
			Aliases:   []string{"assetto corsa", "ac", "acserver", "assetto"},
			SourceRef: moreLGSMRef("ac"),
			Notes:     []string{"Ports, cars and track are set in cfg/server_cfg.ini (UDP_PORT, TCP_PORT, HTTP_PORT) and cfg/entry_list.ini."},
		}, "302550", "Assetto Corsa dedicated server."), "Steam account that owns Assetto Corsa"),
		moreSteam(Template{
			ID: "ndl-project-cars", Name: "Project CARS", GameTitle: "Project CARS",
			Summary:  "Project CARS racing dedicated server.",
			Category: "racing", Engine: "custom",
			Startup:       "exec ./DedicatedServerCmd --config server.cfg",
			Stop:          "^C",
			InstallScript: "if [ ! -f /mnt/server/server.cfg ] && [ -f /mnt/server/config_sample/server.cfg ]; then cp /mnt/server/config_sample/server.cfg /mnt/server/server.cfg; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
				{Name: "steam", ContainerPort: 8766, Protocol: "udp", Fixed: true},
			},
			ConfigFiles:     []ConfigFile{{Path: "server.cfg", Format: "cfg", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:      []string{"racing", "sim"},
			Aliases:   []string{"project cars", "pcars", "pc1"},
			SourceRef: moreLGSMRef("pc"),
			Notes: []string{
				"Ports are hostPort, queryPort and steamPort in server.cfg. If config_sample is missing, create server.cfg from the Project CARS dedicated server guide.",
				"Project CARS is no longer sold on Steam; only existing owners can join.",
			},
		}, "332670", "Project CARS dedicated server."),
		requireSteamOwner(moreSteam(Template{
			ID: "ndl-project-cars-2", Name: "Project CARS 2", GameTitle: "Project CARS 2",
			Summary:  "Project CARS 2 racing dedicated server.",
			Category: "racing", Engine: "custom",
			Startup:       "exec ./DedicatedServerCmd.elf --config server.cfg",
			Stop:          "^C",
			InstallScript: "if [ ! -f /mnt/server/server.cfg ] && [ -f /mnt/server/config_sample/server.cfg ]; then cp /mnt/server/config_sample/server.cfg /mnt/server/server.cfg; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
				{Name: "steam", ContainerPort: 8766, Protocol: "udp", Fixed: true},
			},
			ConfigFiles:     []ConfigFile{{Path: "server.cfg", Format: "cfg", Restart: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:      []string{"racing", "sim"},
			Aliases:   []string{"project cars 2", "pcars2", "pc2"},
			SourceRef: moreLGSMRef("pc2"),
			Notes:     []string{"server.cfg is copied from config_sample/server.cfg on install. Project CARS 2 is no longer sold on Steam; the download needs an account that already owns it."},
		}, "413770", "Project CARS 2 dedicated server."), "Steam account that owns Project CARS 2"),

		// Vehicle sims.
		moreTruckSimTemplate("ndl-ets2", "Euro Truck Simulator 2", "1948160", "227300", "eurotrucks2_server", "ets2", []string{"ets2", "euro truck", "euro truck simulator", "eurotruck"}),
		moreTruckSimTemplate("ndl-ats", "American Truck Simulator", "2239530", "270880", "amtrucks_server", "ats", []string{"ats", "american truck", "american truck simulator"}),
		moreSteam(Template{
			ID: "ndl-the-bus", Name: "The Bus", GameTitle: "The Bus",
			Summary:  "The Bus city bus simulator dedicated server.",
			Category: "simulation", Engine: "unreal5",
			Startup: "exec ./TheBus/Binaries/Linux/TheBusServer_Linux -log -useperfthreads -port={{SERVER_PORT}}",
			Stop:    "^C", Done: "Engine Initialization",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7778, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
			},
			Variables: []Variable{
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "7778", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "TheBus/Settings/ServerSettings.cfg", Format: "json", Restart: true}},
			DefaultMemoryMB: 4096, DefaultDiskMB: 20480, DefaultCPUs: 2,
			Tags:      []string{"sim", "coop", "driving"},
			Aliases:   []string{"the bus", "thebus", "bus simulator"},
			SourceRef: morePelicanDir + "the_bus",
			Notes:     []string{"Server name, passwords, player count and public listing are set in TheBus/Settings/ServerSettings.cfg."},
		}, "507320", "The Bus dedicated server."),

		// Sandbox multiplayer mods.
		moreSteam(Template{
			ID: "ndl-jc2mp", Name: "Just Cause 2: Multiplayer", GameTitle: "Just Cause 2",
			Summary:  "Just Cause 2: Multiplayer (JC2-MP) server with Lua scripting.",
			Category: "sandbox", Engine: "custom",
			Startup:       "exec ./Jcmp-Server",
			Stop:          "quit",
			InstallScript: "if [ ! -f /mnt/server/config.lua ] && [ -f /mnt/server/default_config.lua ]; then cp /mnt/server/default_config.lua /mnt/server/config.lua; fi\n",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Fixed: true},
			},
			ConfigFiles:     []ConfigFile{{Path: "config.lua", Format: "lua", Restart: true}},
			Dependencies:    moreLib32,
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:      []string{"modded", "pvp", "open-world"},
			Aliases:   []string{"jc2", "jc2mp", "jc2-mp", "just cause 2"},
			SourceRef: moreLGSMRef("jc2"),
			Notes:     []string{"Players need Just Cause 2 and the free JC2-MP client. BindPort, name and password are set in config.lua; query uses the game port."},
		}, "261140", "Just Cause 2: Multiplayer dedicated server."),
		moreSteam(Template{
			ID: "ndl-jc3mp", Name: "Just Cause 3: Multiplayer", GameTitle: "Just Cause 3",
			Summary:  "Just Cause 3: Multiplayer (JC3MP) server.",
			Category: "sandbox", Engine: "custom",
			Startup:       "exec ./Server",
			Stop:          "^C",
			InstallScript: moreJC3MPConfig,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 4200, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query", ContainerPort: 4201, Protocol: "udp", Fixed: true},
				{Name: "steam", ContainerPort: 4202, Protocol: "udp", Fixed: true},
				{Name: "http", ContainerPort: 4203, Protocol: "tcp", Fixed: true},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Written into config.json on first install.", "No-DAL JC3MP", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "config.json", Format: "json", Restart: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:      []string{"modded", "pvp", "open-world"},
			Aliases:   []string{"jc3", "jc3mp", "just cause 3"},
			SourceRef: moreLGSMRef("jc3"),
			Notes:     []string{"Players need Just Cause 3 and the JC3MP client. Set announce to true in config.json to list the server publicly."},
		}, "619960", "Just Cause 3: Multiplayer dedicated server."),

		// Party and social.
		moreSteam(Template{
			ID: "ndl-tower-unite", Name: "Tower Unite (Condo)", GameTitle: "Tower Unite",
			Summary:  "Tower Unite dedicated Condo server.",
			Category: "party", Engine: "unreal4",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Tower/Binaries/Linux/TowerServer-Linux-Shipping -Port={{SERVER_PORT}} -QueryPort={{QUERY_PORT}} -TowerServerINI=TowerServer.ini -log",
			Stop:         "^C", Done: "listening on port",
			InstallScript: moreTowerUniteConfig,
			Hint:          "Tokens only to list publicly",
			Requirements: []Requirement{
				{Kind: ReqGSLT, Stage: StageOptional, Label: "Steam Game Server Login Token for app 394690 (SteamLoginToken in TowerServer.ini) for a static Steam ID", URL: "https://steamcommunity.com/dev/managegameservers"},
				{Kind: ReqToken, Stage: StageOptional, Label: "Tower Unite server token (TowerLoginToken in TowerServer.ini) to appear in the Condo server list", URL: "https://moderation.towerunite.com/manage_game_servers.php"},
			},
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "steam", ContainerPort: 7778, Protocol: "udp"},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Env: "QUERY_PORT"},
			},
			Variables: []Variable{
				envText("Server title", "SERVER_NAME", "Written into TowerServer.ini on first install.", "No-DAL Condo", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port. The next port up is also used.", "7777", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27015", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "Tower/Binaries/Linux/TowerServer.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 2048, DefaultDiskMB: 8192, DefaultCPUs: 2,
			Tags:      []string{"social", "coop", "building"},
			Aliases:   []string{"tower unite", "towerunite", "condo", "tu"},
			SourceRef: moreLGSMRef("tu"),
			DocsURL:   "https://towerunite.com/guides/condo_dedicated_linux.html",
		}, "439660", "Tower Unite dedicated server."),
		moreSteam(Template{
			ID: "ndl-puck", Name: "Puck", GameTitle: "Puck",
			Summary:  "Puck physics-based hockey dedicated server (Unity).",
			Category: "sports", Engine: "unity",
			Startup: "exec ./Puck.x86_64",
			Stop:    "^C", Done: "UDP socket started on port",
			InstallScript: morePuckConfig,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "ping", ContainerPort: 7778, Protocol: "udp", Fixed: true},
			},
			Variables: []Variable{
				envText("Server name", "SERVER_NAME", "Written into server_configuration.json on first install.", "No-DAL Puck", true),
				envNumber("Max players", "MAX_PLAYERS", "Written into server_configuration.json on first install.", "12", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "server_configuration.json", Format: "json", Restart: true}},
			DefaultMemoryMB: 2048, DefaultDiskMB: 4096, DefaultCPUs: 2,
			Tags:      []string{"hockey", "pvp", "physics"},
			Aliases:   []string{"puck", "hockey"},
			SourceRef: morePelicanDir + "puck",
			Notes:     []string{"port and pingPort in server_configuration.json set the game and browser ping ports."},
		}, "3481440", "Puck dedicated server."),

		// RPG, sandbox and roleplay titles assigned to this catalogue file.
		moreSteam(Template{
			ID: "ndl-citadel", Name: "Citadel: Forged with Fire", GameTitle: "Citadel: Forged with Fire",
			Summary:  "Citadel: Forged with Fire magic sandbox RPG dedicated server.",
			Category: "rpg", Engine: "unreal4",
			Startup: "exec ./CitadelServer.sh",
			Stop:    "^C", Done: "Steam Server initialized",
			InstallScript: moreCitadelSetup,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Fixed: true},
				{Name: "query", ContainerPort: 27015, Protocol: "udp", Fixed: true},
			},
			ConfigFiles:     []ConfigFile{{Path: "Config/Game.ini", Format: "ini", Restart: true}},
			DefaultMemoryMB: 4096, DefaultDiskMB: 12288, DefaultCPUs: 2,
			Tags:      []string{"magic", "building", "pvp"},
			Aliases:   []string{"citadel", "forged with fire"},
			SourceRef: morePelicanDir + "citadel",
			Notes:     []string{"Config/Game.ini ([UWorks] ConnectionPort and QueryPort, WorldCreationSettings) is created on install and linked into Citadel/Saved/Config/LinuxServer."},
		}, "489650", "Citadel: Forged with Fire dedicated server."),
		moreSteam(Template{
			ID: "ndl-qanga", Name: "QANGA", GameTitle: "QANGA",
			Summary:  "QANGA space sandbox dedicated server.",
			Category: "sandbox", Engine: "unreal5",
			Capabilities: []string{CapQueries},
			Startup:      "exec ./Qanga/Binaries/Linux/QangaServer-Linux-Shipping Qanga /Game/Maps/Universe/{{MAP}}? -server -log -port={{SERVER_PORT}} -map={{MAP}} -sessionName=\"{{SERVER_NAME}}\" -QueryPort={{QUERY_PORT}}",
			Stop:         "^C", Done: "Steam Sockets Adress",
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 10000, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
				{Name: "query", ContainerPort: 27016, Protocol: "udp", Env: "QUERY_PORT"},
			},
			Variables: []Variable{
				envText("Session name", "SERVER_NAME", "Name in the server browser.", "Survival", true),
				envText("Map", "MAP", "Universe map.", "L_Persistent_Universe", true),
				envNumber("Game port", "SERVER_PORT", "UDP game port.", "10000", true),
				envNumber("Query port", "QUERY_PORT", "Steam query port.", "27016", true),
			},
			DefaultMemoryMB: 4096, DefaultDiskMB: 12288, DefaultCPUs: 2,
			Tags:      []string{"space", "building", "coop"},
			Aliases:   []string{"qanga"},
			SourceRef: morePelicanDir + "qanga",
		}, "1652070", "QANGA dedicated server."),
		moreSteam(Template{
			ID: "ndl-novalife-amboise", Name: "Nova-Life: Amboise", GameTitle: "Nova-Life: Amboise",
			Summary:  "Nova-Life: Amboise roleplay simulation dedicated server (Unity).",
			Category: "roleplay", Engine: "unity",
			Startup: "exec ./nova-life.x86_64 -batchmode -nographics -startServer \"server\"",
			Stop:    "^C", Done: "Server launched in ",
			InstallScript: moreNovaLifeConfig,
			DefaultPorts: []Port{
				{Name: "game", ContainerPort: 7777, Protocol: "udp", Primary: true, Fixed: true},
			},
			Variables: []Variable{
				envText("Server list name", "SERVER_NAME", "Name shown in the server list. Written into Servers/server/Config/server.json on first install.", "No-DAL Nova-Life", true),
				envNumber("Slots", "MAX_PLAYERS", "Player slots. Written into server.json on first install.", "25", true),
			},
			ConfigFiles:     []ConfigFile{{Path: "Servers/server/Config/server.json", Format: "json", Restart: true}},
			DefaultMemoryMB: 8192, DefaultDiskMB: 8192, DefaultCPUs: 3,
			Tags:      []string{"roleplay", "life-sim"},
			Aliases:   []string{"nova life", "novalife", "amboise"},
			SourceRef: morePelicanDir + "novalife_amboise",
			Notes:     []string{"The game is in development and the console prints many harmless errors. serverPort in server.json sets the game port."},
		}, "1665030", "Nova-Life: Amboise dedicated server."),
	}
}

// moreTruckSimTemplate builds the SCS Software convoy dedicated servers, which
// share a layout (LinuxGSM ets2server and atsserver).
func moreTruckSimTemplate(id, title, appID, gameAppID, binary, lgsm string, aliases []string) Template {
	cfgDir := ".local/share/" + title
	return moreSteam(Template{
		ID: id, Name: title, GameTitle: title,
		Summary:  title + " convoy dedicated server.",
		Category: "simulation", Engine: "custom",
		Startup:       "cd bin/linux_x64 && exec ./" + binary + " -nosingle",
		Stop:          "^C",
		InstallScript: moreTruckSimConfigScript(title),
		Hint:          "Server token only for a persistent ID",
		Requirements: []Requirement{{
			Kind: ReqGSLT, Stage: StageOptional,
			Label: "Steam Game Server Login Token for app " + gameAppID + " (server_logon_token in server_config.sii) for a persistent server ID. Needs an account that owns the game.",
			URL:   "https://steamcommunity.com/dev/managegameservers",
		}},
		DefaultPorts: []Port{
			{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Fixed: true},
			{Name: "query", ContainerPort: 27016, Protocol: "udp", Fixed: true},
		},
		Variables: []Variable{
			envText("Lobby name", "SERVER_NAME", "Written into server_config.sii on first install.", "No-DAL "+title, true),
		},
		ConfigFiles:     []ConfigFile{{Path: cfgDir + "/server_config.sii", Format: "sii", Restart: true}},
		DefaultMemoryMB: 2048, DefaultDiskMB: 8192, DefaultCPUs: 2,
		Tags:      []string{"driving", "coop", "convoy"},
		Aliases:   aliases,
		SourceRef: moreLGSMRef(lgsm),
		Notes: []string{
			"Without a server_logon_token the server gets a new ID on every start; players find it by the session search ID printed in the console.",
			"Mods and DLC need server_packages.sii and server_packages.dat exported from a game client (export_server_packages) and uploaded to " + cfgDir + ".",
		},
	}, appID, title+" dedicated server.")
}

func moreTruckSimConfigScript(title string) string {
	dir := "/mnt/server/.local/share/" + title
	return `mkdir -p "` + dir + `"
if [ ! -f "` + dir + `/server_config.sii" ]; then
  printf 'SiiNunit\n{\nserver_config : Server {\n lobby_name: "%s"\n description: ""\n welcome_message: ""\n password: ""\n max_players: 8\n max_vehicles_total: 100\n max_ai_vehicles_player: 50\n max_ai_vehicles_player_spawn: 30\n connection_virtual_port: 100\n query_virtual_port: 101\n connection_dedicated_port: 27015\n query_dedicated_port: 27016\n server_logon_token: ""\n player_damage: true\n traffic: true\n hide_in_company: false\n hide_colliding: true\n force_speed_limiter: false\n mods_optioning: false\n timezones: 0\n service_no_collision: false\n in_menu_ghosting: false\n name_tags: true\n friends_only: false\n show_server: true\n moderator_list: 0\n}\n\n}\n' "${SERVER_NAME:-No-DAL server}" > "` + dir + `/server_config.sii"
fi
`
}

// Arma Reforger config from the LinuxGSM default (server.json), with the
// RCON block left out so no placeholder password ships.
const moreArmaReforgerConfig = `
mkdir -p /mnt/server/profile
if [ ! -f /mnt/server/config.json ]; then
  printf '{\n  "bindAddress": "0.0.0.0",\n  "bindPort": 2001,\n  "publicAddress": "",\n  "publicPort": 2001,\n  "a2s": {\n    "address": "0.0.0.0",\n    "port": 17777\n  },\n  "game": {\n    "name": "%s",\n    "password": "",\n    "passwordAdmin": "",\n    "admins": [],\n    "scenarioId": "%s",\n    "maxPlayers": %s,\n    "visible": true,\n    "gameProperties": {\n      "serverMaxViewDistance": 2500,\n      "serverMinGrassDistance": 100,\n      "networkViewDistance": 1500,\n      "disableThirdPerson": false,\n      "fastValidation": true,\n      "battlEye": true,\n      "VONDisableUI": false,\n      "VONDisableDirectSpeechUI": false,\n      "VONCanTransmitCrossFaction": false\n    },\n    "mods": []\n  },\n  "operating": {\n    "lobbyPlayerSynchronise": true\n  }\n}\n' "${SERVER_NAME:-No-DAL Arma Reforger}" "$SCENARIO_ID" "${MAX_PLAYERS:-64}" > /mnt/server/config.json
fi
`

// Ballistic Overkill config.txt from the LinuxGSM default.
const moreBallisticOverkillConfig = `
if [ ! -f /mnt/server/config.txt ]; then
  printf 'ServerName=%s\nServerPort=27015\nQueryPort=27016\nMaxPlayers=12\nMaxSpectators=4\nMaxPing=0\nGameMap=3\nGameMode=4\nMatchTime=10\nAutobalance=1\nRounds=7\nRoundTime=90\nWarmUpTime=26\nPassword=\nDedicatedBroadcast=1\nBannerURL=\nClickURL=\nOwnerId=\n' "${SERVER_NAME:-No-DAL Ballistic Overkill}" > /mnt/server/config.txt
fi
`

// JC3MP config.json from the LinuxGSM default.
const moreJC3MPConfig = `
if [ ! -f /mnt/server/config.json ]; then
  printf '{\n  "announce": false,\n  "description": "",\n  "host": "0.0.0.0",\n  "httpPort": 4203,\n  "logLevel": 7,\n  "logo": "",\n  "maxPlayers": 32,\n  "maxTickRate": 60,\n  "name": "%s",\n  "password": "",\n  "port": 4200,\n  "queryPort": 4201,\n  "requiredDLC": [],\n  "steamPort": 4202\n}\n' "${SERVER_NAME:-No-DAL JC3MP}" > /mnt/server/config.json
fi
`

// Tower Unite TowerServer.ini from the LinuxGSM default, plus the Steam
// client library the Pelican egg copies next to the server binary.
const moreTowerUniteConfig = `
if [ -d /mnt/server/Tower/Binaries/Linux ]; then
  if [ ! -f /mnt/server/Tower/Binaries/Linux/TowerServer.ini ]; then
    printf '[/Script/TowerNetworking.DedicatedServerOptions]\nServerTitle=%s\nMaxPlayers=32\nSteamLoginToken=\nTowerLoginToken=\n\n[Administration]\nAdminSteamID=\n\n[DedicatedCondoOptions]\nCondoAutoSave=True\nCondoBackupsEnabled=True\n' "${SERVER_NAME:-No-DAL Condo}" > /mnt/server/Tower/Binaries/Linux/TowerServer.ini
  fi
  if [ -f /mnt/server/linux64/steamclient.so ]; then
    cp -f /mnt/server/linux64/steamclient.so /mnt/server/Tower/Binaries/Linux/steamclient.so
  elif [ -f /mnt/server/.steam/sdk64/steamclient.so ]; then
    cp -f /mnt/server/.steam/sdk64/steamclient.so /mnt/server/Tower/Binaries/Linux/steamclient.so
  fi
fi
`

// Puck server_configuration.json from the Pelican egg.
const morePuckConfig = `
if [ ! -f /mnt/server/server_configuration.json ]; then
  printf '{\n  "port": 7777,\n  "pingPort": 7778,\n  "name": "%s",\n  "maxPlayers": %s,\n  "password": "",\n  "voip": false,\n  "isPublic": true,\n  "adminSteamIds": [],\n  "reloadBannedSteamIds": false,\n  "usePuckBannedSteamIds": true,\n  "printMetrics": true,\n  "kickTimeout": 300,\n  "targetFrameRate": 120,\n  "tickRate": 100,\n  "phaseDurationMap": {\n    "Warmup": 600,\n    "FaceOff": 3,\n    "Playing": 300,\n    "BlueScore": 5,\n    "RedScore": 5,\n    "Replay": 10,\n    "PeriodOver": 15,\n    "GameOver": 15\n  }\n}\n' "${SERVER_NAME:-No-DAL Puck}" "${MAX_PLAYERS:-12}" > /mnt/server/server_configuration.json
fi
`

// Citadel layout from the Pelican egg: the UWorks Steam library link and a
// Config folder shared by Citadel and Engine saved configs.
const moreCitadelSetup = `
LIBDIR=/mnt/server/Citadel/Plugins/UWorks/Source/ThirdParty/Linux
mkdir -p "$LIBDIR"
if [ ! -f "$LIBDIR/steamclient.so" ] && [ -f /mnt/server/.steam/sdk64/steamclient.so ]; then
  cp -f /mnt/server/.steam/sdk64/steamclient.so "$LIBDIR/steamclient.so"
fi
mkdir -p /mnt/server/Config /mnt/server/Citadel/Saved/Config /mnt/server/Engine/Saved/Config
for d in /mnt/server/Citadel/Saved/Config/LinuxServer /mnt/server/Engine/Saved/Config/LinuxServer; do
  if [ ! -L "$d" ]; then rm -rf "$d"; ln -s ../../../Config "$d"; fi
done
if [ ! -f /mnt/server/Config/Game.ini ]; then
  printf '[UWorks]\nConnectionPort=7777\nQueryPort=27015\n\n[/Script/Citadel.CitadelGameInstance]\nWorldCreationSettings=(ServerName="No-DAL Citadel",Password="",ServerType=PVP,PlayerLimit=20,bPrivate=false)\n' > /mnt/server/Config/Game.ini
fi
chmod +x /mnt/server/CitadelServer.sh 2>/dev/null || true
`

// Nova-Life server folder and server.json from the Pelican egg, using a
// fixed folder name so renaming the server does not orphan its save.
const moreNovaLifeConfig = `
mkdir -p /mnt/server/Servers/server/Config
if [ ! -f /mnt/server/Servers/server/Config/server.json ]; then
  printf '{"serverName":"server","serverListName":"%s","serverSlot":%s,"serverPort":7777,"isPublicServer":false,"useAdminPinAuth":false,"tabletUrl":"","isWhitelisted":false,"useWhitelistProtection":false,"whitelist":{"intro":"","questions":[],"date":""},"autoSaveIntervalSeconds":1800,"disconnectClientsBeforeStop":true,"mapId":0,"serverFramerate":60,"hasShop":false}\n' "${SERVER_NAME:-No-DAL Nova-Life}" "${MAX_PLAYERS:-25}" > /mnt/server/Servers/server/Config/server.json
fi
`

// moreSteam fills Game from the ID and completes the template with
// steamServer.
func moreSteam(t Template, appID, help string) Template {
	if t.Game == "" {
		t.Game = gameKey(t.ID)
	}
	return steamServer(t, appID, help)
}

// moreNS2Template builds Natural Selection 2 or NS2: Combat from the
// LinuxGSM ns2server / ns2cserver configs (spark engine).
func moreNS2Template(combat bool) Template {
	id, name, appID, lgsm, dir, bin, mapName, slots := "ndl-ns2", "Natural Selection 2", "4940", "ns2", "x64", "server_linux", "ns2_summit", "20"
	deps := []string{"speex", "libtbb12"}
	aliases := []string{"ns2", "natural selection 2", "natural selection"}
	extra := " -speclimit 5 -startmodserver -modserverport {{MOD_PORT}}"
	if combat {
		id, name, appID, lgsm, dir, bin, mapName, slots = "ndl-ns2-combat", "NS2: Combat", "313900", "ns2c", "ia32", "ns2combatserver_linux32", "co_core", "24"
		deps = append([]string{"speex:i386", "libtbb12"}, moreLib32...)
		aliases = []string{"ns2c", "ns2 combat", "ns2combat"}
		extra = ""
	}
	ports := []Port{
		{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "query", ContainerPort: 27016, Protocol: "udp"},
		{Name: "webadmin", ContainerPort: 8080, Protocol: "tcp", Env: "WEB_PORT"},
	}
	vars := []Variable{
		envText("Server name", "SERVER_NAME", "Name in the server browser.", "No-DAL "+name, true),
		envText("Map", "MAP", "Map loaded at boot.", mapName, true),
		envNumber("Max players", "MAX_PLAYERS", "Player limit.", slots, true),
		envNumber("Game port", "SERVER_PORT", "UDP game port. Steam query uses the next port.", "27015", true),
		envNumber("Web admin port", "WEB_PORT", "Web admin TCP port. User name is admin.", "8080", true),
		generatedSecret("Web admin password", "WEB_PASSWORD", "Password for the web admin user admin. Generated when left empty."),
	}
	if !combat {
		ports = append(ports, Port{Name: "modserver", ContainerPort: 27031, Protocol: "tcp", Env: "MOD_PORT"})
		vars = append(vars, envNumber("Mod server port", "MOD_PORT", "Built-in mod server TCP port.", "27031", true))
	}
	return moreSteam(Template{
		ID: id, Name: name, GameTitle: name,
		Summary:  name + " dedicated server with the built-in web admin.",
		Category: "shooter", Engine: "custom",
		Capabilities:    []string{CapQueries},
		Startup:         "mkdir -p \"$HOME/serverconfig/Workshop\" \"$HOME/logs\"; cd " + dir + " && exec ./" + bin + " -name \"{{SERVER_NAME}}\" -port {{SERVER_PORT}} -webadmin -webdomain 0.0.0.0 -webuser admin -webpassword \"$WEB_PASSWORD\" -webport {{WEB_PORT}} -map {{MAP}} -limit {{MAX_PLAYERS}}" + extra + " -config_path \"$HOME/serverconfig\" -logdir \"$HOME/logs\" -modstorage \"$HOME/serverconfig/Workshop\"",
		Stop:            "^C",
		DefaultPorts:    ports,
		Variables:       vars,
		Dependencies:    deps,
		DefaultMemoryMB: 2048, DefaultDiskMB: 12288, DefaultCPUs: 2,
		Tags:      []string{"fps", "rts", "pvp"},
		Aliases:   aliases,
		SourceRef: moreLGSMRef(lgsm),
		Notes:     []string{"Server settings, bans and admins are written to the serverconfig folder on the first start."},
	}, appID, name+" dedicated server.")
}

// Pavlov Game.ini from the LinuxGSM default and RconSettings.txt as in the
// Pelican egg.
const morePavlovConfig = `
mkdir -p /mnt/server/Pavlov/Saved/Config/LinuxServer /mnt/server/Pavlov/Saved/Logs
if [ ! -f /mnt/server/Pavlov/Saved/Config/LinuxServer/Game.ini ]; then
  printf '[/Script/Pavlov.DedicatedServer]\nbEnabled=true\nServerName="%s"\nMaxPlayers=10\nbSecured=true\nMapRotation=(MapId="datacenter", GameMode="SND")\nMapRotation=(MapId="sand", GameMode="DM")\nMapRotation=(MapId="bridge", GameMode="TDM")\n' "${SERVER_NAME:-No-DAL Pavlov}" > /mnt/server/Pavlov/Saved/Config/LinuxServer/Game.ini
fi
if [ ! -f /mnt/server/Pavlov/Saved/Config/RconSettings.txt ]; then
  printf 'Password=%s\nPort=8188\n' "$RCON_PASSWORD" > /mnt/server/Pavlov/Saved/Config/RconSettings.txt
fi
`
