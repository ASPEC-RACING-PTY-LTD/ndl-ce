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
      items: [{ id: "oci", title: "OCI", enabled: true, core: false, package_status: "installed", runtime_status: "not_started", starts_runtime: false, kubelet_started: false, workload_count: 0 }],
    },
  },
};


const web = {
  id: "wl-web",
  name: "web",
  kind: "oci",
  status: "running",
  image_pin: "nginx:alpine",
  spec: { network_mode: "bridge", ipv4_address: "192.168.1.50/24", ports: [{ container_port: 80, host_port: 8080 }] },
  nics: [],
};
const db = { id: "wl-db", name: "db", kind: "oci", status: "stopped", image_pin: "postgres:16", spec: {}, nics: [] };
const ct = { id: "wl-ct", name: "box", kind: "system-container", status: "running" };

function ociRoutes(extra: Parameters<typeof mockApi>[0] = {}) {
  return {
    ...baseRoutes,
    "/api/v1/workloads": { status: 200, body: { items: [web, db, ct] } },
    "/api/v1/stacks": {
      status: 200,
      body: {
        items: [
          {
            id: "g1",
            name: "shop",
            status: "applied",
            members: [{ id: "m1", service_name: "web", status: "ready", workload_id: "wl-web" }],
          },
        ],
      },
    },
    "/api/v1/registries": { status: 200, body: { items: [] } },
    "/api/v1/storage/volumes": { status: 200, body: { items: [] } },
    "/api/v1/networks": {
      status: 200,
      body: { items: [{ id: "net-1", name: "lan", kind: "bridge", status: "available", ipv4_cidr: "192.168.1.0/24", gateway: "192.168.1.1" }], nics: [] },
    },
    ...extra,
  };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("OCI Containers page", () => {
  it("shows groups and ungrouped containers, and replaces the Stacks entry", async () => {
    mockApi(ociRoutes());
    window.history.replaceState({}, "", "/oci");
    render(<App />);
    const group = await screen.findByRole("article", { name: /group shop/i });
    expect(within(group).getByText("web")).toBeVisible();
    expect(within(group).getByText(/192\.168\.1\.50/)).toBeVisible();
    const loose = screen.getByRole("article", { name: /ungrouped containers/i });
    expect(within(loose).getByText("db")).toBeVisible();
    expect(screen.queryByText("box")).toBeNull();
    expect(screen.queryByRole("link", { name: /^stacks$/i })).toBeNull();
  });

  it("moves a container into a group and stops a whole group", async () => {
    const posted: { path: string; body: string }[] = [];
    mockApi(
      ociRoutes({
        "POST /api/v1/stacks/g1/members": (init) => {
          posted.push({ path: "members", body: String(init?.body ?? "") });
          return { status: 200, body: { id: "g1", name: "shop", status: "applied", members: [] } };
        },
        "POST /api/v1/stacks/g1/power": (init) => {
          posted.push({ path: "power", body: String(init?.body ?? "") });
          return { status: 200, body: { failed: 0, results: [{ workload_id: "wl-web", name: "web", ok: true }] } };
        },
      }),
    );
    window.history.replaceState({}, "", "/oci");
    render(<App />);
    const loose = await screen.findByRole("article", { name: /ungrouped containers/i });
    fireEvent.contextMenu(within(loose).getByText("db"));
    fireEvent.click(await screen.findByRole("menuitem", { name: /move to group/i }));
    fireEvent.change(await screen.findByLabelText(/^group$/i), { target: { value: "g1" } });
    fireEvent.click(screen.getByRole("button", { name: /^move$/i }));
    await waitFor(() => expect(posted.find((p) => p.path === "members")?.body).toContain("wl-db"));

    fireEvent.click(screen.getByRole("button", { name: /shop group actions/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /^stop all$/i }));
    await waitFor(() => expect(posted.find((p) => p.path === "power")?.body).toContain("stop"));
  });

  it("sends old Stacks links to the OCI page", async () => {
    mockApi(ociRoutes());
    window.history.replaceState({}, "", "/stacks/g1");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /oci containers/i })).toBeVisible();
    expect(window.location.pathname).toBe("/oci");
  });

  it("creates a container with a fixed IP, ports and environment in a group", async () => {
    const bodies: Record<string, string> = {};
    mockApi(
      ociRoutes({
        "POST /api/v1/workloads": (init) => {
          bodies.create = String(init?.body ?? "");
          return { status: 201, body: { id: "wl-new", name: "api", kind: "oci", status: "collecting" } };
        },
        "POST /api/v1/stacks/g1/members": (init) => {
          bodies.group = String(init?.body ?? "");
          return { status: 200, body: { id: "g1", name: "shop", members: [] } };
        },
      }),
    );
    window.history.replaceState({}, "", "/oci");
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^new container$/i }));
    const dialog = await screen.findByRole("dialog", { name: /new oci container/i });
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: "api" } });
    fireEvent.change(within(dialog).getByLabelText(/^image$/i), { target: { value: "ghcr.io/acme/api:1" } });
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "shop" })).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText(/^group$/i), { target: { value: "g1" } });
    await waitFor(() => expect((within(dialog).getByLabelText(/^network$/i) as HTMLSelectElement).value).toBe("net-1"));
    fireEvent.change(within(dialog).getByLabelText(/^ip address$/i), { target: { value: "static" } });
    fireEvent.change(within(dialog).getByLabelText(/^address$/i), { target: { value: "192.168.1.60/24" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /add port/i }));
    fireEvent.change(within(dialog).getByLabelText(/container port 1/i), { target: { value: "3000" } });
    fireEvent.change(within(dialog).getByLabelText(/host port 1/i), { target: { value: "8081" } });
    fireEvent.change(within(dialog).getByLabelText(/environment variables/i), { target: { value: "NODE_ENV=production" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /create container/i }));
    await waitFor(() => expect(bodies.group).toContain("wl-new"));
    const sent = JSON.parse(bodies.create);
    expect(sent).toMatchObject({
      name: "api",
      kind: "oci",
      image_pin: "ghcr.io/acme/api:1",
      network_mode: "bridge",
      network_id: "net-1",
      ipv4_address: "192.168.1.60/24",
      ports: [{ container_port: 3000, host_port: 8081, protocol: "tcp" }],
      env: [{ name: "NODE_ENV", value: "production" }],
    });
  });
});
