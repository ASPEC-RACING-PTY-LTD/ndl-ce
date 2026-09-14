package gameserver

import (
	"strings"
	"testing"
)

func TestCatalogueSearchAliasesAndRuntime(t *testing.T) {
	items := BuiltinCatalogue()
	if len(items) < 24 {
		t.Fatalf("catalogue %d", len(items))
	}
	cases := []struct {
		q    string
		want string
	}{
		{"mc", "ndl-minecraft-paper"},
		{"pz", "ndl-zomboid"},
		{"cs2", "ndl-cs2"},
		{"gmod", "ndl-gmod"},
		{"fivem", "ndl-fivem"},
		{"steamcmd", "ndl-valheim"},
		{"java", "ndl-minecraft-paper"},
		{"standalone", "ndl-terraria"},
	}
	for _, c := range cases {
		got := MatchCatalogue(items, c.q)
		if !catalogueHas(got, c.want) {
			t.Fatalf("%q did not include %s (%d hits)", c.q, c.want, len(got))
		}
	}
	if n := len(MatchCatalogue(items, "definitely-not-a-game")); n != 0 {
		t.Fatalf("unknown query matched %d", n)
	}
}

func TestCatalogueGroups(t *testing.T) {
	items := BuiltinCatalogue()
	mc := FilterCatalogue(items, "minecraft")
	if len(mc) < 5 {
		t.Fatalf("minecraft group %d", len(mc))
	}
	for _, item := range mc {
		if item.Family != "minecraft" {
			t.Fatalf("minecraft group leaked %s", item.ID)
		}
	}
	steam := FilterCatalogue(items, "steamcmd")
	if len(steam) < 8 {
		t.Fatalf("steamcmd group %d", len(steam))
	}
	standalone := FilterCatalogue(items, "standalone")
	if !catalogueHas(standalone, "ndl-terraria") || !catalogueHas(standalone, "ndl-factorio") || !catalogueHas(standalone, "ndl-fivem") {
		t.Fatalf("standalone %#v", idsOf(standalone))
	}
	if catalogueHas(standalone, "ndl-valheim") {
		t.Fatal("valheim is SteamCMD, not standalone")
	}
	if n := len(FilterCatalogue(items, "builtin")); n != len(items) {
		t.Fatalf("builtin %d vs %d", n, len(items))
	}
}

func TestInstallPlansAndStartupExpand(t *testing.T) {
	paper, _ := templateByID("ndl-minecraft-paper")
	got := ExpandStartup(paper.Startup, map[string]string{"SERVER_JARFILE": "server.jar"}, 1536, 25565)
	if !containsAll(got, "1536", "server.jar") {
		t.Fatalf("paper startup %s", got)
	}
	vanilla, _ := templateByID("ndl-minecraft-vanilla")
	script, img := installPlan(vanilla)
	if script == "" || img == "" || vanilla.DefaultPorts[0].ContainerPort != 25565 {
		t.Fatalf("vanilla plan")
	}
	cs2, _ := templateByID("ndl-cs2")
	got = ExpandStartup(cs2.Startup, map[string]string{"MAP": "de_dust2", "GAME_TYPE": "0", "GAME_MODE": "1", "STEAM_TOKEN": ""}, 4096, 27015)
	if !containsAll(got, "de_dust2", "27015") {
		t.Fatalf("cs2 startup %s", got)
	}
	dst, ok := templateByID("ndl-dst")
	if !ok || len(dst.StartRequires) != 1 || dst.StartRequires[0] != "CLUSTER_TOKEN" {
		t.Fatalf("dst requires %+v", dst.StartRequires)
	}
	fivem, _ := templateByID("ndl-fivem")
	if !hasCap(fivem.Capabilities, CapLicenseKey) {
		t.Fatal("fivem lost license capability")
	}
}

func TestFirstStableVelocityVersion(t *testing.T) {
	if got := firstStableVersion([]string{"4.1.2-SNAPSHOT", "4.1.1", "3.5.1"}); got != "4.1.1" {
		t.Fatalf("got %q", got)
	}
}

func TestCapabilityHonestyForNewTemplates(t *testing.T) {
	fabric, _ := templateByID("ndl-minecraft-fabric")
	if !hasCap(fabric.Capabilities, CapMods) || hasCap(fabric.Capabilities, CapPlugins) || hasCap(fabric.Capabilities, CapPlayers) {
		t.Fatalf("fabric caps %v", fabric.Capabilities)
	}
	velocity, _ := templateByID("ndl-velocity")
	if !hasCap(velocity.Capabilities, CapPlugins) || hasCap(velocity.Capabilities, CapWorlds) || hasCap(velocity.Capabilities, CapEULA) {
		t.Fatalf("velocity caps %v", velocity.Capabilities)
	}
	paper, _ := templateByID("ndl-minecraft-paper")
	if !hasCap(paper.Capabilities, CapPlugins) || !hasCap(paper.Capabilities, CapPlayers) {
		t.Fatalf("paper caps %v", paper.Capabilities)
	}
}

func catalogueHas(items []CatalogueItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func idsOf(items []CatalogueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
