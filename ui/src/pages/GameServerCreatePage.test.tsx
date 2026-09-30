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
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

const CATEGORIES = ["survival", "sandbox", "shooter", "strategy", "racing"];

function manyItems(count: number): CatalogueItem[] {
  return Array.from({ length: count }, (_, i) => {
    const n = String(i + 1).padStart(3, "0");
    return {
      id: `ndl-game-${n}`,
      name: `Game ${n}`,
      game: `game${n}`,
      game_title: `Game ${n}`,
      builtin: true,
      category: CATEGORIES[i % CATEGORIES.length],
      install_method: i % 2 === 0 ? "SteamCMD" : "Official download",
      architectures: i % 10 === 0 ? ["amd64", "arm64"] : ["amd64"],
      default_memory_mb: 2048,
      verification: "source",
    };
  });
}

function favouriteButtons() {
  return screen.queryAllByRole("button", { name: /^favourite /i });
}

function openFilters() {
  const toggle = screen.getByRole("button", { name: /^filters/i });
  if (toggle.getAttribute("aria-expanded") !== "true") {
    fireEvent.click(toggle);
  }
}

function openCreate(routes: Record<string, { status: number; body?: unknown }>) {
  window.history.replaceState({}, "", "/game-servers/create");
  const fetchMock = mockApi({ "/api/v1/game-servers": { status: 200, body: { items: [] } }, ...routes });
  render(<App />);
  return fetchMock;
}

function requestsTo(fetchMock: ReturnType<typeof mockApi>, path: string, method: string): Record<string, unknown>[] {
  return fetchMock.mock.calls
    .filter((call) => {
      const [input, init] = call as unknown as [RequestInfo | URL, RequestInit | undefined];
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      return new URL(url, "http://localhost").pathname === path && (init?.method ?? "GET") === method;
    })
    .map((call) => {
      const init = (call as unknown as [unknown, RequestInit | undefined])[1];
      return JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
    });
}

const minecraftFamily: CatalogueItem[] = [
  { id: "ndl-mc-paper", name: "Paper", game: "minecraft", game_title: "Minecraft: Java Edition", family: "minecraft", builtin: true, category: "minecraft", install_method: "PaperMC API", architectures: ["amd64", "arm64"], requirements: [{ kind: "eula", stage: "start", label: "Minecraft EULA" }], default_memory_mb: 4096, verification: "started" },
  { id: "ndl-mc-fabric", name: "Fabric", game: "minecraft", game_title: "Minecraft: Java Edition", family: "minecraft", builtin: true, category: "minecraft", install_method: "Mod loader installer" },
  { id: "ndl-mc-forge", name: "Forge", game: "minecraft", game_title: "Minecraft: Java Edition", family: "minecraft", builtin: true, category: "minecraft", install_method: "Mod loader installer" },
  { id: "ndl-css", name: "Counter-Strike: Source", game: "css", game_title: "Counter-Strike: Source", builtin: true, category: "shooter", install_method: "SteamCMD", requirements: [{ kind: "gslt", stage: "optional", label: "Game Server Login Token" }] },
  { id: "ndl-hldm", name: "Half-Life Deathmatch", game: "hldm", builtin: true, category: "shooter", install_method: "SteamCMD", requirements: [{ kind: "steam_account", stage: "install", label: "Steam account" }] },
];

