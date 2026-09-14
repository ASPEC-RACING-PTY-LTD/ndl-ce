package gameserver

func baseCaps(extra ...string) []string {
	core := []string{
		CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources, CapBackups, CapSchedules,
	}
	return normalizeCapabilities(append(core, extra...))
}

func steamAppVar(appid, help string) Variable {
	return Variable{
		Name: "Steam app ID", Env: "SRCDS_APPID", Description: help,
		Default: appid, Viewable: true, Editable: false, Required: true,
	}
}

func envText(name, env, help, def string, required bool) Variable {
	return Variable{
		Name: name, Env: env, Description: help, Default: def,
		Viewable: true, Editable: true, Required: required, FieldType: "text",
	}
}

func envNumber(name, env, help, def string, required bool) Variable {
	return Variable{
		Name: name, Env: env, Description: help, Default: def,
		Viewable: true, Editable: true, Required: required, FieldType: "number",
	}
}

func envSecret(name, env, help string, required bool) Variable {
	return envSecretDefault(name, env, help, "", required)
}

func envSecretDefault(name, env, help, def string, required bool) Variable {
	return Variable{
		Name: name, Env: env, Description: help, Default: def,
		Viewable: true, Editable: true, Required: required, Secret: true, FieldType: "password",
	}
}

func minecraftEULAVar() Variable {
	return Variable{
		Name: "Accept EULA", Env: "EULA",
		Description: "Must be true to start. This writes eula.txt.",
		Default:     "false", Viewable: true, Editable: true, Required: true, FieldType: "toggle",
	}
}

func minecraftWorldSettings() []Setting {
	return []Setting{
		{ID: "motd", Label: "Server name shown in the list", Help: "The text friends see before they join.", Kind: "text", File: "server.properties", Key: "motd", Default: "A Minecraft Server", Restart: true},
		{ID: "max-players", Label: "Player limit", Help: "How many people can be connected at once.", Kind: "number", File: "server.properties", Key: "max-players", Default: "20", Min: 1, Max: 200, Restart: true},
		{ID: "difficulty", Label: "Difficulty", Help: "How hard the world is.", Kind: "select", File: "server.properties", Key: "difficulty", Default: "easy", Options: []string{"peaceful", "easy", "normal", "hard"}, Restart: true},
		{ID: "gamemode", Label: "Default game mode", Help: "New players start in this mode.", Kind: "select", File: "server.properties", Key: "gamemode", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, Restart: true},
		{ID: "white-list", Label: "Whitelist", Help: "Only approved players can join.", Kind: "toggle", File: "server.properties", Key: "white-list", Default: "false", Restart: true},
		{ID: "pvp", Label: "Player versus player", Help: "Allow players to hurt each other.", Kind: "toggle", File: "server.properties", Key: "pvp", Default: "true", Restart: false},
		{ID: "online-mode", Label: "Online mode", Help: "Check players against Minecraft accounts. Turn off only for a private LAN-style server.", Kind: "toggle", File: "server.properties", Key: "online-mode", Default: "true", Restart: true, Advanced: true},
		{ID: "level-name", Label: "World folder", Help: "Name of the world directory.", Kind: "text", File: "server.properties", Key: "level-name", Default: "world", Restart: true},
		{ID: "view-distance", Label: "View distance", Help: "How far the world loads around each player. Higher uses more RAM.", Kind: "number", File: "server.properties", Key: "view-distance", Default: "10", Min: 3, Max: 32, Restart: true},
	}
}

func javaMinecraftImage() map[string]string {
	return map[string]string{"Java 21": "eclipse-temurin:21-jre"}
}

func steamImage() map[string]string {
	return map[string]string{"Debian": "steamcmd/steamcmd:debian"}
}

func steamLoginVars() []Variable {
	return []Variable{
		{Name: "Steam username", Env: "STEAM_USER", Description: "Leave blank for anonymous SteamCMD. Only fill this when the dedicated app requires an account that owns the game.", Default: "", Viewable: true, Editable: true, FieldType: "text"},
		{Name: "Steam password", Env: "STEAM_PASS", Description: "Used only when Steam username is set. Steam Guard codes go in the next field.", Default: "", Viewable: false, Editable: true, Secret: true, FieldType: "password"},
		{Name: "Steam Guard code", Env: "STEAM_GUARD", Description: "One-time Steam Guard code if Steam asks for it during install.", Default: "", Viewable: false, Editable: true, Secret: true, FieldType: "password"},
	}
}
