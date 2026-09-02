import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import type { GetHealthPath } from "./generated/openapi";
import type { MeResponse } from "./api/types";

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["admin"],
  edition: "ce",
};

function jsonResponse(status: number, body?: unknown): Response {
  if (body === undefined) {
    return new Response(null, { status });
  }
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mockApi(routes: Record<string, { status: number; body?: unknown }>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url =
      typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (
      typeof input !== "string" && !(input instanceof URL) ? input.method : init?.method
    )?.toUpperCase() || "GET";
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${method} ${path}` });
    }
    return jsonResponse(hit.status, hit.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const defaultRoutes = {
  "/api/v1/health": {
    status: 200,
    body: { status: "ok", service: "ndl-control" },
  },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/me": { status: 401 },
  "/api/v1/nodes": { status: 200, body: { items: [] } },
  "/api/v1/events": { status: 200, body: { items: [] } },
  "/api/v1/tasks": { status: 200, body: { items: [] } },
  "/api/v1/storage/pools": { status: 200, body: { items: [] } },
  "/api/v1/storage/volumes": { status: 200, body: { items: [] } },
  "/api/v1/storage/images": { status: 200, body: { items: [] } },
  "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
  try {
    localStorage.clear();
  } catch {
    // jsdom may not expose Storage after global stubs are restored.
  }
});

describe("App", () => {
  it("renders the setup form", async () => {
    window.history.replaceState({}, "", "/setup");
    mockApi({
      ...defaultRoutes,
      "/api/v1/setup/status": { status: 200, body: { open: true } },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /create the first administrator/i })).toBeVisible();
    expect(screen.getByLabelText(/setup token/i)).toBeVisible();
    expect(screen.getByLabelText(/^username$/i)).toBeVisible();
    expect(screen.getByLabelText(/^password$/i)).toBeVisible();
    expect(screen.getByLabelText(/confirm password/i)).toBeVisible();
    expect(screen.queryByRole("heading", { name: /ci works/i })).not.toBeInTheDocument();
  });

  it("renders the login form", async () => {
    window.history.replaceState({}, "", "/login");
    mockApi(defaultRoutes);

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^sign in$/i })).toBeVisible();
    expect(screen.getByLabelText(/^username$/i)).toBeVisible();
    expect(screen.getByLabelText(/^password$/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /^sign in$/i })).toBeVisible();
  });

  it("shows the local node without fake infrastructure data", async () => {
    window.history.replaceState({}, "", "/");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": {
        status: 200,
        body: {
          items: [
            {
              id: "node-1",
              name: "local",
              status: "available",
              host_os: "Debian GNU/Linux 13 (trixie)",
              cpu_model: "Test CPU",
              cpu_cores: 4,
              cpu_threads: 8,
              memory_bytes: 8 * 1024 * 1024 * 1024,
              disk_count: 1,
              disk_bytes: 20 * 1024 * 1024 * 1024,
              nic_count: 1,
              gpu_present: false,
              gpu_count: 0,
            },
          ],
        },
      },
      "/api/v1/events": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/nodes/node-1/metrics": {
        status: 200,
        body: { status: "collecting", series: [] },
      },
    });

    const { container } = render(<App />);

    expect(await screen.findByRole("heading", { name: /dashboard/i })).toBeVisible();
    expect(await screen.findByText(/debian gnu\/linux 13/i)).toBeVisible();
    expect(screen.getByText(/none detected/i)).toBeVisible();
    expect(screen.getAllByText(/collecting data/i).length).toBeGreaterThan(0);
    expect(screen.getByRole("navigation", { name: /appliance/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^workloads$/i })).toBeVisible();
    expect(screen.queryByRole("link", { name: /^kubernetes$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^store$/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/license activation is not required/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/configuration level/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^admin$/i }));
    expect(screen.getByRole("menuitem", { name: /log out/i })).toBeVisible();
    expect(screen.queryByText(/ci works/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/21 workloads/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/12 running/i)).not.toBeInTheDocument();
    expect(screen.getByText(/no usable storage pool yet/i)).toBeVisible();

    const text = container.textContent ?? "";
    expect(text).not.toMatch(/192\.168\./);
    expect(container.querySelector("svg.metric-chart")).toBeNull();
    expect(text).not.toMatch(/Phase \d+/);
  });

  it("shows a 404 for unknown routes", async () => {
    window.history.replaceState({}, "", "/no-such-page");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /not found/i })).toBeVisible();
  });

  it("shows the current user on /me", async () => {
    window.history.replaceState({}, "", "/me");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /account/i })).toBeVisible();
    expect(screen.getByText("Administrator")).toBeVisible();
    expect(screen.getByText("Community Edition")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^details$/i }));
    expect(screen.getByText("user-1")).toBeVisible();
    expect(screen.getByLabelText(/configuration level/i)).toBeVisible();
  });

  it("posts setup claim JSON and opens the dashboard", async () => {
    window.history.replaceState({}, "", "/setup");
    const fetchMock = mockApi({
      ...defaultRoutes,
      "/api/v1/setup/status": { status: 200, body: { open: true } },
      "/api/v1/setup/claim": { status: 200, body: admin },
    });

    render(<App />);
    await screen.findByLabelText(/setup token/i);

    fireEvent.change(screen.getByLabelText(/setup token/i), {
      target: { value: "setup-token" },
    });
    fireEvent.change(screen.getByLabelText(/^username$/i), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText(/^password$/i), {
      target: { value: "secret-pass" },
    });
    fireEvent.change(screen.getByLabelText(/confirm password/i), {
      target: { value: "secret-pass" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create administrator/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/setup/claim",
        expect.objectContaining({
          method: "POST",
          credentials: "include",
          body: JSON.stringify({
            token: "setup-token",
            username: "admin",
            password: "secret-pass",
          }),
        }),
      );
    });
    expect(await screen.findByRole("heading", { name: /dashboard/i })).toBeVisible();
  });

  it("shows storage pools with locator, capacity, and create form", async () => {
    window.history.replaceState({}, "", "/storage");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/storage/pools": {
        status: 200,
        body: {
          default_path: "/var/lib/ndl/storage/local",
          items: [
            {
              id: "pool-1",
              name: "local",
              backend_type: "directory",
              status: "warning",
              locator: "/var/lib/ndl/storage/local",
              usable_bytes: 40 * 1024 * 1024 * 1024,
              allocated_bytes: 2 * 1024 * 1024 * 1024,
              provisioned_bytes: 10 * 1024 * 1024 * 1024,
              storage_classes: ["vm-disk", "iso"],
              capabilities: { incremental_send: false, snapshots: false },
              warning_text: [
                "This Directory pool shares the host root filesystem. Filling it can fill the host and destabilize No-dal.",
              ],
            },
          ],
        },
      },
      "/api/v1/storage/volumes": { status: 200, body: { items: [] } },
      "/api/v1/storage/images": { status: 200, body: { items: [] } },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^storage$/i })).toBeVisible();
    expect(await screen.findByRole("heading", { name: /create directory pool/i })).toBeVisible();
    expect(
      (await screen.findAllByText(/filling it can fill the host and destabilize no-dal/i)).length,
    ).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "local" })).toBeVisible();
    expect(screen.getAllByText(/unavailable/i).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: /create directory pool/i })).toBeVisible();
    expect(screen.queryByText(/^local \(warning\)$/i)).not.toBeInTheDocument();
  });

  it("shows isolated network first-run and create form", async () => {
    window.history.replaceState({}, "", "/network");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/networks": {
        status: 200,
        body: {
          first_run: true,
          items: [],
          nics: [{ name: "eth0", ifindex: 2, state: "up", addresses: ["192.168.1.10/24"] }],
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^network$/i })).toBeVisible();
    expect(await screen.findByRole("heading", { name: /first-run guest network/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /create network/i })).toBeVisible();
    expect(screen.getByText(/eth0/)).toBeVisible();
    expect(screen.getByText("2")).toBeVisible();
    expect(screen.getByText(/192\.168\.1\.10/)).toBeVisible();
  });

  it("renders the workloads route without fake counts", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^workloads$/i })).toBeVisible();
    expect(await screen.findByText(/no system containers yet/i)).toBeVisible();
    expect(screen.queryByText(/21 workloads/i)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /create system container/i })).toBeVisible();
  });

  it("creates a system container with a human-readable OS picker", async () => {
    window.history.replaceState({}, "", "/workloads/new/system-container");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/storage/pools": {
        status: 200,
        body: {
          items: [
            {
              id: "pool-1",
              name: "local",
              backend_type: "directory",
              status: "warning",
              usable_bytes: 139 * 1024 * 1024 * 1024,
              warning_text: ["This Directory pool shares the host root filesystem."],
              capabilities: { snapshots: false },
            },
          ],
        },
      },
      "/api/v1/networks": {
        status: 200,
        body: { items: [{ id: "net-1", name: "isolated", kind: "isolated", status: "available", dhcp: true }] },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /create system container/i })).toBeVisible();
    expect(screen.getByLabelText(/configuration level/i)).toBeVisible();
    expect(await screen.findByLabelText(/operating system/i)).toBeVisible();
    expect(screen.getAllByText(/alpine linux 3.21/i).length).toBeGreaterThan(0);
    expect(screen.queryByText(/alpine\/3.21\/amd64\/default/i)).not.toBeInTheDocument();
    expect(await screen.findByText(/139/i)).toBeVisible();
    expect(screen.getByText(/snapshots unavailable/i)).toBeVisible();
    expect(screen.getByText(/shares the host root filesystem/i)).toBeVisible();
    expect(screen.queryByText(/local \(warning\)/i)).not.toBeInTheDocument();
  });

  it("imports generated OpenAPI path types", () => {
    const path: GetHealthPath = "/api/v1/health";
    expect(path).toBe("/api/v1/health");
  });

  it("renders workload files without inventing a session", async () => {
    window.history.replaceState({}, "", "/workloads/wl-1/files");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/wl-1": {
        status: 200,
        body: { id: "wl-1", name: "accept-ct", kind: "system-container", status: "running" },
      },
      "/api/v1/workloads/wl-1/files": {
        status: 200,
        body: { path: "/", entries: [{ name: "etc", type: "dir", size: 0, path: "etc" }] },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^files$/i })).toBeVisible();
    expect(await screen.findByText("etc")).toBeVisible();
    expect(screen.getByText(/upload here/i)).toBeVisible();
    expect(screen.queryByText(/fake session/i)).not.toBeInTheDocument();
  });

  it("says VM terminal is unsupported", async () => {
    window.history.replaceState({}, "", "/workloads/vm-1/terminal");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/vm-1": {
        status: 200,
        body: { id: "vm-1", name: "win", kind: "vm", status: "stopped" },
      },
    });

    render(<App />);

    expect(await screen.findByText(/not available for this workload type/i)).toBeVisible();
    expect(screen.queryByText(/phase 20/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/connected/i)).not.toBeInTheDocument();
  });

  it("omits console and backups tabs on a system container and explains snapshots", async () => {
    window.history.replaceState({}, "", "/workloads/wl-1/snapshots");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/wl-1": {
        status: 200,
        body: { id: "wl-1", name: "web-01", kind: "system-container", status: "running" },
      },
      "/api/v1/storage/pools": {
        status: 200,
        body: {
          items: [{ id: "pool-1", name: "local", backend_type: "directory", status: "available", capabilities: { snapshots: false } }],
        },
      },
      "/api/v1/storage/pools/pool-1": {
        status: 200,
        body: { id: "pool-1", name: "local", backend_type: "directory", status: "available", capabilities: { snapshots: false } },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /web-01/i })).toBeVisible();
    expect(screen.getAllByRole("link", { name: /^snapshots$/i }).length).toBeGreaterThan(0);
    expect(screen.queryByRole("link", { name: /^console$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^backups$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^metrics$/i })).not.toBeInTheDocument();
    expect(await screen.findByText(/directory storage/i)).toBeVisible();
    expect(screen.getByText(/snapshot-capable pool/i)).toBeVisible();
  });

  it("posts the same create body from Guided and Expert", async () => {
    window.history.replaceState({}, "", "/workloads/new/system-container");
    const fetchMock = mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/storage/pools": {
        status: 200,
        body: {
          items: [
            {
              id: "pool-1",
              name: "local",
              backend_type: "directory",
              status: "available",
              usable_bytes: 10 * 1024 * 1024 * 1024,
              capabilities: { snapshots: false },
            },
          ],
        },
      },
      "/api/v1/networks": {
        status: 200,
        body: { items: [{ id: "net-1", name: "isolated", kind: "isolated", status: "available", dhcp: true }] },
      },
      "POST /api/v1/workloads": { status: 400, body: { error: "hold" } },
    });

    render(<App />);
    await screen.findByLabelText(/operating system/i);
    expect(await screen.findByText(/dhcp on/i)).toBeVisible();
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "web-01" } });
    fireEvent.click(screen.getByRole("button", { name: /create system container/i }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some((call) => String(call[0]).includes("/workloads") && call[1]?.method === "POST")).toBe(
        true,
      );
    });
    const guided = fetchMock.mock.calls.find(
      (call) => String(call[0]).includes("/workloads") && call[1]?.method === "POST",
    );
    fireEvent.click(screen.getByRole("button", { name: /^expert$/i }));
    fireEvent.click(screen.getByRole("button", { name: /create system container/i }));
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter((call) => String(call[0]).includes("/workloads") && call[1]?.method === "POST").length,
      ).toBeGreaterThan(1);
    });
    const expert = fetchMock.mock.calls
      .filter((call) => String(call[0]).includes("/workloads") && call[1]?.method === "POST")
      .at(-1);
    expect(guided?.[1]?.body).toBe(expert?.[1]?.body);
    expect(localStorage.getItem("ndl-ux-level")).toBeNull();
  });

  it("shows in-flight tasks in the header activity control", async () => {
    window.history.replaceState({}, "", "/");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/tasks": {
        status: 200,
        body: {
          items: [
            { id: "t-1", kind: "workload.create", state: "running", progress: 40, message: "Pulling image" },
            { id: "t-2", kind: "network.apply", state: "failed", message: "Uplink is down" },
          ],
        },
      },
    });

    render(<App />);
    expect(await screen.findByRole("button", { name: /1 running/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /1 running/i }));
    expect(screen.getAllByText(/pulling image/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/uplink is down/i).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: /view all/i })).toBeVisible();
  });

  it("disables create for viewers with a role reason", async () => {
    window.history.replaceState({}, "", "/workloads/new/system-container");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: { ...admin, roles: ["viewer"] } },
    });

    render(<App />);
    expect(await screen.findByRole("heading", { name: /create system container/i })).toBeVisible();
    expect(screen.getByText(/requires operator or administrator/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /create system container/i })).toBeDisabled();
  });
});
