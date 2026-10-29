import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["superadmin"],
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


const inventory = {
  summary: { machines: 1, projects: 1, containers: 2, running: 1, stopped: 1, healthy: 1, degraded: 1, critical: 0, daemons_down: 0, update_failed: 0 },
  machines: [
    {
      id: "ct-1",
      name: "apps",
      kind: "system-container",
      daemon_ok: true,
      health: "degraded",
      container_count: 2,
      projects: [
        {
          id: "ct-1/shop",
          machine_id: "ct-1",
          name: "shop",
          working_dir: "/srv/shop",
          health: "degraded",
          status_label: "Degraded",
          running: 1,
          containers: [
            { id: "ct-1/w1", machine_id: "ct-1", machine_name: "apps", container_id: "w1", name: "shop-web-1", project: "shop", state: "exited", health: "degraded", status_label: "Exited (1)" },
            { id: "ct-1/d1", machine_id: "ct-1", machine_name: "apps", container_id: "d1", name: "shop-db-1", project: "shop", state: "running", health: "healthy", status_label: "Running" },
          ],
        },
      ],
    },
  ],
  projects: [],
  containers: [
    { id: "ct-1/w1", machine_id: "ct-1", machine_name: "apps", container_id: "w1", name: "shop-web-1", project: "shop", state: "exited", health: "degraded", status_label: "Exited (1)" },
    { id: "ct-1/d1", machine_id: "ct-1", machine_name: "apps", container_id: "d1", name: "shop-db-1", project: "shop", state: "running", health: "healthy", status_label: "Running" },
  ],
};

const tunnel = {
  machine_id: "ct-1",
  machine_name: "apps",
  unit: "cloudflared.service",
  active: true,
  state: "active",
  sub_state: "running",
  remote_managed: true,
  error_count: 12,
  health: "degraded",
  reason: "routes to containers that are down or unhealthy: shop-web-1",
  links: [{ origin: "http://localhost:8080", hostnames: ["shop.example.com"], container_id: "w1", id: "ct-1/w1", name: "shop-web-1", state: "exited", health: "degraded", errors: 12 }],
  errors: [{ origin: "http://localhost:8080", hostname: "shop.example.com", message: "connection refused", count: 12 }],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

describe("Docker bulk tools", () => {
  it("fixes unhealthy containers as a job, leaves tunnels to Cloudflare, and tests a JSON script", async () => {
    const posted: Record<string, string> = {};
    let polls = 0;
    mockApi({
      ...baseRoutes,
      "/api/v1/features": {
        status: 200,
        body: { items: [{ id: "docker", title: "Docker Management", enabled: true, core: false, package_status: "installed", runtime_status: "not_started", starts_runtime: false, kubelet_started: false, workload_count: 0 }] },
      },
      "/api/v1/docker": { status: 200, body: inventory },
      "GET /api/v1/docker/prefs": { status: 200, body: { items: [] } },
      "/api/v1/docker/tunnels": { status: 200, body: { supported: true, items: [tunnel] } },
      "GET /api/v1/scripts": { status: 200, body: { items: [] } },
      "POST /api/v1/docker/bulk": (init) => {
        posted.bulk = String(init?.body ?? "");
        return { status: 202, body: { id: "job-1", kind: "bulk", name: "Fix unhealthy", status: "running", started_at: "2026-10-16T00:00:00Z", failed: 0, lines: [] } };
      },
      "/api/v1/ops/jobs/job-1": () => {
        polls += 1;
        return {
          status: 200,
          body: {
            id: "job-1",
            kind: "bulk",
            name: "Fix unhealthy",
            status: polls > 1 ? "succeeded" : "running",
            started_at: "2026-10-16T00:00:00Z",
            failed: 0,
            lines: [
              { at: "2026-10-16T00:00:01Z", level: "ok", message: "Restart shop-db-1 on apps" },
              { at: "2026-10-16T00:00:02Z", level: "ok", message: "Start shop-web-1 on apps" },
            ],
          },
        };
      },
      "POST /api/v1/docker/script": (init) => {
        posted.script = String(init?.body ?? "");
        return {
          status: 200,
          body: { id: "t", kind: "test", name: "x", status: "running", started_at: "", failed: 0, valid: true, lines: [{ at: "", level: "plan", message: "Would restart shop-db-1 on apps" }] },
        };
      },
    });
    window.history.replaceState({}, "", "/docker");
    render(<App />);
    await screen.findByRole("row", { name: /container shop-web-1/i });
    // Tunnels belong to the Cloudflare page.
    expect(screen.queryByText(/cloudflared\.service|cloudflare tunnels|shop\.example\.com/i)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /^fix unhealthy$/i }));
    await waitFor(() => expect(posted.bulk).toContain('"action":"fix"'));
    expect(await screen.findByText(/start shop-web-1 on apps/i)).toBeVisible();
    await waitFor(() => expect(screen.getByRole("article", { name: /fix unhealthy progress/i })).toHaveTextContent(/done/i));

    fireEvent.click(screen.getByRole("button", { name: /more docker actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /^run json script$/i }));
    const dialog = await screen.findByRole("dialog", { name: /run a docker script/i });
    fireEvent.click(within(dialog).getByRole("button", { name: /^test$/i }));
    expect(await within(dialog).findByText(/would restart shop-db-1/i)).toBeVisible();
    expect(JSON.parse(posted.script).dry_run).toBe(true);

    fireEvent.change(within(dialog).getByLabelText(/^json$/i), { target: { value: "{ not json" } });
    expect(within(dialog).getByText(/not valid json yet/i)).toBeVisible();
    expect(within(dialog).getByRole("button", { name: /run once/i })).toBeDisabled();
  });
});
