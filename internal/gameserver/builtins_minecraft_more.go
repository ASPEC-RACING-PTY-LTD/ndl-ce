package gameserver

// Minecraft distributions beyond the core Paper, Vanilla, Fabric, Purpur and
// Velocity templates: other Java server builds, mod loaders, hybrids,
// limbo servers, proxies and Bedrock Edition servers.

// Spigot BuildTools compiles CraftBukkit and Spigot with Maven, which needs
// a JDK (javac); the eclipse-temurin JDK images are in RuntimeImages.

const mcmPeggs = "https://github.com/pelican-eggs/minecraft/tree/main/"

func mcmBedrockTitle() string { return "Minecraft: Bedrock Edition" }

func mcmJarVars(def string) []Variable {
	return []Variable{envText("Server jar", "SERVER_JARFILE", "Jar file name inside the server folder.", def, true)}
}

func mcmReleaseVar(help string) Variable {
	return envText("Release", "VERSION", help, "latest", true)
}

func mcmBedrockEULAVar() Variable {
	return Variable{
		Name: "Accept EULA", Env: "EULA",
		Description: "Must be true to start. Confirms you accept the Minecraft EULA and the Microsoft Privacy Statement.",
		Default:     "false", Viewable: true, Editable: true, Required: true, FieldType: "toggle",
	}
}

func mcmBedrockSettings() []Setting {
	return []Setting{
		{ID: "server-name", Label: "Server name", Help: "Shown in the Bedrock server list.", Kind: "text", File: "server.properties", Key: "server-name", Default: "Dedicated Server", Restart: true},
		{ID: "gamemode", Label: "Default game mode", Help: "New players start in this mode.", Kind: "select", File: "server.properties", Key: "gamemode", Default: "survival", Options: []string{"survival", "creative", "adventure"}, Restart: true},
		{ID: "difficulty", Label: "Difficulty", Help: "How hard the world is.", Kind: "select", File: "server.properties", Key: "difficulty", Default: "easy", Options: []string{"peaceful", "easy", "normal", "hard"}, Restart: true},
		{ID: "max-players", Label: "Player limit", Help: "How many people can be connected at once.", Kind: "number", File: "server.properties", Key: "max-players", Default: "10", Min: 1, Max: 200, Restart: true},
		{ID: "allow-cheats", Label: "Allow cheats", Help: "Lets operators use cheat commands.", Kind: "toggle", File: "server.properties", Key: "allow-cheats", Default: "false", Restart: true},
		{ID: "allow-list", Label: "Allow list", Help: "Only players in allowlist.json can join.", Kind: "toggle", File: "server.properties", Key: "allow-list", Default: "false", Restart: true},
		{ID: "online-mode", Label: "Online mode", Help: "Require Xbox Live sign-in.", Kind: "toggle", File: "server.properties", Key: "online-mode", Default: "true", Restart: true, Advanced: true},
		{ID: "level-name", Label: "World folder", Help: "Name of the world inside the worlds folder.", Kind: "text", File: "server.properties", Key: "level-name", Default: "Bedrock level", Restart: true},
	}
}

func mcmBedrockPorts() []Port {
	return []Port{
		{Name: "game", ContainerPort: 19132, Protocol: "udp", Primary: true, Fixed: true},
		{Name: "game-ipv6", ContainerPort: 19133, Protocol: "udp", Fixed: true},
	}
}

func mcmJDKImages() map[string]string {
	return map[string]string{
		"Java 21 JDK": "eclipse-temurin:21-jdk",
		"Java 25 JDK": "eclipse-temurin:25-jdk",
	}
}

func mcmJava(versions ...string) map[string]string {
	all := javaImages()
	out := map[string]string{}
	for _, v := range versions {
		out["Java "+v] = all["Java "+v]
	}
	return out
}

const nanoLimboBootstrap = `if [ ! -f settings.yml ]; then
  if ! command -v curl >/dev/null; then
    apt-get update
    apt-get install -y --no-install-recommends curl ca-certificates
  fi
  curl -fsSL https://raw.githubusercontent.com/Nan1t/NanoLimbo/main/src/main/resources/settings.yml -o settings.yml
  sed -i "s/^  ip: 'localhost'/  ip: '0.0.0.0'/; s/^  port: 65535/  port: 25565/" settings.yml
fi`

