package gameserver

// Capability IDs drive UI and API surfaces. Templates declare these; the UI
// must not hardcode game names to show tabs.
const (
	CapConsole    = "console"
	CapFiles      = "files"
	CapConfig     = "config"
	CapStartup    = "startup"
	CapNetwork    = "network"
	CapResources  = "resources"
	CapBackups    = "backups"
	CapSchedules  = "schedules"
	CapMods       = "mods"
	CapPlugins    = "plugins"
	CapWorlds     = "worlds"
	CapPlayers    = "players"
	CapDatabases  = "databases"
	CapEULA       = "eula"
	CapRCON       = "rcon"
	CapSteamCMD   = "steamcmd"
	CapJava       = "java"
	CapLicenseKey = "license_key"
	CapWorkshop   = "workshop"
	CapQueries    = "query"
)

// AllCapabilities is the deny-by-default catalogue of known capability IDs.
func AllCapabilities() []string {
	return []string{
		CapConsole, CapFiles, CapConfig, CapStartup, CapNetwork, CapResources,
		CapBackups, CapSchedules, CapMods, CapPlugins, CapWorlds, CapPlayers,
		CapDatabases, CapEULA, CapRCON, CapSteamCMD, CapJava, CapLicenseKey,
		CapWorkshop, CapQueries,
	}
}

func knownCapability(id string) bool {
	for _, c := range AllCapabilities() {
		if c == id {
			return true
		}
	}
	return false
}

func normalizeCapabilities(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range in {
		id := canon(raw)
		if id == "" || !knownCapability(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
