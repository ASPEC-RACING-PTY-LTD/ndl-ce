import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const hit = routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
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
  "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
  "/api/v1/storage/images": { status: 200, body: { items: [] } },
};

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
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
    expect(screen.getByText(/community edition\. license activation is not required\./i)).toBeVisible();
    expect(screen.getByRole("button", { name: /log out/i })).toBeVisible();
    expect(screen.queryByText(/ci works/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/21 workloads/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/12 running/i)).not.toBeInTheDocument();
    expect(screen.getByText(/no usable storage pool yet/i)).toBeVisible();

    const text = container.textContent ?? "";
    expect(text).not.toMatch(/192\.168\./);
    expect(container.querySelector("svg.metric-chart")).toBeNull();
  });

  it("shows the current user on /me", async () => {
    window.history.replaceState({}, "", "/me");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /account/i })).toBeVisible();
    expect(screen.getByText("user-1")).toBeVisible();
    expect(screen.getByText("ce")).toBeVisible();
    expect(screen.getAllByText("admin").length).toBeGreaterThan(0);
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
              capabilities: { incremental_send: false },
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
      await screen.findByText(/filling it can fill the host and destabilize no-dal/i),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "local" })).toBeVisible();
    expect(screen.getByText(/^no$/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /create directory pool/i })).toBeVisible();
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
    expect(screen.getByText(/ifindex 2/i)).toBeVisible();
  });

  it("renders the workloads route without fake counts", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^workloads$/i })).toBeVisible();
    expect(screen.getByText(/no workloads yet/i)).toBeVisible();
    expect(screen.queryByText(/21 workloads/i)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /create system container/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /create vm/i })).toBeVisible();
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

    expect(await screen.findByText(/guest agent required/i)).toBeVisible();
    expect(screen.queryByText(/connected/i)).not.toBeInTheDocument();
  });

  it("renders the create VM wizard", async () => {
    window.history.replaceState({}, "", "/workloads/new/vm");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: /create vm/i })).toBeVisible();
    expect(screen.getByText(/step 1 of 7/i)).toBeVisible();
  });

  it("shows VM console and honest guest-agent limits", async () => {
    window.history.replaceState({}, "", "/workloads/vm-1");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/vm-1": {
        status: 200,
        body: { id: "vm-1", name: "web", kind: "vm", status: "running", firmware: "bios", pending_restart: false },
      },
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^web$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /console/i })).toBeVisible();
    expect(screen.getByText(/no-dal guest agent required/i)).toBeVisible();
  });

  it("renders the certificates settings page for admin", async () => {
    window.history.replaceState({}, "", "/settings/certificates");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/certs": {
        status: 200,
        body: {
          enabled: false,
          mode: "",
          common_name: "",
          sans: [],
          fingerprint: "",
          acme_status: "not_configured",
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^certificates$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^certificates$/i })).toBeVisible();
    expect(await screen.findByRole("heading", { name: /^status$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /generate self-signed/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /download.*key/i })).not.toBeInTheDocument();
  });

  it("shows Create snapshot on the VM snapshots tab, not Backup", async () => {
    window.history.replaceState({}, "", "/workloads/vm-1/snapshots");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/vm-1": {
        status: 200,
        body: { id: "vm-1", name: "web", kind: "vm", status: "running" },
      },
      "/api/v1/workloads/vm-1/snapshots": {
        status: 200,
        body: {
          items: [],
          capability: {
            supported: true,
            mechanism: "qcow2-overlay",
            chain_max: 32,
            chain_depth: 0,
            reason: "",
          },
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^snapshots$/i })).toBeVisible();
    expect(screen.getByText(/point-in-time restore on the same pool\. this is not a backup\./i)).toBeVisible();
    expect(await screen.findByRole("button", { name: /^create snapshot$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^flatten chain$/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /backup/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/^\/backups$/i)).not.toBeInTheDocument();
  });

  it("shows Unsupported / not ZFS for Directory system container snapshots", async () => {
    window.history.replaceState({}, "", "/workloads/ct-1/snapshots");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/workloads/ct-1": {
        status: 200,
        body: { id: "ct-1", name: "accept-ct", kind: "system-container", status: "running" },
      },
      "/api/v1/workloads/ct-1/snapshots": {
        status: 200,
        body: {
          items: [],
          capability: {
            supported: false,
            mechanism: "",
            chain_max: 0,
            chain_depth: 0,
            reason: "Directory system containers do not support snapshots; this is not ZFS.",
          },
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^snapshots$/i })).toBeVisible();
    expect(await screen.findByText(/Unsupported\./i)).toBeVisible();
    expect(screen.getAllByText(/not ZFS/i).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: /^create snapshot$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /backup/i })).not.toBeInTheDocument();
  });

  it("renders /settings/updates and keeps snapshot actions off this page", async () => {
    window.history.replaceState({}, "", "/settings/updates");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/updates": {
        status: 200,
        body: {
          channel: "stable",
          host_supported: true,
          host_reason: "Debian 13 amd64",
          packages: [
            { name: "ndl-control", version: "0.1.10", status: "current" },
            { name: "ndl-agent", version: "0.1.10", status: "current" },
            { name: "ndl-ui", version: "0.1.10", status: "current" },
          ],
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^updates$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^updates$/i })).toBeVisible();
    expect(
      screen.getByText(/control-plane package bumps must not stop guests/i),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: /^create snapshot$/i })).not.toBeInTheDocument();
    expect(await screen.findByText("ndl-control")).toBeVisible();
    expect(screen.getByRole("button", { name: /^check for updates$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^apply update$/i })).toBeVisible();
  });

  it("shows Unsupported host honestly on /settings/updates", async () => {
    window.history.replaceState({}, "", "/settings/updates");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/updates": {
        status: 200,
        body: {
          channel: "stable",
          host_supported: false,
          host_reason: "Host platform is not Debian 13 amd64.",
          packages: [],
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^updates$/i })).toBeVisible();
    expect(await screen.findByText(/Unsupported\./i)).toBeVisible();
    expect(screen.getByText(/Host platform is not Debian 13 amd64\./i)).toBeVisible();
    expect(screen.getByText(/will not pretend an upgrade succeeded/i)).toBeVisible();
    expect(screen.queryByRole("button", { name: /^create snapshot$/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^apply update$/i })).toBeDisabled();
  });

  it("renders /backups with honest restore copy and no Create snapshot button", async () => {
    window.history.replaceState({}, "", "/backups");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/backups/targets": {
        status: 200,
        body: {
          items: [
            {
              id: "tgt-1",
              name: "local-disk",
              kind: "local",
              locator: "/var/lib/ndl/backups",
              status: "available",
            },
          ],
        },
      },
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/backups/runs": {
        status: 200,
        body: {
          items: [
            {
              id: "run-1",
              policy_id: "",
              target_id: "tgt-1",
              workload_id: "wl-1",
              snapshot_id: "snap-1",
              status: "running",
              error: "",
              started_at: "2026-09-01T12:00:00Z",
            },
          ],
        },
      },
      "/api/v1/backups/artifacts": {
        status: 200,
        body: {
          items: [
            {
              id: "art-1",
              run_id: "run-0",
              workload_id: "wl-1",
              checksum_sha256: "abc123",
              size_bytes: 1024,
              locator: "/var/lib/ndl/backups/art-1",
              format: "full",
              created_at: "2026-08-31T12:00:00Z",
            },
          ],
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^backups$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^backups$/i })).toBeVisible();
    expect(screen.getByText(/backups are independent copies\. snapshots are not backups\./i)).toBeVisible();
    expect(screen.queryByRole("button", { name: /^create snapshot$/i })).not.toBeInTheDocument();
    expect(await screen.findByText(/^Running$/)).toBeVisible();
    expect(
      screen.getByText(/restore as new creates a new workload uuid\. restore replace overwrites the existing workload/i),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: /^restore as new$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^restore replace$/i })).toBeVisible();
    expect(screen.getAllByText("local-disk").length).toBeGreaterThan(0);
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument();
  });

  it("renders MFA, groups, and audit pages from the shell", async () => {
    window.history.replaceState({}, "", "/settings/mfa");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/mfa": { status: 200, body: { enabled: false, kind: "not_configured" } },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^authenticator$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^mfa$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^groups$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^audit$/i })).toBeVisible();
    expect(screen.getByText(/webauthn is not implemented yet/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /^enroll totp$/i })).toBeVisible();
  });

  it("renders groups with honest empty state", async () => {
    window.history.replaceState({}, "", "/groups");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/groups": { status: 200, body: { items: [] } },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^groups$/i })).toBeVisible();
    expect(screen.getByText(/admin cannot be granted through a group/i)).toBeVisible();
    expect(screen.getByText(/not configured/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /^add group$/i })).toBeVisible();
  });

  it("renders audit events and keeps license activation off this page", async () => {
    window.history.replaceState({}, "", "/audit");
    mockApi({
      ...defaultRoutes,
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/audit": {
        status: 200,
        body: {
          items: [
            {
              id: "aud-1",
              action: "auth.login",
              result: "ok",
              created_at: "2026-09-01T12:00:00Z",
            },
          ],
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^audit$/i })).toBeVisible();
    expect(screen.getByText(/viewers cannot read this log/i)).toBeVisible();
    expect(await screen.findByText("auth.login")).toBeVisible();
    expect(screen.queryByText(/activate license/i)).not.toBeInTheDocument();
  });

  it("prompts for a TOTP code when login returns an MFA challenge", async () => {
    window.history.replaceState({}, "", "/login");
    mockApi({
      ...defaultRoutes,
      "/api/v1/auth/login": {
        status: 200,
        body: {
          mfa_required: true,
          mfa_challenge_id: "ch-1",
          mfa_token: "tok-1",
        },
      },
    });

    render(<App />);

    expect(await screen.findByRole("heading", { name: /^sign in$/i })).toBeVisible();
    fireEvent.change(screen.getByLabelText(/^username$/i), { target: { value: "admin" } });
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: "secret" } });
    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));

    expect(await screen.findByRole("heading", { name: /^authenticator$/i })).toBeVisible();
    expect(screen.getByLabelText(/authenticator code/i)).toBeVisible();
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument();
  });
});
