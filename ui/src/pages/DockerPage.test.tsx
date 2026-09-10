import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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

const baseRoutes = {
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

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Docker page", () => {
  it("explains the feature is off when the API returns 404", async () => {
    mockApi({
      ...baseRoutes,
      "/api/v1/features": { status: 200, body: { items: [] } },
      "/api/v1/docker": { status: 404, body: { error: "Docker Management is not enabled. Enable it from Add Features." } },
    });
    window.history.replaceState({}, "", "/docker");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^docker$/i })).toBeVisible();
    expect(await screen.findByText(/docker management is not enabled/i)).toBeVisible();
    expect(screen.getAllByRole("link", { name: /add features/i }).length).toBeGreaterThan(0);
  });

  it("renders hierarchy, update-failed label, and restart", async () => {
    const posted: string[] = [];
    mockApi({
      ...baseRoutes,
      "/api/v1/docker": {
        status: 200,
        body: {
          summary: { machines: 1, projects: 2, containers: 2, running: 2, stopped: 0, healthy: 0, degraded: 1, critical: 0, daemons_down: 0, update_failed: 1 },
          machines: [
            {
              id: "host",
              name: "AspecRacing",
              kind: "lxc",
              daemon_ok: true,
              health: "degraded",
              health_reason: "Update failed",
              container_count: 2,
              projects: [
                {
                  id: "host/shop",
                  machine_id: "host",
                  name: "shop",
                  working_dir: "/srv/shop",
                  health: "degraded",
                  status_label: "Running · Update Failed",
                  running: 1,
                  containers: [
                    {
                      id: "host/abc123abc123",
                      machine_id: "host",
                      machine_name: "AspecRacing",
                      container_id: "abc123abc123",
                      name: "shop-web-1",
                      service: "web",
                      project: "shop",
                      working_dir: "/srv/shop",
                      image: "nginx:alpine",
                      state: "running",
                      health: "degraded",
                      status_label: "Running · Update Failed",
                      restart_count: 1,
                    },
                  ],
                },
                {
                  id: "host/aspecracing",
                  machine_id: "host",
                  name: "aspecracing",
                  working_dir: "/root/aspecracing",
                  health: "healthy",
                  status_label: "Running",
                  running: 1,
                  containers: [
                    {
                      id: "host/def456def456",
                      machine_id: "host",
                      machine_name: "AspecRacing",
                      container_id: "def456def456",
                      name: "aspecracing-admin-1",
                      service: "admin",
                      project: "aspecracing",
                      working_dir: "/root/aspecracing",
                      image: "aspecracing-admin",
                      state: "running",
                      health: "healthy",
                      status_label: "Running",
                      restart_count: 0,
                    },
                  ],
                },
              ],
            },
          ],
          projects: [],
          containers: [
            {
              id: "host/abc123abc123",
              machine_id: "host",
              machine_name: "AspecRacing",
              container_id: "abc123abc123",
              name: "shop-web-1",
              service: "web",
              project: "shop",
              working_dir: "/srv/shop",
              image: "nginx:alpine",
              state: "running",
              health: "degraded",
              status_label: "Running · Update Failed",
              restart_count: 1,
            },
            {
              id: "host/def456def456",
              machine_id: "host",
              machine_name: "AspecRacing",
              container_id: "def456def456",
              name: "aspecracing-admin-1",
              service: "admin",
              project: "aspecracing",
              working_dir: "/root/aspecracing",
              image: "aspecracing-admin",
              state: "running",
              health: "healthy",
              status_label: "Running",
              restart_count: 0,
            },
          ],
        },
      },
      "POST /api/v1/docker/machines/host/containers/abc123abc123/actions": (init) => {
        posted.push(String(init?.body ?? ""));
        return { status: 200, body: { ok: true, message: "restart" } };
      },
    });
    window.history.replaceState({}, "", "/docker");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^docker$/i })).toBeVisible();
    expect(await screen.findByText(/shop-web-1/i)).toBeVisible();
    expect(screen.getByText(/aspecracing-admin-1/i)).toBeVisible();
    expect(screen.getAllByRole("columnheader", { name: /^service$/i })).toHaveLength(1);
    expect(screen.getAllByRole("columnheader", { name: /^status$/i })).toHaveLength(1);
    expect(screen.getAllByRole("columnheader", { name: /^image$/i })).toHaveLength(1);
    expect(document.querySelectorAll(".docker-table").length).toBe(1);
    expect(screen.getAllByText(/running · update failed/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/\/srv\/shop/)).toBeVisible();
    fireEvent.click(screen.getAllByRole("button", { name: /more actions/i })[0]);
    fireEvent.click(screen.getByRole("menuitem", { name: /^restart$/i }));
    await waitFor(() => expect(posted.some((b) => b.includes("restart"))).toBe(true));
  });

  it("pins docker column tracks in CSS so names cannot shift the grid", () => {
    const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../styles.css"), "utf8");
    expect(css).toContain("table-layout: fixed");
    expect(css).toContain(".docker-table .docker-col-service");
    expect(css).toContain(".docker-table .docker-col-status");
    expect(css).toContain(".docker-table .docker-col-image");
    expect(css).toContain(".docker-machine-head,\n.docker-project-head");
  });
});
