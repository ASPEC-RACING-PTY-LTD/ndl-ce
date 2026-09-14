import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";
import type { CatalogueItem } from "../gameservers/types";

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["admin"],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
};

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const baseRoutes: Record<string, { status: number; body?: unknown }> = {
  "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/me": { status: 200, body: admin },
  "/api/v1/nodes": { status: 200, body: { items: [{ id: "node-1", name: "Node A" }] } },
  "/api/v1/events": { status: 200, body: { items: [] } },
  "/api/v1/timeline": { status: 200, body: { items: [] } },
  "/api/v1/alerts": { status: 200, body: { items: [] } },
  "/api/v1/alerts/channels": { status: 200, body: { items: [] } },
  "/api/v1/tasks": { status: 200, body: { items: [] } },
  "/api/v1/storage/pools": { status: 200, body: { items: [] } },
  "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
  "/api/v1/stacks": { status: 200, body: { items: [] } },
  "/api/v1/cluster": { status: 200, body: { id: "cluster-1", name: "local", nodes: [] } },
  "/api/v1/cluster/ha": {
    status: 200,
    body: { mode: "single-writer", writer: true, replica_status: "not_configured", fencing_mode: "operator", multi_master: false },
  },
  "/api/v1/cluster/wg": { status: 200, body: { items: [], nodes: [] } },
  "/api/v1/features": {
    status: 200,
    body: {
      items: [
        {
          id: "gameservers",
          title: "Game Servers",
          enabled: true,
          core: false,
          package_status: "not_configured",
          runtime_status: "not_started",
          starts_runtime: false,
          kubelet_started: false,
          workload_count: 0,
          software_only: true,
        },
      ],
    },
  },
};

const catalogue: CatalogueItem[] = [
  { id: "ndl-minecraft-paper", name: "Minecraft Paper", game: "minecraft", family: "minecraft", builtin: true, aliases: ["mc", "paper"], runtime_kind: "Java" },
  { id: "ndl-zomboid", name: "Project Zomboid", game: "zomboid", family: "steam", builtin: true, aliases: ["pz"], runtime_kind: "SteamCMD" },
  { id: "ndl-cs2", name: "Counter-Strike 2", game: "cs2", family: "source", builtin: true, aliases: ["cs2"], runtime_kind: "SteamCMD" },
  { id: "ndl-gmod", name: "Garry's Mod", game: "gmod", family: "source", builtin: true, aliases: ["gmod"], runtime_kind: "SteamCMD" },
  { id: "ndl-fivem", name: "FiveM FXServer", game: "fivem", family: "fivem", builtin: true, aliases: ["fivem"], runtime_kind: "Standalone", hint: "Cfx.re key required to start" },
  { id: "ndl-terraria", name: "Terraria", game: "terraria", family: "terraria", builtin: true, runtime_kind: "Standalone" },
  { id: "ndl-valheim", name: "Valheim", game: "valheim", family: "steam", builtin: true, aliases: ["vh"], runtime_kind: "SteamCMD" },
];

const zomboidTemplate = {
  id: "ndl-zomboid",
  name: "Project Zomboid",
  game: "zomboid",
  family: "steam",
  default_cpus: 2,
  default_memory_mb: 4096,
  default_disk_mb: 16384,
  start_requires: [],
  variables: [
    { name: "Server name", env: "SERVER_NAME", default: "servertest", viewable: true, required: true },
    { name: "Admin password", env: "ADMIN_PASSWORD", default: "changeme", viewable: true, required: true, secret: true, field_type: "password" },
  ],
};

function mockApi(routes: Record<string, { status: number; body?: unknown }> = {}) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const parsed = new URL(url, "http://localhost");
    const hit = routes[parsed.pathname] ?? baseRoutes[parsed.pathname];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${parsed.pathname}` });
    }
    return jsonResponse(hit.status, hit.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Create Game Server catalogue", () => {
  it("searches aliases, keeps sidebar server search separate, and advances the wizard", async () => {
    window.history.replaceState({}, "", "/game-servers/create");
    mockApi({
      "/api/v1/game-servers": { status: 200, body: { items: [{ id: "gs-live", name: "My Minecraft Server", game: "minecraft", status: "running", template_name: "Paper", cpus: 2, memory_bytes: 1, disk_bytes: 1 }] } },
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: catalogue } },
      "/api/v1/game-servers/templates": { status: 200, body: zomboidTemplate },
    });
    render(<App />);

    expect(await screen.findByRole("heading", { name: /create game server/i })).toBeVisible();
    const templateSearch = screen.getByRole("searchbox", { name: /search available game templates/i });
    const serverSearch = within(screen.getByRole("navigation", { name: /^game servers$/i })).getByRole("searchbox", { name: /search game servers/i });
    expect(screen.getByRole("button", { name: /project zomboid/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /minecraft paper/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /refresh catalogue/i })).toBeVisible();
    expect(screen.getByLabelText(/egg json url/i)).toBeVisible();

    fireEvent.change(templateSearch, { target: { value: "pz" } });
    expect(screen.getByRole("button", { name: /project zomboid/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /minecraft paper/i })).toBeNull();
    expect(within(screen.getByRole("navigation", { name: /^game servers$/i })).getByRole("treeitem", { name: /my minecraft server/i })).toBeVisible();

    fireEvent.change(serverSearch, { target: { value: "valheim" } });
    expect(screen.getByRole("button", { name: /project zomboid/i })).toBeVisible();
    expect(within(screen.getByRole("navigation", { name: /^game servers$/i })).queryByRole("treeitem", { name: /my minecraft server/i })).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: /^all$/i }));
    fireEvent.change(templateSearch, { target: { value: "" } });
    fireEvent.click(screen.getByRole("tab", { name: /^standalone$/i }));
    expect(screen.getByRole("button", { name: /fivem fxserver/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /project zomboid/i })).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: /^all$/i }));
    fireEvent.change(templateSearch, { target: { value: "pz" } });
    fireEvent.click(screen.getByRole("button", { name: /project zomboid/i }));
    expect(await screen.findByLabelText(/admin password/i)).toBeVisible();
    expect(screen.getAllByLabelText(/server name/i).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: /^continue$/i }));
    expect(await screen.findByRole("combobox")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^review$/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /install server/i })).toBeVisible();
    });
    expect(screen.getByRole("heading", { name: /project zomboid/i })).toBeVisible();
  });

  it("redirects the old create bookmark", async () => {
    window.history.replaceState({}, "", "/workloads/game-servers/create");
    mockApi({
      "/api/v1/game-servers": { status: 200, body: { items: [] } },
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: catalogue } },
    });
    render(<App />);
    await waitFor(() => {
      expect(window.location.pathname).toBe("/game-servers/create");
    });
    expect(await screen.findByRole("heading", { name: /create game server/i })).toBeVisible();
  });
});