const paperTemplate = {
  id: "ndl-mc-paper",
  name: "Paper",
  game: "minecraft",
  game_title: "Minecraft: Java Edition",
  family: "minecraft",
  summary: "High performance Minecraft server with plugin support.",
  install_method: "PaperMC API",
  update_procedure: "Reinstall downloads the latest build for the chosen version.",
  default_cpus: 2,
  default_memory_mb: 4096,
  min_memory_mb: 2048,
  default_disk_mb: 8192,
  architectures: ["amd64", "arm64"],
  images: { "Java 21": "ghcr.io/example/java:21", "Java 17": "ghcr.io/example/java:17" },
  default_image: "ghcr.io/example/java:17",
  default_ports: [{ name: "Game", container_port: 25565, protocol: "tcp", env: "SERVER_PORT" }],
  requirements: [{ kind: "eula", stage: "start", label: "Minecraft EULA", url: "https://www.minecraft.net/eula" }],
  dependencies: ["fontconfig"],
  notes: ["Plugins go in the plugins folder."],
  verification: "started",
  start_requires: [],
  variables: [
    { name: "Minecraft version", env: "MC_VERSION", default: "latest", viewable: true, field_type: "select", options: ["latest", "1.21.4", "1.20.6"] },
    { name: "RCON password", env: "RCON_PASSWORD", default: "", viewable: true, secret: true, field_type: "password", generate: "password" },
  ],
};

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
    expect(screen.getByRole("button", { name: /^project zomboid/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^minecraft paper/i })).toBeVisible();
    fireEvent.click(screen.getByText(/import a pelican or pterodactyl egg/i));
    expect(screen.getByRole("button", { name: /refresh catalogue/i })).toBeVisible();
    expect(screen.getByLabelText(/egg json url/i)).toBeVisible();

    fireEvent.change(templateSearch, { target: { value: "pz" } });
    expect(screen.getByRole("button", { name: /^project zomboid/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^minecraft paper/i })).toBeNull();
    expect(within(screen.getByRole("navigation", { name: /^game servers$/i })).getByRole("treeitem", { name: /my minecraft server/i })).toBeVisible();

    fireEvent.change(serverSearch, { target: { value: "valheim" } });
    expect(screen.getByRole("button", { name: /^project zomboid/i })).toBeVisible();
    expect(within(screen.getByRole("navigation", { name: /^game servers$/i })).queryByRole("treeitem", { name: /my minecraft server/i })).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: /^all\b/i }));
    fireEvent.change(templateSearch, { target: { value: "" } });
    openFilters();
    fireEvent.change(screen.getByLabelText(/install method/i), { target: { value: "Standalone" } });
    expect(screen.getByRole("button", { name: /^fivem fxserver/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^project zomboid/i })).toBeNull();

    fireEvent.change(screen.getByLabelText(/install method/i), { target: { value: "" } });
    fireEvent.click(screen.getByRole("tab", { name: /^all\b/i }));
    fireEvent.change(templateSearch, { target: { value: "pz" } });
    fireEvent.click(screen.getByRole("button", { name: /^project zomboid/i }));
    expect(await screen.findByLabelText(/admin password/i)).toBeVisible();
    expect(screen.getAllByLabelText(/server name/i).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: /^continue$/i }));
    expect(await screen.findByRole("combobox")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^review$/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /install server/i })).toBeVisible();
    });
    expect(screen.getByRole("heading", { name: /^project zomboid/i })).toBeVisible();
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