func minecraftMoreTemplates() []Template {
	return []Template{
		// Java Edition servers.
		minecraftJava(Template{
			ID: "ndl-minecraft-folia", Name: "Minecraft Folia", Implementation: "folia",
			Summary:        "PaperMC Folia server with regionised multithreading for very large player counts. Most Bukkit plugins need Folia support.",
			DefaultImage:   "eclipse-temurin:25-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}} --nogui",
			InstallBuiltin: "papermc", Install: &InstallSpec{Project: "folia"},
			Capabilities:    []string{CapPlugins},
			DefaultMemoryMB: 6144, DefaultDiskMB: 16384, DefaultCPUs: 4,
			Tags:    []string{"minecraft", "folia", "plugins", "performance"},
			Aliases: []string{"folia", "papermc", "mc", "multithreaded"},
			Variables: append([]Variable{
				envText("Minecraft version", "MC_VERSION", "Folia game version, or latest for the newest stable one.", "latest", true),
				envText("Build", "BUILD_NUMBER", "Build number. latest picks the newest build for the version.", "latest", false),
			}, mcmJarVars("server.jar")...),
			Content:   ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "plugins", Loader: "folia", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/folia", DocsURL: "https://docs.papermc.io/folia/",
			Notes: []string{"Minecraft 26.1 and newer need Java 25; pick Java 21 only for 1.21.x builds.", "Folia is meant for servers with many spread-out players and needs several CPU cores."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-spigot", Name: "Minecraft Spigot", Implementation: "spigot",
			Summary:        "Spigot server compiled on install with the official BuildTools. Runs Bukkit and Spigot plugins.",
			Images:         mcmJDKImages(),
			DefaultImage:   "eclipse-temurin:25-jdk",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}} nogui",
			InstallBuiltin: "minecraft-spigot",
			Capabilities:   []string{CapPlugins},
			Tags:           []string{"minecraft", "spigot", "bukkit", "plugins"},
			Aliases:        []string{"spigot", "spigotmc", "bukkit", "craftbukkit", "buildtools", "mc"},
			Variables: append([]Variable{
				envText("Minecraft version", "MC_VERSION", "BuildTools --rev value, for example 26.1.2, or latest.", "latest", true),
			}, mcmJarVars("server.jar")...),
			Content:   ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "plugins", Loader: "spigot", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/spigot", DocsURL: "https://www.spigotmc.org/wiki/buildtools/",
			Notes: []string{"Install compiles Spigot from source and can take 5 to 15 minutes and over 1 GB of RAM.", "BuildTools needs a JDK, so this template uses Temurin JDK images. Java 25 is needed for Minecraft 26.1 and newer."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-forge", Name: "Minecraft Forge", Implementation: "forge",
			Summary:        "Minecraft Forge server installed with the official Forge installer. Mods go in the mods folder.",
			DefaultImage:   "eclipse-temurin:25-jre",
			Startup:        moddedJavaStartup,
			InstallBuiltin: "minecraft-loader", Install: &InstallSpec{Project: "forge"},
			Capabilities:    []string{CapMods},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384,
			Tags:    []string{"minecraft", "forge", "mods", "modded"},
			Aliases: []string{"forge", "minecraftforge", "modded", "mc"},
			Variables: []Variable{
				envText("Minecraft version", "MC_VERSION", "Minecraft version Forge should target, for example 26.1.2 or 1.20.1.", "26.1.2", true),
				envText("Forge version", "LOADER_VERSION", "Forge build such as 64.1.0, or recommended / latest for the promoted build of this Minecraft version.", "recommended", true),
			},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", Loader: "forge", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/forge", DocsURL: "https://docs.minecraftforge.net/",
			Notes: []string{"Pick the Java version the Minecraft version needs: Java 25 for 26.1+, Java 21 for 1.20.5 to 1.21.x, Java 17 for 1.18 to 1.20.4, Java 8 for 1.16.5 and older.", "Change MC_VERSION and Reinstall to switch versions; mods must match."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-neoforge", Name: "Minecraft NeoForge", Implementation: "neoforge",
			Summary:        "NeoForge server installed with the official NeoForge installer. Mods go in the mods folder.",
			DefaultImage:   "eclipse-temurin:25-jre",
			Startup:        moddedJavaStartup,
			InstallBuiltin: "minecraft-loader", Install: &InstallSpec{Project: "neoforge"},
			Capabilities:    []string{CapMods},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384,
			Tags:    []string{"minecraft", "neoforge", "mods", "modded"},
			Aliases: []string{"neoforge", "neoforged", "neo", "modded", "mc"},
			Variables: []Variable{
				envText("Minecraft version", "MC_VERSION", "Full Minecraft version NeoForge should target, for example 26.1.2 or 1.21.1.", "26.1.2", true),
				envText("NeoForge version", "LOADER_VERSION", "NeoForge version, or latest for the newest stable build of this Minecraft version.", "latest", true),
			},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", Loader: "neoforge", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/neoforge", DocsURL: "https://docs.neoforged.net/",
			Notes: []string{"Use Java 25 for Minecraft 26.1+ and Java 21 for 1.20.2 to 1.21.x.", "latest skips beta builds, so a brand-new Minecraft version may need an explicit NeoForge version."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-quilt", Name: "Minecraft Quilt", Implementation: "quilt",
			Summary:        "Quilt mod loader server installed with the official Quilt installer. Most Fabric mods also run on Quilt.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Startup:        moddedJavaStartup,
			InstallBuiltin: "minecraft-loader", Install: &InstallSpec{Project: "quilt"},
			Capabilities:    []string{CapMods},
			DefaultMemoryMB: 3072, DefaultDiskMB: 12288,
			Tags:    []string{"minecraft", "quilt", "mods", "modded"},
			Aliases: []string{"quilt", "quiltmc", "modded", "mc"},
			Variables: []Variable{
				envText("Minecraft version", "MC_VERSION", "Minecraft version Quilt should target. It must be supported by Quilt Loader.", "1.21.1", true),
				envText("Quilt loader", "LOADER_VERSION", "Quilt Loader version, or latest.", "latest", true),
			},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", Loader: "quilt", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/quilt", DocsURL: "https://quiltmc.org/en/install/server/",
			Notes: []string{"Quilt support for new Minecraft releases often lags Fabric; the default stays on 1.21.1 (Java 21)."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-spongevanilla", Name: "Minecraft SpongeVanilla", Implementation: "spongevanilla",
			Summary:        "SpongeVanilla server for Sponge API plugins, downloaded from the Sponge downloads API.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}} nogui",
			InstallBuiltin: "minecraft-sponge",
			Capabilities:   []string{CapPlugins},
			Tags:           []string{"minecraft", "sponge", "plugins"},
			Aliases:        []string{"sponge", "spongevanilla", "spongepowered", "mc"},
			Variables: append([]Variable{
				envText("Sponge version", "SPONGE_VERSION", "SpongeVanilla build version, or recommended for the current recommended build.", "recommended", true),
			}, mcmJarVars("server.jar")...),
			Content:   ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "mods", Loader: "sponge", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/spongevanilla", DocsURL: "https://docs.spongepowered.org/",
			Notes: []string{"Sponge plugins go in the mods folder.", "Current Sponge builds target Minecraft 1.21 and need Java 21."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-arclight-neoforge", Name: "Arclight (NeoForge)", Implementation: "arclight-neoforge",
			Summary:        "Arclight hybrid server: NeoForge mods and Bukkit plugins on one server.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar arclight.jar nogui",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "IzzelAliz/Arclight", Asset: `^arclight-neoforge-.+\.jar$`, Archive: "file", Dest: "arclight.jar",
			}}},
			Capabilities:    []string{CapMods, CapPlugins},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384,
			Tags:      []string{"minecraft", "arclight", "hybrid", "mods", "plugins", "neoforge"},
			Aliases:   []string{"arclight", "hybrid", "neoforge", "bukkit", "mc"},
			Variables: []Variable{mcmReleaseVar("Arclight release tag (for example FeudalKings/1.0.1), or latest.")},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", Loader: "neoforge", GameID: "minecraft", Dependencies: true},
			SourceRef: "https://github.com/IzzelAliz/Arclight/releases", DocsURL: "https://github.com/IzzelAliz/Arclight/wiki",
			Notes: []string{"The current Arclight release targets Minecraft 1.21.1 (Java 21).", "First start downloads Minecraft and NeoForge libraries, so it needs internet access and takes a few minutes.", "Plugins go in plugins, mods in mods. Not every mod and plugin combination works."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-arclight-forge", Name: "Arclight (Forge)", Implementation: "arclight-forge",
			Summary:        "Arclight hybrid server: Forge mods and Bukkit plugins on one server.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar arclight.jar nogui",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "IzzelAliz/Arclight", Asset: `^arclight-forge-.+\.jar$`, Archive: "file", Dest: "arclight.jar",
			}}},
			Capabilities:    []string{CapMods, CapPlugins},
			DefaultMemoryMB: 4096, DefaultDiskMB: 16384,
			Tags:      []string{"minecraft", "arclight", "hybrid", "mods", "plugins", "forge"},
			Aliases:   []string{"arclight", "hybrid", "forge", "bukkit", "mc"},
			Variables: []Variable{mcmReleaseVar("Arclight release tag (for example FeudalKings/1.0.1), or latest.")},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", Loader: "forge", GameID: "minecraft", Dependencies: true},
			SourceRef: "https://github.com/IzzelAliz/Arclight/releases", DocsURL: "https://github.com/IzzelAliz/Arclight/wiki",
			Notes: []string{"The current Arclight release targets Minecraft 1.21.1 (Java 21).", "First start downloads Minecraft and Forge libraries, so it needs internet access and takes a few minutes."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-leaf", Name: "Minecraft Leaf", Implementation: "leaf",
			Summary:        "Leaf, a performance-focused Paper fork. Runs Paper and Bukkit plugins.",
			DefaultImage:   "eclipse-temurin:25-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar leaf.jar --nogui",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "Winds-Studio/Leaf", Asset: `^leaf-.+\.jar$`, Archive: "file", Dest: "leaf.jar",
			}}},
			Capabilities: []string{CapPlugins},
			Tags:         []string{"minecraft", "leaf", "paper", "plugins", "performance"},
			Aliases:      []string{"leaf", "leafmc", "paper fork", "mc"},
			Variables:    []Variable{mcmReleaseVar("Leaf release tag such as ver-26.2, or latest.")},
			Content:      ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "plugins", Loader: "paper", GameID: "minecraft", Dependencies: true},
			SourceRef:    "https://github.com/Winds-Studio/Leaf/releases",
			Notes:        []string{"Current Leaf releases target Minecraft 26.x and need Java 25."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-minecraft-modrinth-pack", Name: "Modrinth Modpack", Implementation: "modrinth-modpack",
			Summary:         "Installs any Modrinth modpack (.mrpack) with its loader and server-side mods.",
			DefaultImage:    "eclipse-temurin:25-jre",
			Startup:         moddedJavaStartup,
			InstallBuiltin:  "modrinth-modpack",
			Capabilities:    []string{CapMods},
			DefaultMemoryMB: 6144, DefaultDiskMB: 20480, DefaultCPUs: 2,
			Tags:    []string{"minecraft", "modpack", "modrinth", "mods", "modded"},
			Aliases: []string{"modrinth", "modpack", "mrpack", "modded", "mc"},
			Variables: []Variable{
				envText("Modpack", "MODPACK_ID", "Modrinth project slug or ID of the modpack, from its modrinth.com URL. The default is Adrenaline, a server performance pack.", "adrenaline", true),
				envText("Modpack version", "MODPACK_VERSION", "Modrinth version number or ID, or latest for the newest release.", "latest", true),
			},
			Content:   ContentSpec{Provider: "modrinth", Kind: "mod", InstallDir: "mods", GameID: "minecraft", Dependencies: true},
			SourceRef: mcmPeggs + "java/modrinth", DocsURL: "https://docs.modrinth.com/",
			Hint:  "Java version must match the pack",
			Notes: []string{"Choose the Java version the pack's Minecraft version needs (Java 25 for 26.1+, Java 21 for 1.20.5 to 1.21.x, Java 17 for 1.18 to 1.20.4, Java 8 for older) before installing.", "Supports packs using Fabric, Quilt, Forge or NeoForge. CurseForge packs are not supported."},
		}, false),
		minecraftJava(Template{
			ID: "ndl-nanolimbo", Name: "NanoLimbo", Implementation: "nanolimbo",
			Summary:        "Lightweight limbo server that holds players in an empty world, usually behind a proxy for AFK or queue use.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Images:         mcmJava("21", "25"),
			Startup:        "java -Xms64M -Xmx{{SERVER_MEMORY}}M -jar NanoLimbo.jar",
			Done:           "Server started on",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "Nan1t/NanoLimbo", Asset: `^NanoLimbo\.jar$`, Archive: "file", Dest: "NanoLimbo.jar",
				URL: "https://github.com/Nan1t/NanoLimbo/releases/download/{{VERSION}}/NanoLimbo.jar",
			}}},
			InstallScript:   nanoLimboBootstrap,
			DefaultMemoryMB: 512, MinMemoryMB: 256, DefaultDiskMB: 1024, DefaultCPUs: 1,
			Tags:        []string{"minecraft", "limbo", "lobby", "lightweight"},
			Aliases:     []string{"nanolimbo", "limbo", "afk", "queue", "mc"},
			Variables:   []Variable{mcmReleaseVar("NanoLimbo release tag such as v1.13.0, or latest.")},
			ConfigFiles: []ConfigFile{{Path: "settings.yml", Format: "yaml", Restart: true}},
			SourceRef:   mcmPeggs + "java/nanolimbo", DocsURL: "https://github.com/Nan1t/NanoLimbo",
			Notes: []string{"Supports Minecraft 1.7 to 26.2 clients. Needs Java 21+.", "Install writes settings.yml bound to 0.0.0.0:25565; set info forwarding there when it runs behind Velocity or BungeeCord."},
		}, true),
		nativeServer(Template{
			ID: "ndl-cuberite", Name: "Cuberite", Game: "cuberite", GameTitle: "Minecraft: Java Edition",
			Category: "minecraft", Engine: "custom", Family: "minecraft", Implementation: "cuberite",
			Summary:        "Cuberite, a lightweight C++ Minecraft server with Lua plugins. Supports Minecraft 1.8 to 1.12.2 clients.",
			Startup:        "if [ -x Server/Cuberite ]; then cd Server; fi; exec ./Cuberite",
			Stop:           "stop",
			Done:           "Startup complete",
			InstallBuiltin: "download",
			Install: &InstallSpec{Downloads: []Download{{
				URL: "https://download.cuberite.org/linux-x86_64/Cuberite.tar.gz", Archive: "tar.gz", Executables: []string{"Cuberite", "Server/Cuberite"},
			}}},
			Capabilities:    []string{CapPlugins, CapWorlds, CapPlayers},
			DefaultPorts:    []Port{{Name: "game", ContainerPort: 25565, Protocol: "tcp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 1,
			Tags:        []string{"minecraft", "cuberite", "lightweight", "plugins", "open-source", "classic"},
			Aliases:     []string{"cuberite", "mcserver", "mc", "c++"},
			ConfigFiles: []ConfigFile{{Path: "settings.ini", Format: "ini", Restart: true}},
			SourceRef:   mcmPeggs + "java/cuberite", DocsURL: "https://book.cuberite.org/",
			Notes: []string{"Players must use Minecraft 1.8 to 1.12.2 (Cuberite does not support newer protocols).", "Cuberite is not Mojang software, so no EULA is needed. Server port is Ports in settings.ini."},
		}),

		// Java Edition proxies.
		minecraftJava(Template{
			ID: "ndl-bungeecord", Name: "BungeeCord", Implementation: "bungeecord",
			Summary:        "SpigotMC BungeeCord proxy that links several Minecraft servers into one network. This is not a world server.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar BungeeCord.jar",
			Stop:           "end",
			Done:           "Listening on",
			InstallBuiltin: "download",
			Install: &InstallSpec{VersionEnv: "BUNGEE_BUILD", Downloads: []Download{{
				URL: "https://ci.md-5.net/job/BungeeCord/{{BUNGEE_BUILD}}/artifact/bootstrap/target/BungeeCord.jar", Archive: "file", Dest: "BungeeCord.jar",
			}}},
			Capabilities:    []string{CapPlugins},
			DefaultPorts:    []Port{{Name: "proxy", ContainerPort: 25577, Protocol: "tcp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:    []string{"minecraft", "proxy", "bungeecord"},
			Aliases: []string{"bungeecord", "bungee", "proxy", "mc"},
			Hint:    "Proxy, not a world",
			Variables: []Variable{
				envText("Jenkins build", "BUNGEE_BUILD", "ci.md-5.net build number, or lastSuccessfulBuild for the newest.", "lastSuccessfulBuild", true),
			},
			ConfigFiles: []ConfigFile{{Path: "config.yml", Format: "yaml", Restart: true}},
			Content:     ContentSpec{Provider: "modrinth", Kind: "plugin", InstallDir: "plugins", Loader: "bungeecord", GameID: "minecraft", Dependencies: true},
			SourceRef:   mcmPeggs + "proxy/java/bungeecord", DocsURL: "https://www.spigotmc.org/wiki/bungeecord/",
			Notes: []string{"Backend servers must run in offline mode with bungeecord: true (Spigot) or Paper's proxy settings, and should not be reachable directly."},
		}, true),
		nativeServer(Template{
			ID: "ndl-gate", Name: "Gate", Game: "minecraft", GameTitle: "Minecraft: Java Edition",
			Category: "proxy", Engine: "custom", Family: "minecraft", Implementation: "gate",
			Summary:        "Minekube Gate, a lightweight Minecraft proxy written in Go. This is not a world server.",
			Startup:        "if [ ! -f config.yml ]; then ./gate config --write; fi; exec ./gate",
			Stop:           "^C",
			Done:           "listening for connections",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "minekube/gate", Asset: `^gate_[0-9.]+_linux_amd64$`, Archive: "file", Dest: "gate", Executables: []string{"gate"},
			}}},
			DefaultPorts:    []Port{{Name: "proxy", ContainerPort: 25565, Protocol: "tcp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 512, DefaultDiskMB: 1024, DefaultCPUs: 1,
			Tags:        []string{"minecraft", "proxy", "gate", "open-source"},
			Aliases:     []string{"gate", "minekube", "proxy", "mc"},
			Hint:        "Proxy, not a world",
			Variables:   []Variable{mcmReleaseVar("Gate release tag such as v0.74.28, or latest.")},
			ConfigFiles: []ConfigFile{{Path: "config.yml", Format: "yaml", Restart: true}},
			SourceRef:   "https://github.com/minekube/gate/releases", DocsURL: "https://gate.minekube.com/",
			Notes: []string{"First start writes config.yml with bind 0.0.0.0:25565; add your backend servers under config.servers and restart.", "Gate has no console commands; stopping sends Ctrl+C."},
		}),
		minecraftJava(Template{
			ID: "ndl-viaproxy", Name: "ViaProxy", Implementation: "viaproxy",
			Summary:        "Standalone ViaVersion proxy that lets newer or older clients join a server of a different Minecraft version.",
			DefaultImage:   "eclipse-temurin:21-jre",
			Images:         mcmJava("17", "21", "25"),
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar ViaProxy.jar config viaproxy.yml",
			Done:           "Binding proxy server to",
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "ViaVersion/ViaProxy", Asset: `^ViaProxy-[0-9.]+\.jar$`, Archive: "file", Dest: "ViaProxy.jar",
			}}},
			DefaultPorts:    []Port{{Name: "proxy", ContainerPort: 25568, Protocol: "tcp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:        []string{"minecraft", "proxy", "viaversion", "cross-version"},
			Aliases:     []string{"viaproxy", "viaversion", "via", "proxy", "mc"},
			Hint:        "Proxy, not a world",
			Variables:   []Variable{mcmReleaseVar("ViaProxy release tag such as v3.4.14, or latest.")},
			ConfigFiles: []ConfigFile{{Path: "viaproxy.yml", Format: "yaml", Restart: true}},
			SourceRef:   mcmPeggs + "proxy/java/viaproxy", DocsURL: "https://github.com/ViaVersion/ViaProxy",
			Notes: []string{"The first start writes viaproxy.yml and exits. Set target-address to your backend server, then start again.", "Default bind address is 0.0.0.0:25568."},
		}, true),

		// Bedrock Edition.
		{
			ID: "ndl-minecraft-bedrock", Name: "Minecraft Bedrock Dedicated Server", Game: "minecraft-bedrock",
			GameTitle: mcmBedrockTitle(), Category: "minecraft", Engine: "bedrock", Family: "minecraft-bedrock", Implementation: "bedrock",
			Summary:         "Official Mojang Bedrock Dedicated Server for Windows 10/11, console, and mobile players.",
			Capabilities:    baseCaps(CapWorlds, CapPlayers, CapEULA),
			Images:          map[string]string{"Debian 12": "debian:bookworm-slim"},
			DefaultImage:    "debian:bookworm-slim",
			Dependencies:    []string{"ca-certificates", "libcurl4"},
			Startup:         "export LD_LIBRARY_PATH=.; exec ./bedrock_server",
			Stop:            "stop",
			Done:            "Server started.",
			WorkingDir:      workDir,
			InstallBuiltin:  "minecraft-bedrock",
			DefaultPorts:    mcmBedrockPorts(),
			Requirements:    []Requirement{{Kind: ReqEULA, Stage: StageStart, Env: "EULA", Label: "Accept the Minecraft EULA and Microsoft Privacy Statement", URL: "https://www.minecraft.net/en-us/eula"}},
			DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 2,
			Tags:    []string{"minecraft", "bedrock", "crossplay", "vanilla", "survival"},
			Aliases: []string{"bedrock", "bds", "mcpe", "pocket edition", "minecraft bedrock", "mc"},
			Variables: []Variable{
				envText("Bedrock version", "BEDROCK_VERSION", "Server version such as 1.26.50.2, or latest.", "latest", true),
				mcmBedrockEULAVar(),
			},
			ConfigFiles:    []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}},
			FriendlyConfig: mcmBedrockSettings(),
			SourceRef:      mcmPeggs + "bedrock/bedrock", DocsURL: "https://www.minecraft.net/en-us/download/server/bedrock",
			Notes: []string{"Clients must run the same Bedrock version as the server; update with Reinstall when the game updates.", "Uses UDP 19132 (IPv4) and 19133 (IPv6), set in server.properties.", "Mojang ships the Linux server for x86_64 only."},
		},
		{
			ID: "ndl-powernukkitx", Name: "PowerNukkitX", Game: "powernukkitx",
			GameTitle: mcmBedrockTitle(), Category: "minecraft", Engine: "java", Family: "minecraft-bedrock", Implementation: "powernukkitx",
			Summary:        "PowerNukkitX, an open-source Bedrock Edition server written in Java with plugin support.",
			Capabilities:   baseCaps(CapJava, CapPlugins, CapWorlds, CapPlayers),
			Images:         mcmJava("25"),
			DefaultImage:   "eclipse-temurin:25-jre",
			Architectures:  []string{"amd64", "arm64"},
			Startup:        `java -Xms128M -Xmx{{SERVER_MEMORY}}M -Dfile.encoding=UTF-8 -Djansi.passthrough=true -Dterminal.ansi=true --add-opens java.base/java.lang=ALL-UNNAMED --add-opens java.base/java.io=ALL-UNNAMED --add-opens java.base/java.net=ALL-UNNAMED --enable-native-access=ALL-UNNAMED --sun-misc-unsafe-memory-access=allow -cp "powernukkitx.jar:./libs/*" org.powernukkitx.PowerNukkitX --skip-setup --accept-license --language eng`,
			Stop:           "stop",
			Done:           "For help, type",
			WorkingDir:     workDir,
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "PowerNukkitX/PowerNukkitX", Asset: `^powernukkitx\.jar$`, Archive: "file", Dest: "powernukkitx.jar",
				URL: "https://github.com/PowerNukkitX/PowerNukkitX/releases/download/{{VERSION}}/powernukkitx.jar",
			}}},
			DefaultPorts:    []Port{{Name: "game", ContainerPort: 19132, Protocol: "udp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 2,
			Tags:    []string{"minecraft", "bedrock", "plugins", "open-source"},
			Aliases: []string{"powernukkitx", "pnx", "nukkit", "bedrock", "mcpe"},
			Variables: []Variable{
				mcmReleaseVar("PowerNukkitX release tag such as 3.0.5, or latest."),
				envNumber("Server memory (MiB)", "SERVER_MEMORY", "Java heap size. Keep this at or below the RAM you allocate.", "1536", true),
			},
			ConfigFiles: []ConfigFile{{Path: "server.properties", Format: "properties", Restart: true, Parser: "properties"}},
			SourceRef:   "https://github.com/PowerNukkitX/PowerNukkitX/blob/master/docker/Dockerfile",
			DocsURL:     "https://github.com/PowerNukkitX/PowerNukkitX",
			Notes:       []string{"Needs Java 25. The start line matches the upstream Docker image and skips the first-run setup wizard (language English, licence notice accepted).", "Each release supports one Bedrock protocol; update when clients update."},
		},
		{
			ID: "ndl-allay", Name: "Allay", Game: "allay",
			GameTitle: mcmBedrockTitle(), Category: "minecraft", Engine: "java", Family: "minecraft-bedrock", Implementation: "allay",
			Summary:        "Allay, an open-source Bedrock Edition server written in Java with a plugin API.",
			Capabilities:   baseCaps(CapJava, CapPlugins, CapWorlds, CapPlayers),
			Images:         mcmJava("21", "25"),
			DefaultImage:   "eclipse-temurin:21-jre",
			Architectures:  []string{"amd64", "arm64"},
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar allay-server.jar",
			Stop:           "stop",
			WorkingDir:     workDir,
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "AllayMC/Allay", Asset: `^allay-server-.+-shaded\.jar$`, Archive: "file", Dest: "allay-server.jar",
			}}},
			DefaultPorts:    mcmBedrockPorts(),
			DefaultMemoryMB: 2048, MinMemoryMB: 1024, DefaultDiskMB: 4096, DefaultCPUs: 2,
			Tags:    []string{"minecraft", "bedrock", "plugins", "open-source"},
			Aliases: []string{"allay", "allaymc", "bedrock", "mcpe"},
			Variables: []Variable{
				mcmReleaseVar("Allay release tag such as 0.14.0, or latest."),
				envNumber("Server memory (MiB)", "SERVER_MEMORY", "Java heap size. Keep this at or below the RAM you allocate.", "1536", true),
			},
			ConfigFiles: []ConfigFile{{Path: "server-settings.yml", Format: "yaml", Restart: true}},
			SourceRef:   "https://github.com/AllayMC/Allay/releases", DocsURL: "https://github.com/AllayMC/Allay",
			Notes: []string{"Allay is under active development and not every vanilla feature is implemented.", "Ports are network.port (19132) and network.portv6 (19133) in server-settings.yml."},
		},
		{
			ID: "ndl-waterdog-pe", Name: "WaterdogPE", Game: "minecraft-bedrock",
			GameTitle: mcmBedrockTitle(), Category: "proxy", Engine: "java", Family: "minecraft-bedrock", Implementation: "waterdogpe",
			Summary:        "WaterdogPE proxy that links several Bedrock Edition servers into one network. This is not a world server.",
			Capabilities:   baseCaps(CapJava, CapPlugins),
			Images:         mcmJava("17", "21", "25"),
			DefaultImage:   "eclipse-temurin:21-jre",
			Architectures:  []string{"amd64", "arm64"},
			Startup:        "java -Dterminal.ansi=true -Xms128M -Xmx{{SERVER_MEMORY}}M -jar Waterdog.jar",
			Stop:           "end",
			Done:           "Started query on",
			WorkingDir:     workDir,
			InstallBuiltin: "github-release",
			Install: &InstallSpec{VersionEnv: "VERSION", Downloads: []Download{{
				Repo: "WaterdogPE/WaterdogPE", Asset: `^Waterdog\.jar$`, Archive: "file", Dest: "Waterdog.jar",
				URL: "https://github.com/WaterdogPE/WaterdogPE/releases/download/{{VERSION}}/Waterdog.jar",
			}}},
			DefaultPorts:    []Port{{Name: "proxy", ContainerPort: 19132, Protocol: "udp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:    []string{"minecraft", "bedrock", "proxy", "waterdog"},
			Aliases: []string{"waterdog", "waterdogpe", "bedrock proxy", "proxy", "mcpe"},
			Hint:    "Proxy, not a world",
			Variables: []Variable{
				mcmReleaseVar("WaterdogPE release tag such as v2.0.3, or latest."),
				envNumber("Server memory (MiB)", "SERVER_MEMORY", "Java heap size.", "768", true),
			},
			ConfigFiles: []ConfigFile{{Path: "config.yml", Format: "yaml", Restart: true}},
			SourceRef:   mcmPeggs + "proxy/bedrock/waterdog_pe", DocsURL: "https://github.com/WaterdogPE/WaterdogPE",
			Notes: []string{"Backend Bedrock servers are listed in config.yml; the listener port is set there (default 19132)."},
		},
		{
			ID: "ndl-geyser-standalone", Name: "Geyser Standalone", Game: "minecraft-bedrock",
			GameTitle: mcmBedrockTitle(), Category: "proxy", Engine: "java", Family: "minecraft-bedrock", Implementation: "geyser",
			Summary:        "GeyserMC standalone proxy that lets Bedrock Edition players join a Java Edition server.",
			Capabilities:   baseCaps(CapJava),
			Images:         mcmJava("21", "25"),
			DefaultImage:   "eclipse-temurin:21-jre",
			Architectures:  []string{"amd64", "arm64"},
			Startup:        "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar Geyser-Standalone.jar",
			Stop:           "geyser shutdown",
			Done:           "Run /geyser help for help",
			WorkingDir:     workDir,
			InstallBuiltin: "download",
			Install: &InstallSpec{Downloads: []Download{{
				URL: "https://download.geysermc.org/v2/projects/geyser/versions/latest/builds/latest/downloads/standalone", Archive: "file", Dest: "Geyser-Standalone.jar",
			}}},
			DefaultPorts:    []Port{{Name: "bedrock", ContainerPort: 19132, Protocol: "udp", Primary: true, Fixed: true}},
			DefaultMemoryMB: 1024, DefaultDiskMB: 2048, DefaultCPUs: 1,
			Tags:    []string{"minecraft", "bedrock", "proxy", "crossplay", "geyser"},
			Aliases: []string{"geyser", "geysermc", "crossplay", "bedrock to java", "proxy"},
			Hint:    "Bridge, not a world",
			Variables: []Variable{
				envNumber("Server memory (MiB)", "SERVER_MEMORY", "Java heap size.", "768", true),
			},
			ConfigFiles: []ConfigFile{{Path: "config.yml", Format: "yaml", Restart: true}},
			SourceRef:   "https://github.com/GeyserMC/pterodactyl-stuff", DocsURL: "https://geysermc.org/wiki/geyser/",
			Notes: []string{"Set java.address and java.port in config.yml to the Java server players should reach.", "Bedrock players need a Java account, or install Floodgate on the Java server for Bedrock-only accounts.", "Reinstall downloads the newest Geyser build."},
		},
	}
}
