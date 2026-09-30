import { describe, expect, it } from "vitest";
import {
  catalogueCategories,
  catalogueInstallMethods,
  catalogueMark,
  catalogueMatches,
  catalogueScore,
  defaultImageLabel,
  filterCatalogue,
  groupCatalogue,
  needsCredentials,
  queryCatalogue,
  requirementBadge,
  showCreateVariable,
  type CatalogueGroup,
} from "./catalogue";
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

const rich: CatalogueItem[] = [
  { id: "ndl-space", name: "Space Engineers", game: "space", builtin: true, summary: "Trusted builds in orbit", category: "sandbox", install_method: "SteamCMD", engine: "vrage" },
  { id: "ndl-rusted", name: "Rusted Warfare", game: "rustedwarfare", builtin: true, category: "strategy", install_method: "Official download", architectures: ["amd64", "arm64"] },
  { id: "ndl-rust", name: "Rust", game: "rust", builtin: true, aliases: ["rust"], category: "survival", install_method: "SteamCMD" },
  {
    id: "ndl-css",
    name: "Counter-Strike: Source",
    game: "css",
    game_title: "Counter-Strike: Source",
    builtin: true,
    category: "shooter",
    engine: "source",
    install_method: "SteamCMD",
    requirements: [{ kind: "gslt", stage: "optional", label: "Game Server Login Token" }],
  },
  {
    id: "ndl-hl",
    name: "Half-Life Deathmatch",
    game: "hldm",
    builtin: true,
    category: "shooter",
    engine: "goldsrc",
    install_method: "SteamCMD",
    requirements: [{ kind: "steam_account", stage: "install", label: "Steam account that owns the game" }],
  },
  { id: "ndl-mc-paper", name: "Paper", game: "minecraft", game_title: "Minecraft: Java Edition", family: "minecraft", builtin: true, category: "minecraft", install_method: "PaperMC API", architectures: ["amd64", "arm64"], requirements: [{ kind: "eula", stage: "start", label: "Minecraft EULA" }] },
  { id: "ndl-mc-fabric", name: "Fabric", game: "minecraft", game_title: "Minecraft: Java Edition", family: "minecraft", builtin: true, category: "minecraft", install_method: "Mod loader installer", architectures: ["amd64", "arm64"] },
  { id: "ndl-fivem", name: "FiveM FXServer", game: "fivem", builtin: true, category: "roleplay", install_method: "Official download", requirements: [{ kind: "license_key", stage: "start", label: "Cfx.re licence key" }] },
  { id: "egg-1", name: "Imported Thing", game: "custom", builtin: false, category: "" },
];

const ids = (list: CatalogueItem[]) => list.map((item) => item.id);

describe("catalogue ranking", () => {
  it("ranks exact alias or name first, then prefix, then substring", () => {
    expect(ids(filterCatalogue(rich, "rust"))).toEqual(["ndl-rust", "ndl-rusted", "ndl-space"]);
    expect(catalogueScore(rich[2], "rust")).toBeGreaterThan(catalogueScore(rich[1], "rust"));
    expect(catalogueScore(rich[1], "rust")).toBeGreaterThan(catalogueScore(rich[0], "rust"));
    expect(catalogueScore(rich[0], "zzz")).toBe(0);
  });

  it("searches game title, engine, install method, platform, and requirements", () => {
    expect(ids(filterCatalogue(rich, "Minecraft: Java"))).toEqual(["ndl-mc-paper", "ndl-mc-fabric"]);
    expect(ids(filterCatalogue(rich, "goldsrc"))).toEqual(["ndl-hl"]);
    expect(ids(filterCatalogue(rich, "papermc"))).toEqual(["ndl-mc-paper"]);
    expect(ids(filterCatalogue(rich, "arm64"))).toEqual(["ndl-rusted", "ndl-mc-paper", "ndl-mc-fabric"]);
    expect(ids(filterCatalogue(rich, "gslt"))).toEqual(["ndl-css"]);
    expect(ids(filterCatalogue(rich, "steam account"))).toEqual(["ndl-hl"]);
    expect(ids(filterCatalogue(rich, "licence key"))).toEqual(["ndl-fivem"]);
    expect(ids(filterCatalogue(rich, "shooter"))).toEqual(["ndl-css", "ndl-hl"]);
    expect(catalogueMatches(rich[3], "source")).toBe(true);
  });
});

