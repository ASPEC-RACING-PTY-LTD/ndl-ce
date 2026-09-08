import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

function mockApi(routes: Record<string, { status: number; body?: unknown }>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
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

describe("ImportExportPage", () => {
  it("shows copy-first source safety and requires an explicit mode", async () => {
    mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/storage/pools": { status: 200, body: { items: [{ id: "pool-1", name: "Fast-ZFS" }] } },
      "/api/v1/networks": { status: 200, body: { items: [{ id: "net-1", name: "LAN" }], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [] } },
      "/api/v1/migration/adapters": {
        status: 200,
        body: {
          items: [
            { id: "proxmox", label: "Proxmox VE", role: "source", discovery: true, notes: "Live is unavailable." },
            { id: "disk", label: "VM disk", role: "source" },
          ],
        },
      },
      "/api/v1/migration/modes": {
        status: 200,
        body: {
          source_safety: "PROTECTED",
          items: [
            { id: "offline", label: "Offline", consistency: "SAFE", source_safety: "PROTECTED", summary: "Stopped state." },
            { id: "live", label: "Live", consistency: "RISKY", source_safety: "PROTECTED", summary: "No guarantees.", requires_ack: true },
          ],
        },
      },
      "/api/v1/migration/sources": { status: 200, body: { items: [] } },
      "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
    });
    window.history.replaceState({}, "", "/import-export");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /import \/ export/i })).toBeVisible();
    expect(screen.getByText(/source safety protected/i)).toBeVisible();
    expect(screen.getByText(/source destruction is not a migration operation/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^export$/i }));
    expect(await screen.findByText(/helps you leave/i)).toBeVisible();
    expect(screen.getAllByText(/compatible package/i).length).toBeGreaterThan(0);
  });

  it("requires the full Proxmox user@realm!tokenid=secret value", async () => {
    mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/storage/pools": { status: 200, body: { items: [] } },
      "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [] } },
      "/api/v1/migration/adapters": {
        status: 200,
        body: {
          items: [{ id: "proxmox", label: "Proxmox VE", role: "source", discovery: true }],
        },
      },
      "/api/v1/migration/modes": { status: 200, body: { source_safety: "PROTECTED", items: [] } },
      "/api/v1/migration/sources": { status: 200, body: { items: [] } },
      "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
    });
    window.history.replaceState({}, "", "/import-export");
    render(<App />);
    expect(await screen.findByLabelText(/proxmox api token/i)).toBeVisible();
    expect(screen.getByText(/user@realm!tokenid=secret/i)).toBeVisible();
    expect(screen.getByText(/root@pam!nodal=/i)).toBeVisible();
    fireEvent.change(screen.getByLabelText(/proxmox api token/i), { target: { value: "SECRET-TOKEN-VALUE" } });
    fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
    const alerts = await screen.findAllByRole("alert");
    expect(alerts.some((el) => /not the secret alone/i.test(el.textContent ?? ""))).toBe(true);
    expect(document.getElementById("mig-tok-error")?.textContent).toMatch(/user@realm!tokenid=secret/);
  });

  it("connects, discovers, and reviews without manual source to dest mapping", async () => {
    const fetchMock = mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/storage/pools": { status: 200, body: { items: [{ id: "pool-1", name: "Fast-ZFS" }] } },
      "/api/v1/networks": { status: 200, body: { items: [{ id: "net-1", name: "LAN" }], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [] } },
      "/api/v1/migration/adapters": {
        status: 200,
        body: { items: [{ id: "proxmox", label: "Proxmox VE", role: "source", discovery: true }] },
      },
      "/api/v1/migration/modes": {
        status: 200,
        body: {
          source_safety: "PROTECTED",
          items: [{ id: "offline", label: "Offline", consistency: "SAFE", source_safety: "PROTECTED", summary: "Stopped state." }],
        },
      },
      "/api/v1/migration/sources": { status: 200, body: { items: [] } },
      "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
      "POST /api/v1/migration/sources": {
        status: 201,
        body: { id: "src-1", adapter: "proxmox", label: "proxmox", endpoint: "https://pve.example:8006" },
      },
      "POST /api/v1/migration/sources/src-1/discover": {
        status: 200,
        body: {
          workloads: [
            {
              source_id: "pve1/100",
              name: "web",
              kind: "vm",
              running: false,
              storage: ["local"],
              networks: ["vmbr0"],
              capabilities: ["offline"],
            },
          ],
        },
      },
      "POST /api/v1/migration/plans": {
        status: 200,
        body: {
          plan: { items: [{ source_id: "pve1/100", name: "web", mode: "offline", compatibility: "READY", findings: [] }] },
          review: [
            {
              source_id: "pve1/100",
              name: "web",
              source: "vm pve1/100",
              destination: "vm",
              migration_mode: "offline",
              consistency: "SAFE",
              source_safety: "PROTECTED",
              storage: { local: "pool-1" },
              network: { vmbr0: "net-1" },
              compatibility: "READY",
              warnings: [],
              source_changes: "NONE",
            },
          ],
        },
      },
    });
    window.history.replaceState({}, "", "/import-export");
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/endpoint/i), { target: { value: "https://pve.example:8006" } });
    fireEvent.change(screen.getByLabelText(/proxmox api token/i), {
      target: { value: "root@pam!nodal=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
    expect(await screen.findByRole("heading", { name: /select workloads/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /1\. select workloads/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /2\. review/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /mapping/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /compatibility/i })).toBeNull();
    fireEvent.click(screen.getByLabelText(/select web/i));
    expect(screen.getByRole("radio", { name: /consistent copy/i })).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: /review 1 workload/i }));
    expect(await screen.findByRole("heading", { name: /^review$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /3\. progress/i })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: /method/i })).toBeVisible();
    expect(screen.getByText(/local -> Fast-ZFS/i)).toBeVisible();
    expect(screen.getByText(/vmbr0 -> LAN/i)).toBeVisible();
    expect(screen.getByText(/offline/i)).toBeVisible();
    expect(document.getElementById("map-st")).toBeNull();
    expect(screen.queryByRole("textbox", { name: /^storage$/i })).toBeNull();
    expect(screen.getByRole("button", { name: /^advanced$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^submit$/i })).toBeVisible();
    const planCall = fetchMock.mock.calls.find((call) => {
      const url = typeof call[0] === "string" ? call[0] : "";
      return url.includes("/migration/plans");
    });
    expect(planCall).toBeTruthy();
    const init = planCall?.[1] as RequestInit | undefined;
    const body = JSON.parse(String(init?.body ?? "{}")) as {
      strategy?: string;
      modes?: Record<string, string>;
      mapping?: { storage?: Record<string, string> };
    };
    expect(body.strategy).toBe("consistent");
    expect(body.modes ?? {}).toEqual({});
    expect(body.mapping?.storage ?? {}).toEqual({});
  });

  it("plans every selected workload from one strategy", async () => {
    const fetchMock = mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/storage/pools": { status: 200, body: { items: [{ id: "pool-1", name: "Fast-ZFS" }] } },
      "/api/v1/networks": { status: 200, body: { items: [{ id: "net-1", name: "LAN" }], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [] } },
      "/api/v1/migration/adapters": {
        status: 200,
        body: { items: [{ id: "proxmox", label: "Proxmox VE", role: "source", discovery: true }] },
      },
      "/api/v1/migration/modes": { status: 200, body: { source_safety: "PROTECTED", items: [] } },
      "/api/v1/migration/sources": { status: 200, body: { items: [] } },
      "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
      "POST /api/v1/migration/sources": {
        status: 201,
        body: { id: "src-1", adapter: "proxmox", label: "proxmox", endpoint: "https://pve.example:8006" },
      },
      "POST /api/v1/migration/sources/src-1/discover": {
        status: 200,
        body: {
          workloads: [
            { source_id: "pve1/100", name: "web", kind: "vm", running: false },
            { source_id: "pve1/101", name: "db", kind: "vm", running: true },
          ],
        },
      },
      "POST /api/v1/migration/plans": {
        status: 200,
        body: {
          plan: {
            items: [
              { source_id: "pve1/100", name: "web", mode: "offline", compatibility: "READY", findings: [] },
              { source_id: "pve1/101", name: "db", mode: "backup", compatibility: "READY", findings: [] },
            ],
          },
          review: [
            {
              source_id: "pve1/100",
              name: "web",
              migration_mode: "offline",
              consistency: "SAFE",
              storage: { local: "pool-1" },
              network: { vmbr0: "net-1" },
              compatibility: "READY",
            },
            {
              source_id: "pve1/101",
              name: "db",
              migration_mode: "backup",
              consistency: "SAFE",
              storage: { local: "pool-1" },
              network: { vmbr0: "net-1" },
              compatibility: "READY",
            },
          ],
        },
      },
    });
    window.history.replaceState({}, "", "/import-export");
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/endpoint/i), { target: { value: "https://pve.example:8006" } });
    fireEvent.change(screen.getByLabelText(/proxmox api token/i), {
      target: { value: "root@pam!nodal=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
    expect(await screen.findByRole("heading", { name: /select workloads/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /select all/i }));
    fireEvent.click(screen.getByRole("radio", { name: /leave sources running/i }));
    fireEvent.click(screen.getByRole("button", { name: /review 2 workloads/i }));
    expect(await screen.findByRole("heading", { name: /^review$/i })).toBeVisible();
    expect(screen.getByText(/^web$/i)).toBeVisible();
    expect(screen.getByText(/^db$/i)).toBeVisible();
    const planCall = fetchMock.mock.calls.find((call) => String(call[0]).includes("/migration/plans"));
    const body = JSON.parse(String((planCall?.[1] as RequestInit | undefined)?.body ?? "{}")) as {
      strategy?: string;
      selected?: string[];
      modes?: Record<string, string>;
    };
    expect(body.strategy).toBe("leave-running");
    expect(body.selected).toEqual(["pve1/100", "pve1/101"]);
    expect(body.modes ?? {}).toEqual({});
  });
});
