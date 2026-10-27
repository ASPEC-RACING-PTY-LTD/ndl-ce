import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";
import { siteMenuFor } from "../components/ContextMenu";
import { startThemeSync } from "../theme";

// main.tsx starts this in the app.
startThemeSync();

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["superadmin"],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
  // These tests cover the classic sidebar, which lists every page.
  ui_prefs: { "layout.nav": "classic", "layout.instance": "classic" },
};

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

const feature = (id: string, enabled: boolean) => ({ id, title: id, enabled, core: false, package_status: "", runtime_status: "", starts_runtime: false, kubelet_started: false, workload_count: 0, reason: "", software_only: true });

function base(features: Record<string, boolean>): Record<string, Route> {
  return {
    "/api/v1/health": { status: 200, body: { status: "ok" } },
    "/api/v1/setup/status": { status: 200, body: { open: false } },
    "/api/v1/me": { status: 200, body: admin },
    "/api/v1/nodes": { status: 200, body: { items: [] } },
    "/api/v1/events": { status: 200, body: { items: [] } },
    "/api/v1/tasks": { status: 200, body: { items: [] } },
    "/api/v1/workloads": { status: 200, body: { items: [] } },
    "/api/v1/networks": { status: 200, body: { items: [] } },
    "/api/v1/storage/pools": { status: 200, body: { items: [] } },
    "/api/v1/features": { status: 200, body: { items: Object.entries(features).map(([id, on]) => feature(id, on)) } },
  };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("Network hub", () => {
  it("shows Cloudflare, Caddy and Nginx tabs only when they are on", async () => {
    mockApi({ ...base({ cloudflare: false, caddy: true, nginx: false }), "/api/v1/proxies/caddy": { status: 200, body: { instances: [], machines: [] } } });
    window.history.replaceState({}, "", "/network");
    render(<App />);
    const tabs = await screen.findByRole("group", { name: /network sections/i });
    expect(await within(tabs).findByRole("button", { name: "Caddy" })).toBeVisible();
    expect(within(tabs).queryByRole("button", { name: "Cloudflare" })).toBeNull();
    expect(within(tabs).queryByRole("button", { name: "Nginx" })).toBeNull();
    // Cloudflare is no longer its own sidebar entry.
    expect(screen.queryByRole("link", { name: /^cloudflare$/i })).toBeNull();
  });

  it("says a turned-off page was only hidden", async () => {
    mockApi(base({ cloudflare: false }));
    window.history.replaceState({}, "", "/cloudflare");
    render(<App />);
    expect(await screen.findByText(/^this feature is disabled$/i)).toBeVisible();
    expect(screen.getByText(/turning it off only hides it/i)).toBeVisible();
  });
});

describe("Caddy page", () => {
  it("groups sites by machine, marks No-dal sites and adds one with the validator's answer", async () => {
    let posted = "";
    mockApi({
      ...base({ caddy: true }),
      "/api/v1/proxies/caddy": {
        status: 200,
        body: {
          machines: [
            { id: "host", name: "Host", installed: true, can_apt: true },
            { id: "w2", name: "web", installed: false, can_apt: true },
          ],
          instances: [
            {
              kind: "caddy",
              machine_id: "host",
              machine_name: "Host",
              binary: "/usr/bin/caddy",
              version: "v2.6.2",
              service: "caddy",
              active: true,
              state: "active",
              config_path: "/etc/caddy/Caddyfile",
              managed_dir: "/etc/caddy/ndl.d",
              included: true,
              sites: [
                { domains: ["old.example.com"], upstream: "10.0.3.5:8080", tls: "auto", managed: false, file: "/etc/caddy/Caddyfile", line: 9 },
                { name: "new.example.com", domains: ["new.example.com"], upstream: "http://10.0.3.7:3000", tls: "internal", managed: true, file: "/etc/caddy/ndl.d/new.example.com.caddy", workload: { id: "w1", name: "vault" } },
              ],
            },
          ],
        },
      },
      "POST /api/v1/proxies/caddy/sites": (init) => {
        posted = String(init?.body);
        return { status: 422, body: { error: "caddy rejected the change, so nothing was changed: bad", output: "Error: adapting config" } };
      },
    });
    window.history.replaceState({}, "", "/network/caddy");
    render(<App />);
    const table = await screen.findByRole("table", { name: /caddy sites/i });
    expect(within(table).getByText("Host").closest("tr")).toHaveClass("cf-group");
    expect(within(table).getByText("No-dal")).toBeVisible();
    expect(within(table).getByText("/etc/caddy/Caddyfile:9")).toBeVisible();
    expect(within(table).getByRole("link", { name: "vault" })).toBeVisible();
    // File sites are read-only: their menu has no Edit.
    fireEvent.contextMenu(within(table).getByText("old.example.com").closest("tr")!);
    expect(screen.queryByRole("menuitem", { name: /^edit$/i })).toBeNull();
    fireEvent.keyDown(document, { key: "Escape" });

    fireEvent.click(screen.getByRole("button", { name: /^add site$/i }));
    const dlg = await screen.findByRole("dialog", { name: /add a caddy site/i });
    fireEvent.change(within(dlg).getByLabelText(/^domains$/i), { target: { value: "a.example.com, b.example.com" } });
    fireEvent.change(within(dlg).getByLabelText(/goes to/i), { target: { value: "10.0.3.9:80" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /^save$/i }));
    expect(await within(dlg).findByText(/adapting config/i)).toBeVisible();
    expect(JSON.parse(posted)).toMatchObject({ machine_id: "host", site: { domains: ["a.example.com", "b.example.com"], upstream: "10.0.3.9:80", tls: "auto" } });
  });
});

describe("Right-click menus", () => {
  it("leave text fields and empty page space to the browser's menu", async () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    expect(siteMenuFor(input)).toBeNull();
    input.remove();
    mockApi(base({}));
    render(<App />);
    await screen.findByRole("heading", { name: /dashboard/i });
    const ev = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 5, clientY: 5 });
    document.querySelector("main")!.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(false);
    expect(screen.queryByRole("menuitem", { name: /^reload$/i })).toBeNull();
  });
});

