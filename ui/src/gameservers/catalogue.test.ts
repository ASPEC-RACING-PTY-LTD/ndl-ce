import { describe, expect, it } from "vitest";
import { catalogueMark, filterCatalogue, showCreateVariable, type CatalogueGroup } from "./catalogue";
import type { CatalogueItem } from "./types";

const items: CatalogueItem[] = [
  { id: "ndl-minecraft-paper", name: "Minecraft Paper", game: "minecraft", family: "minecraft", builtin: true, aliases: ["mc", "paper"], runtime_kind: "Java", tags: ["plugins"] },
  { id: "ndl-zomboid", name: "Project Zomboid", game: "zomboid", family: "steam", builtin: true, aliases: ["pz"], runtime_kind: "SteamCMD" },
  { id: "ndl-cs2", name: "Counter-Strike 2", game: "cs2", family: "source", builtin: true, aliases: ["cs2"], runtime_kind: "SteamCMD" },
  { id: "ndl-gmod", name: "Garry's Mod", game: "gmod", family: "source", builtin: true, aliases: ["gmod"], runtime_kind: "SteamCMD" },
  { id: "ndl-fivem", name: "FiveM FXServer", game: "fivem", family: "fivem", builtin: true, aliases: ["fivem"], runtime_kind: "Standalone", hint: "Cfx.re key required to start" },
  { id: "ndl-terraria", name: "Terraria", game: "terraria", family: "terraria", builtin: true, runtime_kind: "Standalone" },
  { id: "remote-egg", name: "Imported Egg", game: "custom", builtin: false, runtime_kind: "Imported" },
];

describe("catalogue search and groups", () => {
  it("matches names, aliases, and runtime types", () => {
    expect(filterCatalogue(items, "mc").map((item) => item.id)).toContain("ndl-minecraft-paper");
    expect(filterCatalogue(items, "pz").map((item) => item.id)).toEqual(["ndl-zomboid"]);
    expect(filterCatalogue(items, "cs2").map((item) => item.id)).toEqual(["ndl-cs2"]);
    expect(filterCatalogue(items, "gmod").map((item) => item.id)).toEqual(["ndl-gmod"]);
    expect(filterCatalogue(items, "fivem").map((item) => item.id)).toEqual(["ndl-fivem"]);
    expect(filterCatalogue(items, "SteamCMD").map((item) => item.id)).toEqual(["ndl-zomboid", "ndl-cs2", "ndl-gmod"]);
  });

  it("filters groups without mixing deployed-server search", () => {
    const groups: Record<CatalogueGroup, string[]> = {
      all: filterCatalogue(items, "", "all").map((item) => item.id),
      builtin: filterCatalogue(items, "", "builtin").map((item) => item.id),
      steamcmd: filterCatalogue(items, "", "steamcmd").map((item) => item.id),
      standalone: filterCatalogue(items, "", "standalone").map((item) => item.id),
      minecraft: filterCatalogue(items, "", "minecraft").map((item) => item.id),
    };
    expect(groups.builtin).not.toContain("remote-egg");
    expect(groups.steamcmd).toEqual(["ndl-zomboid", "ndl-cs2", "ndl-gmod"]);
    expect(groups.standalone).toEqual(["ndl-fivem", "ndl-terraria"]);
    expect(groups.minecraft).toEqual(["ndl-minecraft-paper"]);
    expect(catalogueMark(items[0])).toBe("M");
  });

  it("shows start-required secrets during create", () => {
    expect(showCreateVariable("CLUSTER_TOKEN", false, false, ["CLUSTER_TOKEN"])).toBe(true);
    expect(showCreateVariable("STEAM_PASS", false, false, [])).toBe(false);
    expect(showCreateVariable("EULA", true, true, [])).toBe(true);
  });
});
