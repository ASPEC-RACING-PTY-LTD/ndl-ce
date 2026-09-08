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
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
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

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Dashboard activity details", () => {
  it("expands a recent event with ids and copy details", async () => {
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
    mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/events": {
        status: 200,
        body: {
          items: [
            {
              id: "evt-9",
              type: "migration.failed",
              created_at: "2026-09-08T01:00:00Z",
              payload: { workload_id: "wl-1", job_id: "job-2", error: "archive missing" },
            },
          ],
        },
      },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/storage/pools": { status: 200, body: { items: [] } },
      "/api/v1/networks": { status: 200, body: { items: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [] } },
      "/api/v1/features": { status: 200, body: { items: [] } },
      "/api/v1/settings/license": {
        status: 200,
        body: { edition: "ce", status: "absent", has_key: false, workloads_stopped: false },
      },
    });
    window.history.replaceState({}, "", "/");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /recent events/i })).toBeVisible();
    fireEvent.click(await screen.findByRole("button", { name: /migration failed/i }));
    expect((await screen.findAllByText(/evt-9/)).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/job-2/).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: /copy details/i }));
    expect(await screen.findByRole("button", { name: /copied/i })).toBeVisible();
  });
});
