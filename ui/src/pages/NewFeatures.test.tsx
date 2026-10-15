import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

const admin: MeResponse = { user_id: "user-1", username: "admin", roles: ["admin"], edition: "ce", ux_level: "guided", expert_ack: false };

type Route = { status: number; body?: unknown } | ((init?: RequestInit, url?: URL) => { status: number; body?: unknown });

function mockApi(routes: Record<string, Route>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${url.pathname}`] ?? routes[url.pathname];
    if (!hit) {
      return new Response(JSON.stringify({ error: `unmocked ${url.pathname}` }), { status: 404, headers: { "Content-Type": "application/json" } });
    }
    const res = typeof hit === "function" ? hit(init, url) : hit;
    return new Response(JSON.stringify(res.body ?? {}), { status: res.status, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const base: Record<string, Route> = {
  "/api/v1/health": { status: 200, body: { status: "ok" } },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/me": { status: 200, body: admin },
  "/api/v1/nodes": { status: 200, body: { items: [] } },
  "/api/v1/events": { status: 200, body: { items: [] } },
  "/api/v1/timeline": { status: 200, body: { items: [] } },
  "/api/v1/alerts": { status: 200, body: { items: [] } },
  "/api/v1/tasks": { status: 200, body: { items: [] } },
  "/api/v1/cluster": { status: 200, body: { id: "c", name: "local", nodes: [] } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

const T1 = "11111111-1111-4111-8111-111111111111";
const T2 = "22222222-2222-4222-8222-222222222222";
const A = "a".repeat(32);
const B = "b".repeat(32);

function route(host: string, account: string, accountName: string, tunnel: string, tunnelName: string, dns: string) {
  return {
    key: `${tunnel}|${host}|`,
    hostname: host,
    service: "http://192.168.2.38:3000",
    origin: { protocol: "http", host: "192.168.2.38", port: 3000 },
    options: {},
    enabled: true,
    editable: true,
    account_id: account,
    account_name: accountName,
    tunnel_id: tunnel,
    tunnel_name: tunnelName,
    tunnel_status: "healthy",
    zone_id: "z",
    zone_name: "example.com",
    dns: { action: dns, hostname: host, reason: dns === "conflict" ? "existing A record(s) use this name" : undefined },
  };
}

const inventory = {
  fetched_at: new Date().toISOString(),
  refreshing: false,
  errors: [],
  accounts: [
    { id: A, name: "Racing", credentials: [{ id: "c1", name: "racing", status: "active" }], zones: [] },
    { id: B, name: "Hosting", credentials: [{ id: "c2", name: "hosting", status: "active" }], zones: [] },
  ],
  tunnels: [
    { id: T1, name: "racing-shared", account_tag: A, account_name: "Racing", status: "healthy", config_src: "cloudflare", remote_managed: true, connectors: [{ id: "x", version: "2026.10.1", arch: "amd64", conns: [{ colo_name: "syd01", origin_ip: "1" }] }], route_count: 1, local: [] },
    { id: T2, name: "hosting-shared", account_tag: B, account_name: "Hosting", status: "down", config_src: "cloudflare", remote_managed: true, connectors: [], route_count: 1, local: [] },
  ],
  routes: [route("shop.racing.example", A, "Racing", T1, "racing-shared", "ok"), route("app.hosting.example", B, "Hosting", T2, "hosting-shared", "create")],
  dns: [],
};

describe("Cloudflare page", () => {
  it("shows every account together, filters, diagnoses, blocks DNS conflicts and confirms deletes", async () => {
    let deleted = "";
    mockApi({
      ...base,
      "/api/v1/cloudflare/inventory": { status: 200, body: inventory },
      "/api/v1/cloudflare/credentials": { status: 200, body: { items: [] } },
      "/api/v1/cloudflare/machines": { status: 200, body: { items: [{ id: "host", name: "Host", kind: "host" }], supported: true } },
      "POST /api/v1/cloudflare/routes/diagnose": {
        status: 200,
        body: {
          summary: "Internal service: the connector cannot reach the origin.",
          checks: [
            { area: "tunnel", status: "ok", detail: "1 connector(s) connected via syd01" },
            { area: "origin", status: "fail", detail: "192.168.2.38:3000 is not reachable from Host: nothing is listening" },
          ],
        },
      },
      "POST /api/v1/cloudflare/routes/plan": { status: 200, body: { zone: { id: "z", name: "example.com" }, plan: { action: "conflict", hostname: "mail.racing.example", reason: "existing A record(s) use this name" } } },
      "POST /api/v1/cloudflare/routes/delete": (init) => {
        deleted = String(init?.body ?? "");
        return { status: 200, body: { deleted: true, note: "DNS record removed" } };
      },
    });
    window.history.replaceState({}, "", "/cloudflare");
    render(<App />);
    const table = await screen.findByRole("table", { name: /public hostnames/i });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    const rowOf = (host: string) => within(table).getByText(host).closest("tr")!;
    expect(within(rowOf("app.hosting.example")).getByText("Hosting")).toBeVisible();
    expect(within(rowOf("shop.racing.example")).getByText("Racing")).toBeVisible();
    expect(within(table).getByText(/no dns record/i)).toBeVisible();
    // One DNS issue across both accounts.
    expect(screen.getByText("DNS issues").parentElement).toHaveTextContent("1");

    fireEvent.change(screen.getByLabelText(/account filter/i), { target: { value: A } });
    expect(within(table).queryByText("app.hosting.example")).toBeNull();
    fireEvent.change(screen.getByLabelText(/account filter/i), { target: { value: "" } });

    fireEvent.contextMenu(within(table).getByText("shop.racing.example").closest("tr")!);
    fireEvent.click(await screen.findByRole("menuitem", { name: /^diagnose$/i }));
    const diag = await screen.findByRole("dialog", { name: /diagnose shop.racing.example/i });
    expect(await within(diag).findByText(/internal service/i)).toBeVisible();
    expect(within(diag).getByText(/not reachable from host/i)).toBeVisible();
    fireEvent.click(within(diag).getByRole("button", { name: /close/i }));

    fireEvent.click(screen.getByRole("button", { name: /^add hostname$/i }));
    const add = await screen.findByRole("dialog", { name: /add public hostname/i });
    fireEvent.change(within(add).getByLabelText(/public hostname/i), { target: { value: "mail.racing.example" } });
    fireEvent.change(within(add).getByLabelText(/^address$/i), { target: { value: "192.168.2.40" } });
    expect(await within(add).findByText(/existing a record/i)).toBeVisible();
    expect(within(add).getByRole("button", { name: /^add hostname$/i })).toBeDisabled();
    fireEvent.click(within(add).getByRole("button", { name: /cancel/i }));

    fireEvent.contextMenu(within(table).getByText("shop.racing.example").closest("tr")!);
    fireEvent.click(await screen.findByRole("menuitem", { name: /^delete$/i }));
    const del = await screen.findByRole("dialog", { name: /delete shop.racing.example/i });
    const go = within(del).getByRole("button", { name: /^delete$/i });
    expect(go).toBeDisabled();
    fireEvent.change(within(del).getByLabelText(/type shop.racing.example/i), { target: { value: "shop.racing.example" } });
    fireEvent.click(go);
    await waitFor(() => expect(deleted).toContain('"confirm":"shop.racing.example"'));
    expect(JSON.parse(deleted)).toMatchObject({ delete_dns: true, tunnel_id: T1 });
  });
});

describe("Extensions page", () => {
  it("shows a failed Compose install with its reason and retries, updates all", async () => {
    const posts: string[] = [];
    mockApi({
      ...base,
      "/api/v1/workloads/w1": { status: 200, body: { id: "w1", name: "VaultDock", kind: "system-container", status: "running" } },
      "/api/v1/workloads/w1/extensions": {
        status: 200,
        body: {
          busy: false,
          items: [
            { id: "docker", name: "Docker Engine", description: "Container runtime", category: "containers", available: true, state: "installed", requested: true, version: "26.1.5" },
            {
              id: "compose",
              name: "Docker Compose",
              description: "Compose v2 plugin",
              category: "containers",
              available: true,
              state: "failed",
              requested: true,
              message: "context canceled",
            },
            { id: "gh", name: "GitHub CLI", description: "GitHub command line", category: "development", available: false, state: "missing", requested: false, unavailable_reason: "GitHub CLI is not in this image's default repositories" },
          ],
        },
      },
      "POST /api/v1/workloads/w1/extensions": (init) => {
        posts.push(String(init?.body ?? ""));
        return { status: 202, body: { started: true, extras: ["docker", "compose"] } };
      },
    });
    window.history.replaceState({}, "", "/workloads/w1/extensions");
    render(<App />);
    const table = await screen.findByRole("table", { name: /extensions/i });
    expect(within(table).getByText("context canceled")).toBeVisible();
    expect(within(table).getByText(/not in this image/i)).toBeVisible();
    fireEvent.click(within(table).getByRole("button", { name: /^retry$/i }));
    await waitFor(() => expect(posts[0]).toBeTruthy());
    expect(JSON.parse(posts[0])).toEqual({ action: "install", extras: ["compose"] });
    fireEvent.click(screen.getByRole("button", { name: /^update all$/i }));
    await waitFor(() => expect(posts[1]).toBeTruthy());
    expect(JSON.parse(posts[1])).toEqual({ action: "update", all: true });
  });
});

describe("Storage manager", () => {
  it("scans, explains items, protects workload disks and confirms bulk deletes", async () => {
    let deleted = "";
    const scan = { id: "s1", root: "/var/lib/ndl/storage/local", one_filesystem: true, state: "done", started_at: "", entries: 9, bytes: 300, fs_total: 1000, fs_free: 200 };
    mockApi({
      ...base,
      "/api/v1/storage/pools": { status: 200, body: { items: [] } },
      "/api/v1/storage/images": { status: 200, body: { items: [] } },
      "/api/v1/host/disk": { status: 200, body: { status: { level: "ok", filesystems: [], reserve_bytes: 0 } } },
      "/api/v1/storage/scan/targets": { status: 200, body: { can_delete: true, items: [{ label: "local pool (directory)", path: "/var/lib/ndl/storage/local", kind: "pool" }] } },
      "POST /api/v1/storage/scan": { status: 202, body: scan },
      "/api/v1/storage/scan/s1": {
        status: 200,
        body: {
          scan,
          path: "",
          node: { name: "local", path: "", dir: true, size: 300, apparent: 300, files: 3, dirs: 2, mtime: 0, insight: { purpose: "No-dal storage pool data", importance: "important", confidence: "likely" } },
          items: [
            { name: "vault.img", path: "vault.img", dir: false, size: 200, apparent: 900, files: 1, dirs: 0, mtime: 1700000000, insight: { purpose: "The disk of system-container VaultDock", importance: "critical", confidence: "known", protected: true } },
            { name: "old.tar.gz", path: "old.tar.gz", dir: false, size: 60, apparent: 60, files: 1, dirs: 0, mtime: 1700000000, insight: { purpose: "Archive", importance: "review", confidence: "likely" } },
            { name: "cache", path: "cache", dir: true, size: 40, apparent: 40, files: 1, dirs: 0, mtime: 1700000000, has_children: true, insight: { purpose: "Application caches", importance: "low", confidence: "likely" } },
          ],
        },
      },
      "POST /api/v1/storage/scan/s1/delete": (init) => {
        deleted = String(init?.body ?? "");
        return { status: 200, body: { deleted: 2, freed_bytes: 100, results: [{ path: "old.tar.gz", deleted: true, freed: 60 }, { path: "cache", deleted: true, freed: 40 }] } };
      },
    });
    window.history.replaceState({}, "", "/storage/manage");
    render(<App />);
    expect(await screen.findByRole("group", { name: /storage sections/i })).toBeVisible();
    fireEvent.click(await screen.findByRole("button", { name: /^scan$/i }));
    const table = await screen.findByRole("table", { name: /folder contents/i });
    expect(within(table).getByText(/the disk of system-container vaultdock/i)).toBeVisible();
    expect(within(table).getByRole("checkbox", { name: /select vault.img/i })).toBeDisabled();
    fireEvent.click(within(table).getByRole("checkbox", { name: /select old.tar.gz/i }));
    fireEvent.click(within(table).getByRole("checkbox", { name: /select cache/i }));
    fireEvent.click(screen.getByRole("button", { name: /delete selected/i }));
    const dialog = await screen.findByRole("dialog", { name: /delete 2 item/i });
    expect(within(dialog).getByText(/not marked usually safe/i)).toBeVisible();
    const go = within(dialog).getByRole("button", { name: /^delete /i });
    expect(go).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/type delete 2/i), { target: { value: "delete 2" } });
    fireEvent.click(go);
    await waitFor(() => expect(deleted).toBeTruthy());
    expect(JSON.parse(deleted)).toEqual({ items: ["old.tar.gz", "cache"], confirm: "delete 2" });
    expect(await screen.findByText(/deleted 2 item/i)).toBeVisible();
  });
});

describe("Create system container", () => {
  it("follows extras installing in the background and shows which failed and why", async () => {
    let polls = 0;
    mockApi({
      ...base,
      "/api/v1/storage/pools": { status: 200, body: { items: [{ id: "p1", name: "local", status: "available", backend_type: "directory" }] } },
      "/api/v1/networks": { status: 200, body: { items: [{ id: "n1", name: "LAN", status: "available", kind: "bridge" }], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [], image_pins: ["debian/13/amd64/default"] } },
      "/api/v1/workload-extras": {
        status: 200,
        body: {
          items: [
            { id: "docker", name: "Docker Engine", description: "Container runtime", category: "containers", available: true, needs_nesting: true },
            { id: "compose", name: "Docker Compose", description: "Compose v2 plugin", category: "containers", requires: ["docker"], available: true, needs_nesting: true },
          ],
        },
      },
      "POST /api/v1/workloads": { status: 201, body: { id: "w9", name: "vault", kind: "system-container", status: "running", setup_status: "installing", setup_operation_id: "op1" } },
      "/api/v1/workloads/w9/extensions": () => {
        polls++;
        const done = polls > 1;
        return {
          status: 200,
          body: {
            busy: !done,
            items: [
              { id: "docker", name: "Docker Engine", state: done ? "installed" : "installing", available: true, requested: true },
              { id: "compose", name: "Docker Compose", state: done ? "failed" : "installing", available: true, requested: true, message: done ? "context canceled" : undefined },
            ],
          },
        };
      },
    });
    window.history.replaceState({}, "", "/workloads/new/system-container");
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/^name$/i), { target: { value: "vault" } });
    const steps = screen.getByRole("navigation", { name: /create steps/i });
    fireEvent.click(within(steps).getByRole("button", { name: /extras/i }));
    fireEvent.click(await screen.findByText("Docker Compose"));
    fireEvent.click(within(steps).getByRole("button", { name: /review/i }));
    fireEvent.click(await screen.findByRole("button", { name: /create system container/i }));
    expect(await screen.findByText(/extensions install in the background/i)).toBeVisible();
    expect(await screen.findByText(/docker compose failed: context canceled/i, {}, { timeout: 8000 })).toBeVisible();
    expect(screen.getByText(/docker engine installed/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /retry failed setup/i })).toBeVisible();
  }, 15000);
});
