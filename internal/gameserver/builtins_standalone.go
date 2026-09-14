package gameserver

func terrariaTemplate() Template {
	return Template{
		ID:              "ndl-terraria",
		Name:            "Terraria",
		Game:            "terraria",
		Implementation:  "vanilla",
		Family:          "terraria",
		Summary:         "Official Terraria dedicated server.",
		Capabilities:    baseCaps(CapWorlds),
		Images:          map[string]string{"Debian": "debian:bookworm-slim"},
		DefaultImage:    "debian:bookworm-slim",
		Startup:         "sh -c './TerrariaServer.bin.x86_64 -port {{SERVER_PORT}} -maxplayers {{MAX_PLAYERS}} -world /home/container/Worlds/{{WORLD}}.wld -worldname \"{{WORLD}}\" -autocreate {{AUTOCREATE}} ${SERVER_PASSWORD:+-password \"$SERVER_PASSWORD\"}'",
		Stop:            "exit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "terraria",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 7777, Protocol: "tcp", Primary: true}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   8192,
		DefaultCPUs:     2,
		Tags:            []string{"sandbox", "coop"},
		Aliases:         []string{"terraria", "terra"},
		Variables: []Variable{
			envText("Dedicated build", "TERRARIA_VERSION", "Official zip build number, for example 1449 for 1.4.4.9.", "1449", true),
			envText("World name", "WORLD", "World file name without .wld.", "World", true),
			envNumber("Max players", "MAX_PLAYERS", "Player slots.", "8", true),
			envNumber("Game port", "SERVER_PORT", "TCP game port.", "7777", true),
			envText("Autocreate size", "AUTOCREATE", "1 small, 2 medium, 3 large. Used only when the world file is missing.", "2", true),
			envSecret("Join password", "SERVER_PASSWORD", "Optional. Leave empty for an open world.", false),
		},
		FriendlyConfig: []Setting{
			{ID: "world", Label: "World name", Help: "Saved under Worlds/.", Kind: "text", Env: "WORLD", Restart: true},
			{ID: "slots", Label: "Player slots", Help: "How many clients can join.", Kind: "number", Env: "MAX_PLAYERS", Restart: true},
		},
		Content: ContentSpec{Provider: "local", Kind: "world", InstallDir: "Worlds"},
	}
}

func factorioTemplate() Template {
	return Template{
		ID:              "ndl-factorio",
		Name:            "Factorio",
		Game:            "factorio",
		Implementation:  "headless",
		Family:          "factorio",
		Summary:         "Official Factorio headless dedicated server.",
		Capabilities:    baseCaps(CapWorlds),
		Images:          map[string]string{"Debian": "debian:bookworm-slim"},
		DefaultImage:    "debian:bookworm-slim",
		Startup:         "sh -c 'SAVE=\"{{SAVE_NAME}}\"; mkdir -p saves; if [ ! -f \"saves/${SAVE}.zip\" ]; then ./bin/x64/factorio --create \"saves/${SAVE}.zip\"; fi; exec ./bin/x64/factorio --start-server \"saves/${SAVE}.zip\" --server-settings server-settings.json --port {{SERVER_PORT}}'",
		Stop:            "/quit",
		WorkingDir:      "/home/container",
		InstallBuiltin:  "factorio",
		DefaultPorts:    []Port{{Name: "game", ContainerPort: 34197, Protocol: "udp", Primary: true}},
		DefaultMemoryMB: 2048,
		DefaultDiskMB:   8192,
		DefaultCPUs:     2,
		Tags:            []string{"factory", "coop", "sandbox"},
		Aliases:         []string{"factorio", "fact"},
		Variables: []Variable{
			envText("Factorio version", "FACTORIO_VERSION", "stable, latest, or a version such as 2.0.60.", "stable", true),
			envText("Save name", "SAVE_NAME", "Save file name without .zip. Created on first start if missing.", "world", true),
			envNumber("Game port", "SERVER_PORT", "UDP game port.", "34197", true),
		},
		ConfigFiles: []ConfigFile{{Path: "server-settings.json", Format: "json", Restart: true, Parser: "json"}},
		FriendlyConfig: []Setting{
			{ID: "save", Label: "Save name", Help: "Created automatically when missing.", Kind: "text", Env: "SAVE_NAME", Restart: true},
		},
		Content: ContentSpec{Provider: "local", Kind: "mod", InstallDir: "mods"},
	}
}
