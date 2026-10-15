import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  return new Response(JSON.stringify(body ?? {}), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mockApi(routes: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
    }
    const resolved = typeof hit === "function" ? hit(init) : hit;
    return jsonResponse(resolved.status, resolved.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
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
  "/api/v1/cluster/wg": { status: 200, body: { items: [], nodes: [] } },
  "/api/v1/cluster": { status: 200, body: { id: "cluster-1", name: "local", nodes: [] } },
  "/api/v1/cluster/ha": {
    status: 200,
    body: { mode: "single-writer", writer: true, replica_status: "not_configured", fencing_mode: "operator", multi_master: false },
  },
  "/api/v1/cluster/update": { status: 200, body: { preview: [], note: "Rolling drains one node" } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
  "/api/v1/storage/images": { status: 200, body: { items: [] } },
  "/api/v1/policies": { status: 200, body: { items: [] } },
  "/api/v1/policy-runs": { status: 200, body: { items: [] } },
  "/api/v1/ai/ask": { status: 200, body: { answer: "", citations: [], provider_status: "not_configured", mutate: false } },
  "/api/v1/ai/plans": { status: 200, body: { items: [] } },
  "/api/v1/settings/license": {
    status: 200,
    body: {
      edition: "ce",
      status: "absent",
      reason: "Community Edition. License activation is not required.",
      has_key: false,
      workloads_stopped: false,
      ee_blobs: false,
      contacts_api: false,
    },
  },
  "/api/v1/migration/adapters": { status: 200, body: { items: [] } },
  "/api/v1/migration/modes": { status: 200, body: { items: [], source_safety: "PROTECTED" } },
  "/api/v1/migration/sources": { status: 200, body: { items: [] } },
  "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
  "/api/v1/features": {
    status: 200,
    body: {
      items: [{ id: "docker", title: "Docker Management", enabled: true, core: false, package_status: "installed", runtime_status: "not_started", starts_runtime: false, kubelet_started: false, workload_count: 0 }],
    },
  },
};


const monitors = [
  {
    id: "m1",
    name: "shop",
    target: "https://shop.example.com/health",
    expect_status: "",
    keyword: "",
    containers: [{ machine_id: "ct-1", name: "shop-web-1" }],
    auto_restart: true,
    enabled: true,
    state: "down",
    last_check_at: "2026-10-16T00:00:00Z",
    last_status: 502,
    last_latency_ms: 80,
    last_error: "unexpected status 502 Bad Gateway (the tunnel or proxy could not reach the service)",
    failures: 3,
    events: [
      { at: "2026-10-16T00:00:00Z", ok: false, status_code: 502, latency_ms: 80, error: "502", action: "started shop-web-1" },
      { at: "2026-10-15T23:55:00Z", ok: true, status_code: 200, latency_ms: 40 },
    ],
    uptime_percent: 50,
  },
  {
    id: "m2",
    name: "blog",
    target: "blog.example.com",
    expect_status: "",
    keyword: "",
    containers: [],
    auto_restart: true,
    enabled: true,
    state: "up",
    last_status: 200,
    last_latency_ms: 35,
    last_error: "",
    failures: 0,
    events: [],
  },
];

const dockerInv = {
  summary: {},
  machines: [{ id: "ct-1", name: "apps", kind: "system-container", daemon_ok: true, health: "healthy", container_count: 1, projects: [] }],
  projects: [],
  containers: [{ id: "ct-1/w1", machine_id: "ct-1", machine_name: "apps", container_id: "w1", name: "shop-web-1", project: "shop", state: "running", health: "healthy", status_label: "Running" }],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("Uptime page", () => {
  it("lists down monitors first, creates a monitor with containers, and tests JSON", async () => {
    const sent: Record<string, string> = {};
    mockApi({
      ...baseRoutes,
      "GET /api/v1/uptime/monitors": { status: 200, body: { items: monitors } },
      "/api/v1/docker": { status: 200, body: dockerInv },
      "GET /api/v1/scripts": { status: 200, body: { items: [{ id: "s1", kind: "uptime", name: "all sites", body: '{"monitors":[{"name":"a","target":"a.example.com"}]}' }] } },
      "POST /api/v1/uptime/monitors": (init) => {
        sent.create = String(init?.body ?? "");
        return { status: 201, body: { ...monitors[1], id: "m3", name: "api" } };
      },
      "POST /api/v1/uptime/monitors/m1/check": (init) => {
        sent.check = "yes";
        void init;
        return { status: 200, body: monitors[0] };
      },
      "POST /api/v1/uptime/import": (init) => {
        sent.import = String(init?.body ?? "");
        return {
          status: 200,
          body: { valid: true, dry_run: true, results: [{ name: "a", change: "create", check: { ok: true, status_code: 200, latency_ms: 21 } }] },
        };
      },
    });
    window.history.replaceState({}, "", "/uptime");
    render(<App />);
    const cards = await screen.findAllByRole("article", { name: /^monitor /i });
    expect(cards[0]).toHaveAccessibleName(/monitor shop/i);
    expect(within(cards[0]).getByText(/tunnel or proxy could not reach/i)).toBeVisible();
    expect(within(cards[0]).getByText(/restarts apps\/shop-web-1/i)).toBeVisible();

    fireEvent.contextMenu(cards[0]);
    fireEvent.click(await screen.findByRole("menuitem", { name: /^check now$/i }));
    await waitFor(() => expect(sent.check).toBe("yes"));

    fireEvent.click(screen.getByRole("button", { name: /^new monitor$/i }));
    const dialog = await screen.findByRole("dialog", { name: /new monitor/i });
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: "api" } });
    fireEvent.change(within(dialog).getByLabelText(/url, host\/path or ip:port/i), { target: { value: "https://api.example.com" } });
    fireEvent.click(await within(dialog).findByRole("checkbox", { name: /shop-web-1/i }));
    fireEvent.click(within(dialog).getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(sent.create).toBeTruthy());
    expect(JSON.parse(sent.create)).toMatchObject({
      name: "api",
      target: "https://api.example.com",
      containers: [{ machine: "apps", name: "shop-web-1" }],
      auto_restart: true,
    });

    fireEvent.click(screen.getByRole("button", { name: /^json$/i }));
    const json = await screen.findByRole("dialog", { name: /uptime monitors as json/i });
    fireEvent.change(await within(json).findByLabelText(/saved scripts/i), { target: { value: "s1" } });
    expect((within(json).getByLabelText(/^json$/i) as HTMLTextAreaElement).value).toContain("a.example.com");
    fireEvent.click(within(json).getByRole("button", { name: /^test$/i }));
    expect(await within(json).findByText(/nothing was saved/i)).toBeVisible();
    expect(JSON.parse(sent.import)).toMatchObject({ dry_run: true, script: { monitors: [{ name: "a" }] } });
  });
});

describe("Automation page", () => {
  it("creates an Uptime recovery policy", async () => {
    let created = "";
    mockApi({
      ...baseRoutes,
      "GET /api/v1/policies": { status: 200, body: { items: [] } },
      "POST /api/v1/policies": (init) => {
        created = String(init?.body ?? "");
        return { status: 201, body: { id: "p1", name: "recover sites", kind: "uptime_check", action: "check_and_recover", target: "shop" } };
      },
    });
    window.history.replaceState({}, "", "/automation");
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/what it does/i), { target: { value: "uptime_check" } });
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "recover sites" } });
    fireEvent.change(screen.getByLabelText(/monitor name/i), { target: { value: "shop" } });
    fireEvent.click(screen.getByRole("button", { name: /create policy/i }));
    await waitFor(() => expect(created).toBeTruthy());
    const body = JSON.parse(created);
    expect(body).toMatchObject({ name: "recover sites", kind: "uptime_check", target: "shop" });
    expect(body.threshold_percent).toBeUndefined();
  });
});
