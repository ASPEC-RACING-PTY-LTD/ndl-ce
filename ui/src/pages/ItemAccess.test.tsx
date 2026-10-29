import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

type Route = { status: number; body?: unknown };

const roleless: MeResponse = {
  user_id: "user-9",
  username: "guest",
  roles: [],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
  grants: ["identity.read", "identity.mfa"],
  item_access: { workload: ["compute.read", "compute.lifecycle", "terminal.open"] },
  item_grants: [{ kind: "workload", id: "wl-a", permissions: ["compute.read", "compute.lifecycle", "terminal.open"] }],
  ui_prefs: { "layout.nav": "modern", "layout.instance": "instance" },
};

const admin: MeResponse = {
  ...roleless,
  user_id: "user-1",
  username: "admin",
  roles: ["superadmin"],
  grants: ["*"],
  item_access: {},
  item_grants: [],
};

const workload = { id: "wl-a", name: "AspecRacing", kind: "system-container", status: "stopped" };

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body ?? {}), { status, headers: { "Content-Type": "application/json" } });
}

/**
 * mockApi answers like the server does for this person: their workload and
 * own account; anything else is refused (403) for a person without a role.
 */
function mockApi(me: MeResponse, extra: Record<string, Route> = {}) {
  const routes: Record<string, Route> = {
    "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
    "/api/v1/setup/status": { status: 200, body: { open: false } },
    "/api/v1/me": { status: 200, body: me },
    "/api/v1/nodes": { status: 200, body: { items: [] } },
    "/api/v1/workloads": { status: 200, body: { items: [workload] } },
    "/api/v1/workloads/wl-a": { status: 200, body: workload },
    "/api/v1/workloads/wl-a/metrics": { status: 200, body: { status: "ok", series: [] } },
    ...extra,
  };
  const refused = me.roles.length === 0;
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return refused ? jsonResponse(403, { error: "forbidden" }) : jsonResponse(404, { error: `unmocked ${path}` });
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

describe("a person whose only access is an item grant", () => {
  it("sees the controls the grant allows on the workload page, and no delete", async () => {
    mockApi(roleless);
    window.history.replaceState({}, "", "/workloads/wl-a");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "AspecRacing" })).toBeVisible();
    expect(screen.getByRole("button", { name: /^start$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^stop$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^restart$/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^delete$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^edit$/i })).not.toBeInTheDocument();
    // The workload's own bar offers the terminal it may open, not Storage or Files.
    const tabs = screen.getByRole("navigation", { name: "Workload IO" });
    expect(within(tabs).getByRole("link", { name: /terminal/i })).toBeVisible();
    expect(within(tabs).queryByRole("link", { name: /storage/i })).not.toBeInTheDocument();
    expect(within(tabs).queryByRole("link", { name: /files/i })).not.toBeInTheDocument();
    // Refused side requests do not turn into an error banner.
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("sees no power controls when the grant only lets them view", async () => {
    mockApi({
      ...roleless,
      item_access: { workload: ["compute.read"] },
      item_grants: [{ kind: "workload", id: "wl-a", permissions: ["compute.read"] }],
    });
    window.history.replaceState({}, "", "/workloads/wl-a");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "AspecRacing" })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^start$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^restart$/i })).not.toBeInTheDocument();
    const tabs = screen.getByRole("navigation", { name: "Workload IO" });
    expect(within(tabs).queryByRole("link", { name: /terminal/i })).not.toBeInTheDocument();
  });

  it("gets a sidebar with only the areas holding their items", async () => {
    mockApi(roleless);
    window.history.replaceState({}, "", "/workloads/wl-a");
    render(<App />);
    await screen.findByRole("heading", { name: "AspecRacing" });
    const sidebar = screen.getByRole("navigation", { name: "Appliance" });
    expect(within(sidebar).getByRole("link", { name: "Instance" })).toBeVisible();
    expect(within(sidebar).getByRole("link", { name: "Terminal" })).toBeVisible();
    expect(within(sidebar).queryByRole("link", { name: "Dashboard" })).not.toBeInTheDocument();
    expect(within(sidebar).queryByRole("link", { name: "Files" })).not.toBeInTheDocument();
    expect(within(sidebar).queryByRole("link", { name: "Administration" })).not.toBeInTheDocument();
  });

  it("lands on their workloads instead of the Dashboard", async () => {
    mockApi(roleless);
    window.history.replaceState({}, "", "/");
    render(<App />);
    expect(await screen.findByRole("link", { name: "AspecRacing" })).toBeVisible();
    expect(window.location.pathname).toBe("/instance");
  });

  it("is shown the no-access page for areas they hold nothing in", async () => {
    mockApi(roleless);
    window.history.replaceState({}, "", "/storage");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Forbidden" })).toBeVisible();
  });
});

describe("an admin on the same workload page", () => {
  it("keeps every control, delete included", async () => {
    mockApi(admin, { "/api/v1/features": { status: 200, body: { items: [] } } });
    window.history.replaceState({}, "", "/workloads/wl-a");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "AspecRacing" })).toBeVisible();
    for (const name of [/^edit$/i, /^start$/i, /^stop$/i, /^restart$/i, /^delete$/i]) {
      expect(screen.getByRole("button", { name })).toBeVisible();
    }
    const sidebar = screen.getByRole("navigation", { name: "Appliance" });
    expect(within(sidebar).getByRole("link", { name: "Dashboard" })).toBeVisible();
    expect(within(sidebar).getByRole("link", { name: "Files" })).toBeVisible();
    expect(within(sidebar).getByRole("link", { name: "Administration" })).toBeVisible();
  });
});