describe("Create Game Server catalogue at scale", () => {
  it("renders a bounded number of cards and more after Show more", async () => {
    openCreate({ "/api/v1/game-servers/catalogue": { status: 200, body: { items: manyItems(250) } } });
    expect(await screen.findByRole("searchbox", { name: /search available game templates/i })).toBeVisible();
    expect(screen.getByText(/250 games, 250 server types/i)).toBeVisible();
    expect(favouriteButtons()).toHaveLength(48);
    fireEvent.click(screen.getByRole("button", { name: /show more/i }));
    expect(favouriteButtons()).toHaveLength(96);
    expect(screen.getByRole("button", { name: /show more \(154 more\)/i })).toBeVisible();

    // Filtering resets the page and narrows the list.
    openFilters();
    fireEvent.change(screen.getByLabelText(/^platform$/i), { target: { value: "arm64" } });
    expect(favouriteButtons()).toHaveLength(25);
    expect(screen.queryByRole("button", { name: /show more/i })).toBeNull();
    fireEvent.change(screen.getByLabelText(/^platform$/i), { target: { value: "" } });
    fireEvent.click(screen.getByRole("tab", { name: /^shooters\b/i }));
    expect(favouriteButtons()).toHaveLength(48);
    expect(screen.getByRole("button", { name: /show more \(2 more\)/i })).toBeVisible();
    fireEvent.change(screen.getByLabelText(/install method/i), { target: { value: "SteamCMD" } });
    expect(favouriteButtons()).toHaveLength(25);
    fireEvent.click(screen.getByRole("tab", { name: /^all\b/i }));
    fireEvent.change(screen.getByLabelText(/install method/i), { target: { value: "" } });

    fireEvent.change(screen.getByRole("searchbox", { name: /search available game templates/i }), { target: { value: "game 249" } });
    expect(screen.getByRole("button", { name: /^game 249/i })).toBeVisible();
    expect(favouriteButtons()).toHaveLength(1);
  });

  it("shows one tile per game and opens a server type chooser for games with several", async () => {
    openCreate({ "/api/v1/game-servers/catalogue": { status: 200, body: { items: minecraftFamily } } });
    const tile = await screen.findByRole("button", { name: /^minecraft: java edition, 3 server types/i });
    expect(screen.queryByRole("button", { name: /^paper/i })).toBeNull();
    expect(screen.getByText(/3 games, 5 server types/i)).toBeVisible();
    expect(favouriteButtons()).toHaveLength(3);

    fireEvent.click(tile);
    expect(await screen.findByRole("heading", { name: /^minecraft: java edition/i })).toBeVisible();
    expect(screen.getByText(/choose a server type\. 3 available/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /^paper/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^fabric/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^forge/i })).toBeVisible();
    expect(screen.getByText("EULA")).toBeVisible();
    expect(screen.getByText("Start tested")).toHaveAttribute("title", expect.stringMatching(/isolated test environment/i));
    fireEvent.click(screen.getByRole("button", { name: /all games/i }));
    expect(await screen.findByRole("button", { name: /^minecraft: java edition, 3 server types/i })).toBeVisible();

    // Minecraft needs only the EULA; CS:S has an optional GSLT. Both count as credential free.
    openFilters();
    fireEvent.click(screen.getByRole("checkbox", { name: /no credentials needed/i }));
    expect(favouriteButtons()).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /^half-life deathmatch/i })).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: /no credentials needed/i }));

    fireEvent.change(screen.getByRole("searchbox", { name: /search available game templates/i }), { target: { value: "gslt" } });
    expect(screen.getByRole("button", { name: /^counter-strike: source/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^minecraft: java edition/i })).toBeNull();

    // Searching for a distribution finds its game and narrows the chooser.
    fireEvent.change(screen.getByRole("searchbox", { name: /search available game templates/i }), { target: { value: "fabric" } });
    fireEvent.click(await screen.findByRole("button", { name: /^minecraft: java edition, 3 server types/i }));
    expect(await screen.findByText(/1 match your search/i)).toBeVisible();
    expect(screen.queryByRole("button", { name: /^forge/i })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /show all 3/i }));
    expect(screen.getByRole("button", { name: /^forge/i })).toBeVisible();
  });

  it("toggles favourites and remembers recents in localStorage when prefs lack them", async () => {
    openCreate({
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: catalogue } },
      "/api/v1/game-servers/templates": { status: 200, body: zomboidTemplate },
    });
    await screen.findByRole("button", { name: /^project zomboid/i });
    const card = screen.getByRole("button", { name: /^project zomboid/i }).closest(".gs-game") as HTMLElement;
    const star = within(card).getByRole("button", { name: /^favourite /i });
    expect(star).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(star);
    expect(star).toHaveAttribute("aria-pressed", "true");
    expect(JSON.parse(localStorage.getItem("ndl.gameservers.catalogue.favorites") ?? "[]")).toEqual(["ndl-zomboid"]);
    fireEvent.click(screen.getByRole("tab", { name: /^favourites\b/i }));
    expect(favouriteButtons()).toHaveLength(1);
    expect(screen.getByRole("button", { name: /^project zomboid/i })).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: /^project zomboid/i }));
    expect(await screen.findByLabelText(/admin password/i)).toBeVisible();
    expect(JSON.parse(localStorage.getItem("ndl.gameservers.catalogue.recents") ?? "[]")).toEqual(["ndl-zomboid"]);
    fireEvent.click(screen.getByRole("button", { name: /^back$/i }));
    fireEvent.click(await screen.findByRole("tab", { name: /^recent\b/i }));
    expect(favouriteButtons()).toHaveLength(1);
  });

  it("stores favourites through the prefs API when the server supports them", async () => {
    const fetchMock = openCreate({
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: catalogue } },
      "/api/v1/game-servers/prefs": { status: 200, body: { view: "grid", recents: "[]", favorites: JSON.stringify(["ndl-cs2"]) } },
    });
    const card = (await screen.findByRole("button", { name: /^counter-strike 2/i })).closest(".gs-game") as HTMLElement;
    await waitFor(() => {
      expect(within(card).getByRole("button", { name: /^favourite /i })).toHaveAttribute("aria-pressed", "true");
    });
    const zomboid = screen.getByRole("button", { name: /^project zomboid/i }).closest(".gs-game") as HTMLElement;
    fireEvent.click(within(zomboid).getByRole("button", { name: /^favourite /i }));
    await waitFor(() => {
      expect(requestsTo(fetchMock, "/api/v1/game-servers/prefs", "PUT")).toContainEqual({ favorites: JSON.stringify(["ndl-zomboid", "ndl-cs2"]) });
    });
  });

  it("shows the template summary, runtime selector, and blocks install on preflight errors", async () => {
    const fetchMock = openCreate({
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: minecraftFamily } },
      "/api/v1/game-servers/templates": { status: 200, body: paperTemplate },
      "/api/v1/game-servers/preflight": {
        status: 200,
        body: {
          ok: false,
          errors: ["Node node-1 is arm64 but this image is amd64 only."],
          warnings: ["2 GiB is below the recommended 4 GiB."],
          ports: [{ name: "Game", container_port: 25565, host_port: 25566, protocol: "tcp" }],
          env_updates: { SERVER_PORT: "25566" },
          image: "ghcr.io/example/java:17",
          dependencies: ["fontconfig"],
          node: { id: "node-1", name: "Node A", architecture: "arm64", memory_total_bytes: 8 * 1024 ** 3, memory_committed_bytes: 2 * 1024 ** 3 },
        },
      },
    });
    fireEvent.click(await screen.findByRole("button", { name: /^minecraft: java edition/i }));
    fireEvent.click(await screen.findByRole("button", { name: /^paper/i }));
    const summary = await screen.findByRole("complementary", { name: /template summary/i });
    expect(within(summary).getByText("PaperMC API")).toBeVisible();
    expect(within(summary).getByText(/latest build/i)).toBeVisible();
    expect(within(summary).getByRole("link", { name: /minecraft eula/i })).toHaveAttribute("href", "https://www.minecraft.net/eula");
    expect(within(summary).getByText(/Game 25565\/tcp/)).toBeVisible();
    expect(screen.getByText(/leave empty to generate one/i)).toBeVisible();
    const runtime = screen.getByLabelText(/runtime version/i) as HTMLSelectElement;
    expect(runtime.value).toBe("Java 17");
    const version = screen.getByLabelText(/minecraft version/i) as HTMLSelectElement;
    expect([...version.options].map((o) => o.value)).toEqual(["latest", "1.21.4", "1.20.6"]);

    fireEvent.click(screen.getByRole("button", { name: /^continue$/i }));
    const ram = await screen.findByLabelText(/ram \(mib\)/i);
    fireEvent.change(ram, { target: { value: "1024" } });
    expect(screen.getByText(/below the 2 GiB minimum/i)).toBeVisible();
    fireEvent.change(ram, { target: { value: "3072" } });
    expect(screen.getByText(/below the recommended 4 GiB/i)).toBeVisible();
    expect(await screen.findByText(/architecture: arm64/i)).toBeVisible();
    expect(screen.getByText(/2 GiB committed of 8 GiB/i)).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: /^review$/i }));
    expect(await screen.findByText(/is arm64 but this image is amd64 only/i)).toBeVisible();
    expect(screen.getByText(/2 GiB is below the recommended 4 GiB\./i)).toBeVisible();
    expect(screen.getByText("SERVER_PORT = 25566")).toBeVisible();
    expect(screen.getByText("ghcr.io/example/java:17")).toBeVisible();
    expect(screen.getByRole("button", { name: /install server/i })).toBeDisabled();
    const bodies = requestsTo(fetchMock, "/api/v1/game-servers/preflight", "POST");
    expect(bodies.at(-1)).toMatchObject({ template_id: "ndl-mc-paper", image_label: "Java 17", node_id: "node-1", memory_bytes: 3072 * 1024 * 1024 });
  });

  it("falls back to plain review when the preflight endpoint is missing", async () => {
    const fetchMock = openCreate({
      "/api/v1/game-servers/catalogue": { status: 200, body: { items: minecraftFamily } },
      "/api/v1/game-servers/templates": { status: 200, body: paperTemplate },
    });
    fireEvent.click(await screen.findByRole("button", { name: /^minecraft: java edition/i }));
    fireEvent.click(await screen.findByRole("button", { name: /^paper/i }));
    fireEvent.change(await screen.findByLabelText(/runtime version/i), { target: { value: "Java 21" } });
    fireEvent.click(screen.getByRole("button", { name: /^continue$/i }));
    fireEvent.click(await screen.findByRole("button", { name: /^review$/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /install server/i })).toBeEnabled();
    });
    fireEvent.click(screen.getByRole("button", { name: /install server/i }));
    await waitFor(() => {
      expect(requestsTo(fetchMock, "/api/v1/game-servers", "POST")).toHaveLength(1);
    });
    expect(requestsTo(fetchMock, "/api/v1/game-servers", "POST")[0]).toMatchObject({
      template_id: "ndl-mc-paper",
      image_label: "Java 21",
      node_id: "node-1",
      cpus: 2,
      memory_bytes: 4096 * 1024 * 1024,
      disk_bytes: 8192 * 1024 * 1024,
      env: { MC_VERSION: "latest", RCON_PASSWORD: "" },
    });
  });
});
