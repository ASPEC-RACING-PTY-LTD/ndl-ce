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
});
