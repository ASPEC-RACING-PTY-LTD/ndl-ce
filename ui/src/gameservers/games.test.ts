import { describe, expect, it } from "vitest";
import { gameCategories, gameFavoriteKey, groupGames, isFavoriteGame, monogram, queryGames, sharedRequirements, variantSections } from "./games";
import type { CatalogueItem } from "./types";

const items: CatalogueItem[] = [
  { id: "paper", name: "Minecraft Paper", game: "minecraft", game_title: "Minecraft: Java Edition", category: "minecraft", aliases: ["paper"], logo_url: "https://avatars.githubusercontent.com/PaperMC?s=160", logo_kind: "icon" },
  { id: "velocity", name: "Velocity", game: "minecraft", game_title: "Minecraft: Java Edition", category: "proxy", logo_url: "https://avatars.githubusercontent.com/PaperMC?s=160", logo_kind: "icon" },
  { id: "rust", name: "Rust", game: "rust", game_title: "Rust", category: "survival", game_logo_url: "https://cdn.cloudflare.steamstatic.com/steam/apps/252490/header.jpg", game_logo_kind: "banner", requirements: [{ kind: "gslt", stage: "optional", label: "GSLT" }] },
  { id: "rust-oxide", name: "Rust (Oxide)", game: "rust", game_title: "Rust", category: "survival", game_logo_url: "https://cdn.cloudflare.steamstatic.com/steam/apps/252490/header.jpg", game_logo_kind: "banner" },
  { id: "dayz", name: "DayZ", game: "dayz", game_title: "DayZ", category: "survival", requirements: [{ kind: "steam_account", stage: "install", label: "Owning account" }] },
  { id: "tshock", name: "TShock", game: "terraria", game_title: "Terraria", category: "sandbox", logo_url: "https://avatars.githubusercontent.com/Pryaxis?s=160", logo_kind: "icon" },
];

describe("game catalogue", () => {
  it("groups templates by game and keeps game artwork", () => {
    const games = groupGames(items);
    expect(games.map((g) => [g.title, g.items.length])).toEqual([
      ["Minecraft: Java Edition", 2],
      ["Rust", 2],
      ["DayZ", 1],
      ["Terraria", 1],
    ]);
    expect(games[1].logoKind).toBe("banner");
    // A single-template game with no art of its own borrows its template mark.
    expect(games[3].logoUrl).toContain("Pryaxis");
    // A multi-template game never borrows one distribution's mark.
    expect(games[0].logoUrl).toBeUndefined();
  });

  it("matches games through any server type and orders by best match", () => {
    const hits = queryGames(items, { q: "paper" });
    expect(hits.map((g) => g.title)).toEqual(["Minecraft: Java Edition"]);
    expect(hits[0].matched.map((i) => i.id)).toEqual(["paper"]);
    expect(queryGames(items, { noCredentials: true }).map((g) => g.title)).not.toContain("DayZ");
  });

  it("counts games per category, including games that span categories", () => {
    const chips = Object.fromEntries(gameCategories(items, {}).map((c) => [c.id, c.count]));
    expect(chips.all).toBe(4);
    expect(chips.survival).toBe(2);
    expect(chips.minecraft).toBe(1);
    expect(chips.proxy).toBe(1);
    expect(queryGames(items, { category: "proxy" }).map((g) => g.title)).toEqual(["Minecraft: Java Edition"]);
  });

  it("treats a game as favourite through its own key or any server type", () => {
    const [mc, rust] = groupGames(items);
    expect(isFavoriteGame(mc, new Set([gameFavoriteKey("Minecraft: Java Edition")]))).toBe(true);
    expect(isFavoriteGame(rust, new Set(["rust-oxide"]))).toBe(true);
    expect(isFavoriteGame(rust, new Set(["paper"]))).toBe(false);
    expect(queryGames(items, { category: "favorites", favorites: ["rust-oxide"] }).map((g) => g.title)).toEqual(["Rust"]);
    expect(queryGames(items, { category: "recent", recents: ["tshock", "velocity"] }).map((g) => g.title)).toEqual(["Terraria", "Minecraft: Java Edition"]);
  });

  it("only shows requirements every server type shares, and splits proxies out", () => {
    const [mc, rust, dayz] = groupGames(items);
    expect(sharedRequirements(rust)).toEqual([]);
    expect(sharedRequirements(dayz).map((r) => r.kind)).toEqual(["steam_account"]);
    expect(variantSections(mc.items).map((s) => [s.label, s.items.length])).toEqual([
      ["Servers", 1],
      ["Proxies and bridges", 1],
    ]);
    expect(variantSections(rust.items)).toHaveLength(1);
  });

  it("builds readable monograms", () => {
    expect(monogram("Minecraft: Java Edition")).toBe("MJ");
    expect(monogram("Rust")).toBe("RU");
    expect(monogram("7 Days to Die")).toBe("7D");
  });
});
