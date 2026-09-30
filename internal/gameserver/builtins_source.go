package gameserver

import "strings"

// Source engine (srcds) and GoldSrc (hlds) dedicated servers. Every app ID,
// game directory, launcher, default map and slot count below comes from the
// LinuxGSM default config linked in SourceRef (or the Pelican egg where
// noted). Shared launch details live in srcdsTemplate and goldsrcTemplate.

const lgsmCfg = "https://github.com/GameServerManagers/LinuxGSM/blob/master/lgsm/config-default/config-lgsm/"

func lgsmRef(name string) string {
	return lgsmCfg + name + "server/_default.cfg"
}

// steamcmdRuntimeDeps are the 32-bit runtime libraries LinuxGSM installs
// for every SteamCMD game on Debian 13 (lgsm/data/debian-13.csv).
func steamcmdRuntimeDeps(extra ...string) []string {
	return append([]string{"lib32gcc-s1", "lib32stdc++6", "libsdl2-2.0-0:i386"}, extra...)
}

// srcdsLauncher builds a Source template whose launcher differs from
// "./srcds_run -game <dir>" (mods that ship their own start script). The
// launcher replaces that prefix; every other argument stays the same.
func srcdsLauncher(g SourceGame, launcher string) Template {
	t := srcdsTemplate(g)
	bin := g.Binary
	if bin == "" {
		bin = "srcds_run"
	}
	t.Startup = strings.Replace(t.Startup, "./"+bin+" -game "+g.GameDir, launcher, 1)
	return t
}

// srvLibLinks recreates the *_srv.so symlinks some Source SDK 2013 based
// servers expect (LinuxGSM fix_nmrih.sh, fix_sfc.sh).
const srvLibLinks = `
for lib in datacache dedicated engine materialsystem replay scenefilecache shaderapiempty soundemittersystem studiorender vphysics; do
  if [ -f "/mnt/server/bin/${lib}_srv.so" ] && [ ! -e "/mnt/server/bin/${lib}.so" ]; then
    ln -s "${lib}_srv.so" "/mnt/server/bin/${lib}.so"
  fi
done
`

// csgoFixes mirrors LinuxGSM fix_csgo.sh: the server does not always write
// steam_appid.txt, and the bundled libgcc_s.so.1 breaks on newer distros.
const csgoFixes = `
if [ ! -f /mnt/server/steam_appid.txt ]; then
  printf '730' > /mnt/server/steam_appid.txt
fi
if [ -f /mnt/server/bin/libgcc_s.so.1 ]; then
  mv -f /mnt/server/bin/libgcc_s.so.1 /mnt/server/bin/libgcc_s.so.1.bak
fi
`

// steamclientTo copies the 32-bit Steam client library to a directory where
// the server loads it from (LinuxGSM fix_steamcmd.sh).
func steamclientTo(dir string) string {
	return `
if [ -f /mnt/server/.steam/sdk32/steamclient.so ]; then
  mkdir -p "/mnt/server/` + dir + `"
  cp -f /mnt/server/.steam/sdk32/steamclient.so "/mnt/server/` + dir + `/steamclient.so"
fi
`
}

// tf2BaseInstall installs the Team Fortress 2 dedicated server (app 232250)
// into ./tf2, which Team Fortress 2 Classified loads with -tf_path
// (LinuxGSM tf2cserver baseappid and supportdir).
const tf2BaseInstall = `
ok=0
for attempt in 1 2 3; do
  echo "steamcmd app_update 232250 (Team Fortress 2 base content) attempt ${attempt}"
  if steamcmd +force_install_dir /mnt/server/tf2 +login anonymous +app_update 232250 validate +quit; then
    if [ -d /mnt/server/tf2/tf ]; then ok=1; break; fi
  fi
  sleep $((attempt * 5))
done
if [ "$ok" != 1 ]; then
  echo "could not install Team Fortress 2 base content (app 232250)" >&2
  exit 1
fi
`

const goldsrcNote = "SteamCMD often needs more than one pass for app 90 (HLDS). The installer retries; reinstall if files are still missing."