describe("catalogue filters", () => {
  it("filters by category, method, platform, and credentials", () => {
    expect(ids(queryCatalogue(rich, { category: "shooter" }))).toEqual(["ndl-css", "ndl-hl"]);
    expect(ids(queryCatalogue(rich, { category: "imported" }))).toEqual(["egg-1"]);
    expect(ids(queryCatalogue(rich, { method: "Official download" }))).toEqual(["ndl-rusted", "ndl-fivem"]);
    expect(ids(queryCatalogue(rich, { arch: "arm64" }))).toEqual(["ndl-rusted", "ndl-mc-paper", "ndl-mc-fabric"]);
    const noCreds = ids(queryCatalogue(rich, { noCredentials: true }));
    expect(noCreds).not.toContain("ndl-hl");
    expect(noCreds).not.toContain("ndl-fivem");
    expect(noCreds).toContain("ndl-css");
    expect(noCreds).toContain("ndl-mc-paper");
    expect(needsCredentials(rich[4])).toBe(true);
    expect(needsCredentials({ id: "x", name: "x", game: "x", capabilities: ["license_key"] })).toBe(true);
  });

  it("supports favourites, recents, and A-Z sort", () => {
    expect(ids(queryCatalogue(rich, { category: "favorites", favorites: ["ndl-hl", "ndl-rust"] }))).toEqual(["ndl-rust", "ndl-hl"]);
    expect(ids(queryCatalogue(rich, { category: "recent", recents: ["ndl-hl", "ndl-rust"] }))).toEqual(["ndl-hl", "ndl-rust"]);
    const az = queryCatalogue(rich, { sort: "az" }).map((item) => item.name);
    expect(az).toEqual([...az].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" })));
    expect(az[0]).toBe("Counter-Strike: Source");
  });

  it("derives category chips with counts", () => {
    const chips = catalogueCategories(rich, { favorites: ["ndl-rust"], recents: [] });
    const byId = Object.fromEntries(chips.map((c) => [c.id, c.count]));
    expect(chips.slice(0, 3).map((c) => c.label)).toEqual(["All", "Favourites", "Recent"]);
    expect(byId).toMatchObject({ all: rich.length, favorites: 1, recent: 0, shooter: 2, minecraft: 2, imported: 1 });
    const searched = Object.fromEntries(catalogueCategories(rich, { q: "arm64" }).map((c) => [c.id, c.count]));
    expect(searched).toMatchObject({ all: 3, minecraft: 2, strategy: 1, shooter: 0 });
    expect(catalogueInstallMethods(rich)).toContain("PaperMC API");
  });

  it("groups templates by game title", () => {
    const entries = groupCatalogue(rich);
    const group = entries.find((e) => e.kind === "group");
    expect(group).toMatchObject({ kind: "group", title: "Minecraft: Java Edition" });
    expect(group && group.kind === "group" ? ids(group.items) : []).toEqual(["ndl-mc-paper", "ndl-mc-fabric"]);
    // Counter-Strike: Source has a game_title but only one template, so it stays a card.
    expect(entries.find((e) => e.kind === "single" && e.item.id === "ndl-css")).toBeTruthy();
    expect(entries).toHaveLength(rich.length - 1);
  });

  it("labels requirements and picks the default runtime image", () => {
    expect(requirementBadge({ kind: "gslt", stage: "optional", label: "x" })).toBe("GSLT optional");
    expect(requirementBadge({ kind: "license_key", stage: "start", label: "x" })).toBe("Licence key");
    expect(requirementBadge({ kind: "steam_account", stage: "install", label: "x" })).toBe("Steam account");
    expect(defaultImageLabel({ "Java 21": "img:21", "Java 17": "img:17" }, "img:17")).toBe("Java 17");
    expect(defaultImageLabel({ "Java 21": "img:21" }, "other")).toBe("Java 21");
    expect(defaultImageLabel(undefined, "x")).toBe("");
  });
});
