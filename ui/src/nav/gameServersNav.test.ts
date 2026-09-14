import { describe, expect, it } from "vitest";
import { buildGameServerNav, gameServerTabList } from "./gameServersNav";

describe("Game Servers capability nav", () => {
  it("hides plugins, mods, workshop, players, and databases unless declared", () => {
    const groups = buildGameServerNav("gs-1", { capabilities: ["console", "files", "backups"] }, "console");
    const labels = groups.flatMap((group) => group.items.map((item) => item.label));
    expect(labels).toContain("Overview");
    expect(labels).toContain("Console");
    expect(labels).toContain("Files");
    expect(labels).toContain("Backups");
    expect(labels).toContain("Diagnostics");
    expect(labels).toContain("Settings");
    expect(labels).not.toContain("Plugins");
    expect(labels).not.toContain("Mods");
    expect(labels).not.toContain("Workshop");
    expect(labels).not.toContain("Players");
    expect(labels).not.toContain("Databases");
    expect(groups.find((group) => group.label === "Operate")?.items.find((item) => item.id === "console")?.current).toBe(true);
  });

  it("shows only the matching content surface", () => {
    const paper = gameServerTabList({ capabilities: ["plugins", "players"] }).map((item) => item.id);
    expect(paper).toContain("plugins");
    expect(paper).toContain("players");
    expect(paper).not.toContain("mods");
    expect(paper).not.toContain("workshop");

    const gmod = gameServerTabList({ capabilities: ["workshop"] }).map((item) => item.id);
    expect(gmod).toContain("workshop");
    expect(gmod).not.toContain("plugins");

    const fabric = gameServerTabList({ capabilities: ["mods", "console"] }).map((item) => item.id);
    expect(fabric).toContain("mods");
    expect(fabric).not.toContain("plugins");
    expect(fabric).not.toContain("players");

    const velocity = gameServerTabList({ capabilities: ["plugins"] }).map((item) => item.id);
    expect(velocity).toContain("plugins");
    expect(velocity).not.toContain("mods");
    expect(velocity).not.toContain("worlds");
  });
});