describe("Sidebar", () => {
  it("reorders entries within a category and saves the order to the profile", async () => {
    const saved: string[] = [];
    mockApi({
      ...base({}),
      "PATCH /api/v1/me": (init) => {
        saved.push(String(init?.body));
        return { status: 200, body: admin };
      },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: /appliance/i });
    const names = () => within(nav).getAllByRole("link").map((a) => a.getAttribute("aria-label"));
    expect(names().indexOf("Files")).toBeGreaterThan(names().indexOf("Workloads"));
    fireEvent.contextMenu(within(nav).getByRole("link", { name: "Files" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /^move up$/i }));
    fireEvent.contextMenu(within(nav).getByRole("link", { name: "Files" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /^move up$/i }));
    expect(names().indexOf("Files")).toBeLessThan(names().indexOf("Terminal"));
    expect(names().indexOf("Files")).toBeLessThan(names().indexOf("Workloads"));
    // Keyboard: Alt+ArrowDown moves it one place down.
    fireEvent.keyDown(within(nav).getByRole("link", { name: "Files" }), { key: "ArrowDown", altKey: true });
    expect(names().indexOf("Files")).toBe(names().indexOf("Workloads") + 1);
    await waitFor(() => expect(saved.some((b) => b.includes('"nav.order"'))).toBe(true), { timeout: 3000 });
  });

  it("switches to light mode from the account popup and previews themes", async () => {
    mockApi({ ...base({}), "PATCH /api/v1/me": { status: 200, body: admin } });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^admin menu$/i }));
    fireEvent.click(screen.getByRole("button", { name: /^light mode$/i }));
    expect(document.documentElement.dataset.mode).toBe("light");
    // Theme is a page now; the popup button opens it.
    fireEvent.click(screen.getByRole("button", { name: /^theme$/i }));
    expect(window.location.pathname).toBe("/theme");
    expect(await screen.findByRole("heading", { name: /^colours$/i })).toBeVisible();
  });
});

describe("Dashboard", () => {
  it("adds a card for each feature that is on", async () => {
    mockApi({
      ...base({ caddy: true, nginx: false, docker: false }),
      "/api/v1/proxies/caddy": { status: 200, body: { machines: [], instances: [{ machine_id: "host", machine_name: "Host", active: true, sites: [{ domains: ["a"], managed: true }] }] } },
    });
    render(<App />);
    const features = await screen.findByRole("heading", { name: /^features$/i });
    const section = features.closest("section")!;
    expect(await within(section).findByText("1/1 running")).toBeVisible();
    expect(within(section).getByText("Caddy")).toBeVisible();
    expect(within(section).queryByText("Nginx")).toBeNull();
  });
});
