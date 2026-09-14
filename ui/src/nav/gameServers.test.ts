import { describe, expect, it } from "vitest";
import {
  gameServerCrumbs,
  gameServerHref,
  gameServerIdFromPath,
  gameServerSectionFromPath,
  gameServersHomeFilter,
  isGameServersContext,
  legacyGameServersRedirect,
} from "./gameServers";
import { isWorkloadsContext } from "./match";

describe("Game Servers routes", () => {
  it("treats /game-servers as its own context, not Workloads", () => {
    expect(isGameServersContext("/game-servers")).toBe(true);
    expect(isGameServersContext("/game-servers/create")).toBe(true);
    expect(isGameServersContext("/game-servers/abc/console")).toBe(true);
    expect(isGameServersContext("/workloads")).toBe(false);
    expect(isWorkloadsContext("/game-servers")).toBe(false);
    expect(isWorkloadsContext("/game-servers/abc")).toBe(false);
    expect(isWorkloadsContext("/workloads")).toBe(true);
    expect(isWorkloadsContext("/workloads/game-servers")).toBe(false);
  });

  it("parses server id and capability sections", () => {
    expect(gameServerIdFromPath("/game-servers")).toBeNull();
    expect(gameServerIdFromPath("/game-servers/create")).toBeNull();
    expect(gameServerIdFromPath("/game-servers/catalogue")).toBeNull();
    expect(gameServerIdFromPath("/game-servers/favourites")).toBeNull();
    expect(gameServerIdFromPath("/game-servers/gs-1")).toBe("gs-1");
    expect(gameServerSectionFromPath("/game-servers/gs-1")).toBe("overview");
    expect(gameServerSectionFromPath("/game-servers/gs-1/console")).toBe("console");
    expect(gameServerSectionFromPath("/game-servers/gs-1/notes")).toBe("settings");
    expect(gameServerHref("gs-1")).toBe("/game-servers/gs-1");
    expect(gameServerHref("gs-1", "files")).toBe("/game-servers/gs-1/files");
    expect(gameServersHomeFilter("/game-servers/favourites")).toBe("favorites");
  });

  it("redirects old Workloads-prefixed bookmarks", () => {
    expect(legacyGameServersRedirect("/workloads/game-servers")).toBe("/game-servers");
    expect(legacyGameServersRedirect("/workloads/game-servers/create")).toBe("/game-servers/create");
    expect(legacyGameServersRedirect("/workloads/game-servers/gs-1")).toBe("/game-servers/gs-1");
    expect(legacyGameServersRedirect("/workloads/game-servers/gs-1", "?tab=console")).toBe("/game-servers/gs-1/console");
    expect(legacyGameServersRedirect("/workloads")).toBeNull();
  });

  it("keeps breadcrumbs under Game Servers", () => {
    expect(gameServerCrumbs("/game-servers/gs-1/console")).toEqual([
      { href: "/game-servers", label: "Game Servers" },
      { href: "/game-servers/gs-1", label: "Server" },
      { href: "/game-servers/gs-1/console", label: "Console" },
    ]);
  });
});
