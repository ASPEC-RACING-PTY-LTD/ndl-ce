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

const feature = (id: string, enabled: boolean) => ({ id, title: id === "codehold" ? "CodeHold runners" : id, enabled, core: false, package_status: "", runtime_status: "", starts_runtime: false, kubelet_started: false, workload_count: 0, reason: "", software_only: true });

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


const chStatus = {
  connected: true,
  controller: { url: "http://192.168.2.196:8085", runner_url: "http://192.168.2.196:8085", controller_id: "ctl-1", name: "homelab", status: "connected", syncing: true, cloudflare_access: false, last_sync_at: new Date().toISOString() },
  settings: { cpus: 2, memory_mb: 2048, disk_gb: 50, image_pin: "debian/trixie/amd64/default", source: "local", checksum_required: false },
  capacity: { cpus: 16, memory_mb: 49152 },
  instances: [{ machine_id: "w9", machine_name: "CodeHold", container_id: "srv", container_name: "codehold-codehold-1", image: "codehold-codehold", http_port: 8085, has_runner: true, url: "http://192.168.2.196:8085" }],
  runners: [
    { id: "r1", name: "build-1", desired: "running", state: "running", busy: 1, limits: { concurrency: 2, cpus: 2, memory_mb: 2048 }, workload_id: "w1", workload_name: "codehold-build-1-abc123", power: "running", setting_up: false, detached: false, updated_at: "" },
    { id: "r2", name: "build-2", desired: "running", state: "error", error: "installing Docker in the container failed: no network", busy: 0, limits: {}, workload_id: "w2", workload_name: "codehold-build-2-def456", power: "running", setting_up: false, detached: false, updated_at: "" },
  ],
};

describe("CodeHold runners", () => {
  it("lists runners with their containers and fills the local CodeHold when connecting", async () => {
    let posted = "";
    mockApi({
      ...base({ codehold: true }),
      "/api/v1/codehold": { status: 200, body: { ...chStatus, connected: false, controller: undefined, runners: [] } },
      "POST /api/v1/codehold/detect": { status: 200, body: { instances: chStatus.instances } },
      "POST /api/v1/codehold/connect": (init) => {
        posted = String(init?.body);
        return { status: 200, body: { connected: true, name: "homelab" } };
      },
    });
    window.history.replaceState({}, "", "/codehold");
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^connect codehold$/i }));
    const dlg = await screen.findByRole("dialog", { name: /connect codehold/i });
    // The CodeHold on this node is offered and used over the local network.
    await waitFor(() => expect(within(dlg).getByLabelText(/codehold address/i)).toHaveValue("http://192.168.2.196:8085"));
    expect(within(dlg).getByText("codehold-codehold-1")).toBeVisible();
    fireEvent.change(within(dlg).getByLabelText(/enrollment token/i), { target: { value: "chce_abc" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /^connect$/i }));
    await waitFor(() => expect(posted).toContain("chce_abc"));
    expect(JSON.parse(posted)).toMatchObject({ url: "http://192.168.2.196:8085", enrollment_token: "chce_abc" });
  });

  it("shows each runner's state, container and errors", async () => {
    mockApi({ ...base({ codehold: true }), "/api/v1/codehold": { status: 200, body: chStatus } });
    window.history.replaceState({}, "", "/codehold");
    render(<App />);
    const table = await screen.findByRole("table", { name: /codehold runners/i });
    expect(within(table).getByText(/running 1 job/i)).toBeVisible();
    expect(within(table).getByRole("link", { name: "codehold-build-1-abc123" })).toHaveAttribute("href", "/workloads/w1");
    expect(within(table).getByText(/installing docker in the container failed/i)).toBeVisible();
    expect(screen.getByText("2 vCPU · 2 GB")).toBeVisible();
    fireEvent.click(within(within(table).getByText("build-2").closest("tr")!).getByRole("button", { name: /actions/i }));
    expect(await screen.findByRole("menuitem", { name: /retry setup now/i })).toBeVisible();
  });

  it("asks what happens to every runner before turning off, or forces it", async () => {
    let body = "";
    mockApi({
      ...base({ codehold: true }),
      "/api/v1/codehold": { status: 200, body: chStatus },
      "POST /api/v1/codehold/disable": (init) => {
        body = String(init?.body);
        return { status: 200, body: { disabled: true } };
      },
    });
    window.history.replaceState({}, "", "/settings/features");
    render(<App />);
    const heads = await screen.findAllByRole("heading", { name: /^codehold runners$/i });
    const row = heads[0].closest("article")!;
    fireEvent.click(await within(row).findByRole("button", { name: /^disable$/i }));
    const dlg = await screen.findByRole("dialog", { name: /turn off codehold runners/i });
    const apply = within(dlg).getByRole("button", { name: /apply and turn off/i });
    await within(dlg).findByText("build-1");
    expect(apply).toBeDisabled();
    fireEvent.change(within(dlg).getByLabelText(/what happens to build-1/i), { target: { value: "stop" } });
    expect(apply).toBeDisabled();
    fireEvent.change(within(dlg).getByLabelText(/what happens to build-2/i), { target: { value: "delete" } });
    expect(apply).toBeEnabled();
    fireEvent.click(apply);
    await waitFor(() => expect(body).not.toBe(""));
    expect(JSON.parse(body)).toEqual({ decisions: { r1: "stop", r2: "delete" } });
  });

  it("warns on the dashboard while force-disabled containers wait for deletion", async () => {
    let acted = "";
    mockApi({
      ...base({}),
      "/api/v1/codehold/pending": {
        status: 200,
        body: { items: [{ ...chStatus.runners[0], detached: true, delete_after: "2026-10-23T12:00:00Z" }] },
      },
      "POST /api/v1/codehold/pending": (init) => {
        acted = String(init?.body);
        return { status: 200, body: { done: 1 } };
      },
    });
    render(<App />);
    const alert = (await screen.findByText(/1 codehold runner container will be deleted/i)).closest("[role=alert]")!;
    fireEvent.click(within(alert as HTMLElement).getByRole("button", { name: /keep them/i }));
    await waitFor(() => expect(acted).toContain("keep"));
  });

  it("can be dismissed", async () => {
    mockApi({
      ...base({}),
      "/api/v1/codehold/pending": { status: 200, body: { items: [{ ...chStatus.runners[0], detached: true, delete_after: "2026-10-23T12:00:00Z" }] } },
      "PATCH /api/v1/me": { status: 200, body: admin },
    });
    render(<App />);
    const alert = (await screen.findByText(/will be deleted/i)).closest("[role=alert]") as HTMLElement;
    fireEvent.click(within(alert).getByRole("button", { name: /dismiss/i }));
    await waitFor(() => expect(screen.queryByText(/will be deleted/i)).toBeNull());
  });
});
