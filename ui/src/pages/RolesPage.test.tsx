import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

const owner: MeResponse = {
  user_id: "user-1",
  username: "owner",
  roles: ["admin"],
  grants: ["*"],
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

describe("Roles page", () => {
  it("groups permissions and keeps built-in roles immutable", async () => {
    mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: owner },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "/api/v1/features": { status: 200, body: { items: [] } },
      "/api/v1/settings/license": {
        status: 200,
        body: { edition: "ce", status: "absent", has_key: false, workloads_stopped: false },
      },
      "/api/v1/roles": {
        status: 200,
        body: {
          items: [
            {
              name: "viewer",
              title: "User",
              summary: "Read the appliance.",
              login: true,
              immutable: true,
              user_count: 2,
              permissions: ["compute.read", "backup.read"],
            },
          ],
        },
      },
      "/api/v1/users": { status: 200, body: { items: [{ username: "skila", roles: ["admin"] }] } },
    });
    window.history.replaceState({}, "", "/roles");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /roles/i })).toBeVisible();
    fireEvent.click(await screen.findByRole("button", { name: /^permissions$/i }));
    expect(await screen.findByText(/view workloads/i)).toBeVisible();
    expect(screen.getByText("compute.read")).toBeVisible();
    expect(screen.getAllByText(/built-in/i).length).toBeGreaterThan(0);
  });
});
