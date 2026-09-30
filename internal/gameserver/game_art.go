package gameserver

import (
	"regexp"
	"strings"
)

// Artwork shown in the catalogue. Images are loaded by the browser from the
// publisher's own CDN; nothing is bundled or proxied. The UI falls back to a
// monogram when an image cannot load (offline control planes, blocked CDNs).
//
// Every Steam ID below is the game's STORE app (not the dedicated server
// app) and was confirmed against its store.steampowered.com/app/<id> page.
// Every GitHub login is the organisation that owns the project and whose
// avatar is the project's own mark (checked by eye; personal avatars and
// placeholder avatars were rejected).

// GameArt is the artwork source for one game (keyed by GameTitle).
type GameArt struct {
	SteamAppID string
	GitHub     string
}

// Logo kinds tell the UI how to lay the image out.
const (
	LogoBanner = "banner" // wide store header, fills the tile
	LogoIcon   = "icon"   // square mark, centred on a tinted tile
)

var (
	steamStoreIDPattern = regexp.MustCompile(`^[0-9]{1,8}$`)
	githubLoginPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

// variantArt gives distributions their own mark (Paper vs Fabric vs Forge)
// on the server-type chooser. Keyed by template ID, value is a GitHub login.
var variantArt = map[string]string{
	"ndl-minecraft-paper":         "PaperMC",
	"ndl-minecraft-folia":         "PaperMC",
	"ndl-velocity":                "PaperMC",
	"ndl-minecraft-purpur":        "PurpurMC",
	"ndl-minecraft-fabric":        "FabricMC",
	"ndl-minecraft-quilt":         "QuiltMC",
	"ndl-minecraft-neoforge":      "neoforged",
	"ndl-minecraft-forge":         "MinecraftForge",
	"ndl-minecraft-spongevanilla": "SpongePowered",
	"ndl-minecraft-spigot":        "SpigotMC",
	"ndl-bungeecord":              "SpigotMC",
	"ndl-minecraft-leaf":          "Winds-Studio",
	"ndl-minecraft-modrinth-pack": "modrinth",
	"ndl-cuberite":                "cuberite",
	"ndl-gate":                    "minekube",
	"ndl-viaproxy":                "ViaVersion",
	"ndl-geyser-standalone":       "GeyserMC",
	"ndl-powernukkitx":            "PowerNukkitX",
	"ndl-allay":                   "AllayMC",
	"ndl-waterdog-pe":             "WaterdogPE",
	"ndl-fivem":                   "citizenfx",
	"ndl-redm":                    "citizenfx",
	"ndl-beammp":                  "BeamMP",
	"ndl-tshock":                  "Pryaxis",
	"ndl-tmodloader":              "tModLoader",
	"ndl-impostor":                "Impostor",
	"ndl-openmp":                  "openmultiplayer",
	"ndl-rust-oxide":              "OxideMod",
	"ndl-7dtd-oxide":              "OxideMod",
	"ndl-rust-carbon":             "CarbonCommunity",
	"ndl-valheim-bepinex":         "BepInEx",
	"ndl-scpsl-exiled":            "ExMod-Team",
}

func steamHeaderURL(appID string) string {
	return "https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/" + appID + "/header.jpg"
}

func githubAvatarURL(login string) string {
	return "https://avatars.githubusercontent.com/" + login + "?s=160"
}

func (a GameArt) url() (string, string) {
	switch {
	case a.SteamAppID != "":
		return steamHeaderURL(a.SteamAppID), LogoBanner
	case a.GitHub != "":
		return githubAvatarURL(a.GitHub), LogoIcon
	}
	return "", ""
}

// GameLogo is the artwork for a game title.
func GameLogo(title string) (string, string) {
	if art, ok := gameArt[strings.TrimSpace(title)]; ok {
		return art.url()
	}
	return "", ""
}

// TemplateLogo is the artwork for one template: its distribution mark when
// it has one, otherwise the game's artwork.
func TemplateLogo(t Template) (string, string) {
	if login, ok := variantArt[t.ID]; ok {
		return githubAvatarURL(login), LogoIcon
	}
	return GameLogo(firstNonEmpty(t.GameTitle, t.Name))
}