func sourceTemplates() []Template {
	var out []Template

	// Source engine, Valve titles.
	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-css", Name: "Counter-Strike: Source", GameTitle: "Counter-Strike: Source",
		Summary: "Counter-Strike: Source dedicated server (srcds) via SteamCMD.",
		AppID:   "232330", GameDir: "cstrike", Map: "de_dust2", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "classic", "competitive"},
		Aliases:  []string{"css", "cs source", "cssource", "counter-strike source"},
		MemoryMB: 1024, DiskMB: 6144, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("css"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Source_Dedicated_Server",
	}))

	csgo := srcdsTemplate(SourceGame{
		ID: "ndl-csgo", Name: "Counter-Strike: Global Offensive (legacy)", GameTitle: "Counter-Strike: Global Offensive",
		Summary: "Legacy CS:GO dedicated server (app 740) via SteamCMD, for players on the csgo_legacy branch.",
		AppID:   "740", GameDir: "csgo", Map: "de_mirage", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "competitive", "legacy"},
		Aliases:  []string{"csgo", "cs:go", "cs go", "global offensive", "csgo legacy"},
		MemoryMB: 2048, DiskMB: 49152, GSLT: true,
		ExtraArgs:    "-tickrate 64 -maxplayers_override {{MAX_PLAYERS}} +game_type 0 +game_mode 0 +mapgroup mg_active -nobreakpad",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("csgo"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Counter-Strike:_Global_Offensive_Dedicated_Servers",
		Notes: []string{
			"CS:GO was replaced by Counter-Strike 2. Players must select the csgo_legacy beta in Steam to join this server.",
			"Without a GSLT (app 730) the server is limited to LAN play.",
		},
	})
	csgo.InstallScript = csgoFixes
	csgo.Hint = "GSLT needed for internet play"
	out = append(out, csgo)

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-dods", Name: "Day of Defeat: Source", GameTitle: "Day of Defeat: Source",
		Summary: "Day of Defeat: Source dedicated server via SteamCMD.",
		AppID:   "232290", GameDir: "dod", Map: "dod_Anzio", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "ww2", "classic"},
		Aliases:  []string{"dods", "dod source", "day of defeat source"},
		MemoryMB: 1024, DiskMB: 6144,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("dods"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Source_Dedicated_Server",
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-hl2dm", Name: "Half-Life 2: Deathmatch", GameTitle: "Half-Life 2: Deathmatch",
		Summary: "Half-Life 2: Deathmatch dedicated server via SteamCMD.",
		AppID:   "232370", GameDir: "hl2mp", Map: "dm_lockdown", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch", "classic"},
		Aliases:  []string{"hl2dm", "hl2mp", "half-life 2 deathmatch"},
		MemoryMB: 1024, DiskMB: 6144,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("hl2dm"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Source_Dedicated_Server",
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-hldms", Name: "Half-Life Deathmatch: Source", GameTitle: "Half-Life Deathmatch: Source",
		Summary: "Half-Life Deathmatch: Source dedicated server via SteamCMD.",
		AppID:   "255470", GameDir: "hl1mp", Map: "crossfire", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch", "classic"},
		Aliases:  []string{"hldms", "hl1mp", "hldm source", "half-life deathmatch source"},
		MemoryMB: 1024, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("hldms"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-l4d", Name: "Left 4 Dead", GameTitle: "Left 4 Dead",
		Summary: "Left 4 Dead (the original) dedicated server via SteamCMD.",
		AppID:   "222840", GameDir: "left4dead", Map: "l4d_hospital01_apartment", MaxPlayers: 8,
		Category: "shooter", Tags: []string{"coop", "zombies", "versus"},
		Aliases:  []string{"l4d", "l4d1", "left 4 dead", "left4dead"},
		MemoryMB: 1024, DiskMB: 12288,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("l4d"),
	}))

	// Source engine, third-party titles.
	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-bmdm", Name: "Black Mesa: Deathmatch", GameTitle: "Black Mesa",
		Summary: "Black Mesa deathmatch dedicated server via SteamCMD.",
		AppID:   "346680", GameDir: "bms", Map: "dm_bounce", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch"},
		Aliases:  []string{"bmdm", "black mesa", "bms"},
		MemoryMB: 2048, DiskMB: 20480, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("bmdm"),
		Notes:        []string{"LinuxGSM also lists libncurses5:i386, which is not packaged for Debian 13; the server normally starts without it."},
	}))

	nmrih := srcdsTemplate(SourceGame{
		ID: "ndl-nmrih", Name: "No More Room in Hell", GameTitle: "No More Room in Hell",
		Summary: "No More Room in Hell co-op zombie survival dedicated server via SteamCMD.",
		AppID:   "317670", GameDir: "nmrih", Map: "nmo_broadway", MaxPlayers: 8,
		Category: "shooter", Tags: []string{"coop", "zombies", "survival", "free"},
		Aliases:  []string{"nmrih", "no more room in hell"},
		MemoryMB: 1024, DiskMB: 12288, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("nmrih"),
	})
	nmrih.InstallScript = srvLibLinks
	out = append(out, nmrih)

	ins := srcdsTemplate(SourceGame{
		ID: "ndl-insurgency", Name: "Insurgency (2014)", GameTitle: "Insurgency",
		Summary: "Insurgency (2014, Source engine) dedicated server via SteamCMD. Not Insurgency: Sandstorm.",
		AppID:   "237410", GameDir: "insurgency", Map: "embassy_coop checkpoint", MaxPlayers: 32,
		Category: "tactical", Tags: []string{"pvp", "coop", "realism"},
		Aliases:  []string{"ins", "insurgency", "insurgency 2014"},
		MemoryMB: 2048, DiskMB: 16384, GSLT: true,
		ExtraArgs:    "-tickrate 64 -workshop",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("ins"),
		Notes:        []string{"The start map value is a map followed by a game mode, for example \"embassy_coop checkpoint\"."},
	})
	ins.Startup = "export LD_LIBRARY_PATH=/home/container:/home/container/bin:$LD_LIBRARY_PATH; exec " + ins.Startup
	ins.InstallScript = steamclientTo("bin")
	out = append(out, ins)

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-doi", Name: "Day of Infamy", GameTitle: "Day of Infamy",
		Summary: "Day of Infamy WW2 infantry shooter dedicated server via SteamCMD.",
		AppID:   "462310", GameDir: "doi", Map: "bastogne stronghold", MaxPlayers: 32,
		Category: "tactical", Tags: []string{"pvp", "coop", "ww2"},
		Aliases:  []string{"doi", "day of infamy"},
		MemoryMB: 2048, DiskMB: 20480,
		ExtraArgs:    "-tickrate 64 -workshop",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("doi"),
		Notes:        []string{"The start map value is a map followed by a game mode, for example \"bastogne stronghold\"."},
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-fof", Name: "Fistful of Frags", GameTitle: "Fistful of Frags",
		Summary: "Fistful of Frags western shooter dedicated server via SteamCMD.",
		AppID:   "295230", GameDir: "fof", Map: "fof_depot", MaxPlayers: 20,
		Category: "shooter", Tags: []string{"pvp", "western", "free"},
		Aliases:  []string{"fof", "fistful of frags"},
		MemoryMB: 1024, DiskMB: 6144,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("fof"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-zps", Name: "Zombie Panic! Source", GameTitle: "Zombie Panic! Source",
		Summary: "Zombie Panic! Source survivors versus zombies dedicated server via SteamCMD.",
		AppID:   "17505", GameDir: "zps", Map: "zps_deadend", MaxPlayers: 20,
		Category: "shooter", Tags: []string{"pvp", "zombies", "free"},
		Aliases:  []string{"zps", "zombie panic", "zombie panic source"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("zps"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-nucleardawn", Name: "Nuclear Dawn", GameTitle: "Nuclear Dawn",
		Summary: "Nuclear Dawn FPS and RTS hybrid dedicated server via SteamCMD.",
		AppID:   "111710", GameDir: "nucleardawn", Map: "hydro", MaxPlayers: 32,
		Category: "strategy", Tags: []string{"pvp", "rts", "fps"},
		Aliases:  []string{"nd", "nuclear dawn"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("nd"),
	}))

	out = append(out, srcdsLauncher(SourceGame{
		ID: "ndl-dystopia", Name: "Dystopia", GameTitle: "Dystopia",
		Summary: "Dystopia cyberpunk team shooter dedicated server via SteamCMD.",
		AppID:   "17585", GameDir: "dystopia", Map: "dys_broadcast", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "cyberpunk", "free"},
		Aliases:  []string{"dys", "dystopia"},
		MemoryMB: 1024, DiskMB: 8192, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("dys"),
	}, "cd /home/container/bin && exec ./srcds_run.sh -game /home/container/dystopia"))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-empires", Name: "Empires Mod", GameTitle: "Empires",
		Summary: "Empires FPS and RTS hybrid dedicated server via SteamCMD.",
		AppID:   "460040", GameDir: "empires", Map: "con_district402", MaxPlayers: 62,
		Category: "strategy", Tags: []string{"pvp", "rts", "fps", "free"},
		Aliases:  []string{"em", "empires", "empires mod"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("em"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-bb2", Name: "BrainBread 2", GameTitle: "BrainBread 2",
		Summary: "BrainBread 2 co-op zombie shooter dedicated server via SteamCMD.",
		AppID:   "475370", GameDir: "brainbread2", Map: "bba_barracks", MaxPlayers: 20,
		Category: "shooter", Tags: []string{"coop", "zombies"},
		Aliases:  []string{"bb2", "brainbread 2", "brainbread2"},
		MemoryMB: 1024, DiskMB: 12288, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("bb2"),
		Notes:        []string{"LinuxGSM also lists libcurl4-gnutls-dev:i386, which this runtime cannot layer yet. If the server fails to load a libcurl-gnutls library, report it."},
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-codenamecure", Name: "Codename CURE", GameTitle: "Codename CURE",
		Summary: "Codename CURE co-op zombie shooter dedicated server via SteamCMD.",
		AppID:   "383410", GameDir: "cure", Map: "cbe_bunker", MaxPlayers: 6,
		Category: "shooter", Tags: []string{"coop", "zombies", "free"},
		Aliases:  []string{"cc", "codename cure", "cure"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("cc"),
	}))

	out = append(out, srcdsLauncher(SourceGame{
		ID: "ndl-dab", Name: "Double Action: Boogaloo", GameTitle: "Double Action: Boogaloo",
		Summary: "Double Action: Boogaloo stylish action shooter dedicated server via SteamCMD.",
		AppID:   "317800", GameDir: "dab", Map: "da_rooftops", MaxPlayers: 10,
		Category: "shooter", Tags: []string{"pvp", "action", "free"},
		Aliases:  []string{"dab", "double action", "double action boogaloo"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("dab"),
	}, "./dabds.sh"))

	out = append(out, requireSteamOwner(srcdsLauncher(SourceGame{
		ID: "ndl-bladesymphony", Name: "Blade Symphony", GameTitle: "Blade Symphony",
		Summary: "Blade Symphony sword fighting dedicated server via SteamCMD. Needs a Steam account that owns the game.",
		AppID:   "228780", GameDir: "berimbau", Map: "duel_winter", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "melee", "dueling"},
		Aliases:  []string{"bs", "blade symphony", "berimbau"},
		MemoryMB: 1024, DiskMB: 16384, GSLT: true,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("bs"),
	}, "cd /home/container/bin && exec ./srcds_run.sh -game /home/container/berimbau"), "Steam account that owns Blade Symphony"))
	out[len(out)-1].Hint = "Steam account that owns the game required; GSLT only to list publicly"

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-pvkii", Name: "Pirates, Vikings, and Knights II", GameTitle: "Pirates, Vikings, and Knights II",
		Summary: "Pirates, Vikings, and Knights II melee team combat dedicated server via SteamCMD.",
		AppID:   "17575", GameDir: "pvkii", Map: "bt_island", MaxPlayers: 24,
		Category: "shooter", Tags: []string{"pvp", "melee", "free"},
		Aliases:  []string{"pvkii", "pvk2", "pirates vikings knights"},
		MemoryMB: 1024, DiskMB: 12288,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("pvkii"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-iosoccer", Name: "IOSoccer", GameTitle: "IOSoccer",
		Summary: "IOSoccer team football dedicated server via SteamCMD.",
		AppID:   "673990", GameDir: "iosoccer", Map: "8v8_vienna", MaxPlayers: 32,
		Category: "sports", Tags: []string{"pvp", "football", "soccer", "free"},
		Aliases:  []string{"ios", "iosoccer", "soccer"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("ios"),
	}))

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-jbep3", Name: "Jabroni Brawl: Episode 3", GameTitle: "Jabroni Brawl: Episode 3",
		Summary: "Jabroni Brawl: Episode 3 chaotic arena shooter dedicated server via SteamCMD.",
		AppID:   "869800", GameDir: "jbep3", Map: "crossfire", MaxPlayers: 24,
		Category: "shooter", Tags: []string{"pvp", "arena", "free"},
		Aliases:  []string{"jbep3", "jabroni brawl"},
		MemoryMB: 1024, DiskMB: 12288,
		Binary:       "srcds_run.sh",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("jbep3"),
	}))

	tf2c := srcdsTemplate(SourceGame{
		ID: "ndl-tf2c", Name: "Team Fortress 2 Classified", GameTitle: "Team Fortress 2 Classified",
		Summary: "Team Fortress 2 Classified dedicated server via SteamCMD. Installs the TF2 server alongside for shared content.",
		AppID:   "3557020", GameDir: "tf2classified", Map: "4koth_frigid", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "class-based", "free", "modded"},
		Aliases:  []string{"tf2c", "tf2 classified", "tf2 classic", "team fortress 2 classic"},
		MemoryMB: 2048, DiskMB: 51200, GSLT: true,
		Binary:       "srcds.sh",
		ExtraArgs:    "-tf_path /home/container/tf2",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("tf2c"),
		Notes: []string{
			"The Team Fortress 2 dedicated server (app 232250) is installed into tf2/ because TF2 Classified loads its content from there.",
			"LinuxGSM also lists libcurl4-gnutls-dev:i386, which this runtime cannot layer yet.",
		},
	})
	tf2c.InstallScript = tf2BaseInstall
	out = append(out, tf2c)

	out = append(out, srcdsTemplate(SourceGame{
		ID: "ndl-mcv", Name: "Military Conflict: Vietnam", GameTitle: "Military Conflict: Vietnam",
		Summary: "Military Conflict: Vietnam 64-bit Source dedicated server via SteamCMD.",
		AppID:   "1136190", GameDir: "vietnam", Map: "mcv_siege", MaxPlayers: 32,
		Category: "shooter", Tags: []string{"pvp", "vietnam", "military"},
		Aliases:  []string{"mcv", "military conflict vietnam", "vietnam"},
		MemoryMB: 2048, DiskMB: 30720,
		Binary:       "srcds_run_x64",
		ExtraArgs:    "-tickrate 64 -maxplayers_override {{MAX_PLAYERS}} +game_type 0 +game_mode 0 -nobreakpad",
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("mcv"),
	}))

	out = append(out, requireSteamOwner(srcdsTemplate(SourceGame{
		ID: "ndl-actionsource", Name: "Action: Source", GameTitle: "Action: Source",
		Summary: "Action: Source dedicated server via SteamCMD. The dedicated app needs a logged-in Steam account.",
		AppID:   "985050", GameDir: "ahl2", Map: "act_airport", MaxPlayers: 20,
		Category: "shooter", Tags: []string{"pvp", "action", "free"},
		Aliases:  []string{"ahl2", "action source", "action half-life 2"},
		MemoryMB: 1024, DiskMB: 8192,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("ahl2"),
		Notes:        []string{"Action: Source is free on Steam; add it to the account used for installation."},
	}), "Steam account with Action: Source in its library (free)"))

	// GoldSrc (Half-Life engine). App 90 mods set Mod for app_set_config.
	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-cs16", Name: "Counter-Strike 1.6", GameTitle: "Counter-Strike",
		Summary: "Counter-Strike 1.6 dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "cstrike", GameDir: "cstrike", Map: "de_dust2", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "classic", "retro"},
		Aliases:  []string{"cs16", "cs 1.6", "cs1.6", "counter-strike 1.6", "cstrike"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("cs"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))
	out[len(out)-1].InstallScript = steamclientTo("")

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-cscz", Name: "Counter-Strike: Condition Zero", GameTitle: "Counter-Strike: Condition Zero",
		Summary: "Counter-Strike: Condition Zero dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "czero", GameDir: "czero", Map: "de_dust2", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "classic", "retro"},
		Aliases:  []string{"cscz", "czero", "condition zero"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("cscz"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-dod", Name: "Day of Defeat", GameTitle: "Day of Defeat",
		Summary: "Day of Defeat (GoldSrc) dedicated server via SteamCMD.",
		AppID:   "90", Mod: "dod", GameDir: "dod", Map: "dod_Anzio", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "ww2", "classic", "retro"},
		Aliases:  []string{"dod", "day of defeat", "dod 1.3"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("dod"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-tfc", Name: "Team Fortress Classic", GameTitle: "Team Fortress Classic",
		Summary: "Team Fortress Classic dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "tfc", GameDir: "tfc", Map: "dustbowl", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "class-based", "classic", "retro"},
		Aliases:  []string{"tfc", "team fortress classic"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("tfc"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-hldm", Name: "Half-Life: Deathmatch", GameTitle: "Half-Life",
		Summary: "Half-Life multiplayer deathmatch dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", GameDir: "valve", Map: "crossfire", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch", "classic", "retro"},
		Aliases:  []string{"hldm", "hl1", "half-life deathmatch", "hlds", "valve"},
		MemoryMB: 512, DiskMB: 3072,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("hldm"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-opfor", Name: "Half-Life: Opposing Force", GameTitle: "Half-Life: Opposing Force",
		Summary: "Half-Life: Opposing Force multiplayer dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "gearbox", GameDir: "gearbox", Map: "op4_bootcamp", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch", "classic", "retro"},
		Aliases:  []string{"opfor", "op4", "opposing force", "gearbox"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("opfor"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-ricochet", Name: "Ricochet", GameTitle: "Ricochet",
		Summary: "Ricochet disc arena dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "ricochet", GameDir: "ricochet", Map: "rc_arena", MaxPlayers: 16,
		Category: "party", Tags: []string{"pvp", "arena", "classic", "retro"},
		Aliases:  []string{"ricochet", "rc"},
		MemoryMB: 512, DiskMB: 3072,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("ricochet"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-dmc", Name: "Deathmatch Classic", GameTitle: "Deathmatch Classic",
		Summary: "Deathmatch Classic dedicated server (HLDS) via SteamCMD.",
		AppID:   "90", Mod: "dmc", GameDir: "dmc", Map: "dcdm5", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"pvp", "deathmatch", "classic", "retro"},
		Aliases:  []string{"dmc", "deathmatch classic"},
		MemoryMB: 512, DiskMB: 3072,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("dmc"),
		DocsURL:      "https://developer.valvesoftware.com/wiki/Half-Life_Dedicated_Server",
		Notes:        []string{goldsrcNote},
	}))

	// BrainBread: LinuxGSM installs app 90 (cstrike config) and then the
	// community Linux server build from IronOak-Studios/BrainBread releases
	// (lgsm/modules/update_bb.sh, asset matching linuxserver.tar.gz,
	// extracted over the server root). The archive's top folder is
	// brainbread/ (package.py in that repo), so it lands in the game dir.
	bb := goldsrcTemplate(SourceGame{
		ID: "ndl-brainbread", Name: "BrainBread", GameTitle: "BrainBread",
		Summary: "BrainBread co-op zombie Half-Life mod dedicated server: HLDS via SteamCMD plus the IronOak Studios Linux server release.",
		AppID:   "90", Mod: "cstrike", GameDir: "brainbread", Map: "bb_chp4_slaywatch", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"coop", "zombies", "classic", "retro", "modded"},
		Aliases:  []string{"bb", "brainbread", "bb1"},
		MemoryMB: 512, DiskMB: 6144,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("bb"),
		Notes: []string{
			goldsrcNote,
			"After SteamCMD, the latest IronOak-Studios/BrainBread release asset ending in linuxserver.tar.gz is downloaded and extracted over the server folder. Set Mod version to a release tag (for example build-260720) to pin it.",
		},
	})
	bb.Variables = append(bb.Variables, envText("Mod version", "VERSION", "IronOak-Studios/BrainBread release tag, or latest.", "latest", true))
	bb.Install.VersionEnv = "VERSION"
	bb.Install.Downloads = []Download{{Repo: "IronOak-Studios/BrainBread", Asset: `^brainbread-v[0-9.]+(-[0-9.]+)?-linuxserver\.tar\.gz$`}}
	out = append(out, bb)

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-svencoop", Name: "Sven Co-op", GameTitle: "Sven Co-op",
		Summary: "Sven Co-op cooperative Half-Life dedicated server via SteamCMD.",
		AppID:   "276060", GameDir: "svencoop", Map: "svencoop1", MaxPlayers: 16,
		Category: "shooter", Tags: []string{"coop", "free", "classic"},
		Aliases:  []string{"sven", "svencoop", "sven coop", "sven co-op"},
		MemoryMB: 1024, DiskMB: 8192,
		Binary:       "svends_run",
		Dependencies: steamcmdRuntimeDeps("libssl3t64:i386", "zlib1g:i386"),
		SourceRef:    lgsmRef("sven"),
		DocsURL:      "https://wiki.svencoop.com/Server_Configuration",
	}))

	out = append(out, goldsrcTemplate(SourceGame{
		ID: "ndl-basedefense", Name: "Base Defense", GameTitle: "Base Defense",
		Summary: "Base Defense co-op GoldSrc dedicated server via SteamCMD.",
		AppID:   "817300", GameDir: "bdef", Map: "pve_tomb", MaxPlayers: 3,
		Category: "shooter", Tags: []string{"coop", "free"},
		Aliases:  []string{"bd", "base defense", "bdef"},
		MemoryMB: 512, DiskMB: 4096,
		Dependencies: steamcmdRuntimeDeps(),
		SourceRef:    lgsmRef("bd"),
	}))

	return out
}
