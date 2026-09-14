import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

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
  "/api/v1/nodes": { status: 200, body: { items: [] } },
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
  "/api/v1/features": { status: 200, body: { items: [] } },
};

const gameserversOn = {
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
};

const paper = {
  id: "gs-mc",
  name: "My Minecraft Server",
  status: "running",
  template_id: "ndl-minecraft-paper",
  template_name: "Paper",
  game: "minecraft",
  family: "minecraft",
  cpus: 2,
  memory_bytes: 2147483648,
  disk_bytes: 8589934592,
  capabilities: ["console", "files", "config", "startup", "network", "resources", "backups", "schedules", "plugins", "players"],
};

function mockApi(routes: Record<string, { status: number; body?: unknown }>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const hit = routes[path] ?? baseRoutes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
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

describe("Game Servers home", () => {
  it("renders the empty state when the feature is enabled", async () => {
    window.history.replaceState({}, "", "/game-servers");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [gameserversOn] } },
      "/api/v1/game-servers": { status: 200, body: { items: [] } },
      "/api/v1/game-servers/prefs": { status: 200, body: { view: "grid", recents: "" } },
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^game servers$/i })).toBeVisible();
    expect(await screen.findByText(/no game servers yet/i)).toBeVisible();
    expect(screen.getByRole("link", { name: /create your first server/i })).toBeVisible();
    const nav = screen.getByRole("navigation", { name: /^game servers$/i });
    expect(within(nav).getByRole("button", { name: /back to main menu/i })).toBeVisible();
    expect(within(nav).getByRole("link", { name: /all servers/i })).toBeVisible();
    expect(within(nav).queryByRole("link", { name: /^manage$/i })).toBeNull();
    expect(within(nav).queryByText(/system containers/i)).toBeNull();
  });

  it("keeps Game Servers out of the Workloads contextual sidebar", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [gameserversOn] } },
      "/api/v1/game-servers": { status: 200, body: { items: [paper] } },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: /^workloads$/i });
    expect(within(nav).getByRole("link", { name: /^manage$/i })).toBeVisible();
    expect(within(nav).queryByRole("link", { name: /game servers/i })).toBeNull();
    expect(within(nav).queryByRole("link", { name: /my minecraft server/i })).toBeNull();
  });

  it("opens capability links for a selected game server", async () => {
    window.history.replaceState({}, "", "/game-servers/gs-mc/console");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [gameserversOn] } },
      "/api/v1/game-servers": { status: 200, body: { items: [paper] } },
      "/api/v1/game-servers/gs-mc": { status: 200, body: paper },
      "/api/v1/game-servers/gs-mc/tuning": { status: 200, body: { items: [] } },
      "/api/v1/game-servers/gs-mc/console": { status: 200, body: { log: "", favorites: [] } },
      "/api/v1/game-servers/gs-mc/console/history": { status: 200, body: { items: [] } },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: /^game servers$/i });
    expect(within(nav).getByRole("button", { name: /back to game servers/i })).toBeVisible();
    expect(await within(nav).findByRole("link", { name: /^console$/i })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: /^plugins$/i })).toBeVisible();
    expect(within(nav).getByRole("link", { name: /^players$/i })).toBeVisible();
    expect(within(nav).queryByRole("link", { name: /^mods$/i })).toBeNull();
    expect(within(nav).queryByRole("link", { name: /^workshop$/i })).toBeNull();
    expect(within(nav).queryByRole("link", { name: /^databases$/i })).toBeNull();
    expect(within(nav).queryByText(/system containers/i)).toBeNull();
  });

  it("hides the primary Game Servers button when the feature is disabled", async () => {
    window.history.replaceState({}, "", "/");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [{ ...gameserversOn, enabled: false }] } },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: /appliance/i });
    await waitFor(() => {
      expect(within(nav).getByRole("link", { name: /workloads/i })).toBeVisible();
    });
    expect(within(nav).queryByRole("link", { name: /game servers/i })).toBeNull();
  });

  it("shows Game Servers beside Workloads on the main Compute list when enabled", async () => {
    window.history.replaceState({}, "", "/");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [gameserversOn] } },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: /appliance/i });
    await waitFor(() => {
      expect(within(nav).getByRole("link", { name: /game servers/i })).toHaveAttribute("href", "/game-servers");
    });
    const labels = within(nav)
      .getAllByRole("link")
      .map((item) => item.getAttribute("aria-label") || item.textContent || "");
    expect(labels.indexOf("Workloads")).toBeLessThan(labels.indexOf("Game Servers"));
  });

  it("redirects old /workloads/game-servers bookmarks", async () => {
    window.history.replaceState({}, "", "/workloads/game-servers");
    mockApi({
      "/api/v1/features": { status: 200, body: { items: [gameserversOn] } },
      "/api/v1/game-servers": { status: 200, body: { items: [] } },
      "/api/v1/game-servers/prefs": { status: 200, body: { view: "grid", recents: "" } },
    });
    render(<App />);
    await waitFor(() => {
      expect(window.location.pathname).toBe("/game-servers");
    });
    expect(await screen.findByRole("heading", { name: /^game servers$/i })).toBeVisible();
  });
});
