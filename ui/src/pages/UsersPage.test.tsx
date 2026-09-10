import { cleanup, render, screen } from "@testing-library/react";
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

const viewer: MeResponse = {
  user_id: "user-2",
  username: "viewer",
  roles: ["viewer"],
  grants: ["identity.read"],
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

const common = {
  "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/nodes": { status: 200, body: { items: [] } },
  "/api/v1/tasks": { status: 200, body: { items: [] } },
  "/api/v1/features": { status: 200, body: { items: [] } },
  "/api/v1/settings/license": {
    status: 200,
    body: { edition: "ce", status: "absent", has_key: false, workloads_stopped: false },
  },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Users management", () => {
  it("lists users for an owner", async () => {
    mockApi({
      ...common,
      "/api/v1/me": { status: 200, body: owner },
      "GET /api/v1/users": {
        status: 200,
        body: {
          items: [
            {
              id: "u1",
              username: "owner",
              display_name: "Owner",
              roles: ["admin"],
              status: "active",
              mfa_status: "not_enrolled",
              api_token_count: 0,
              protected: true,
            },
          ],
          total: 1,
        },
      },
    });
    window.history.replaceState({}, "", "/users");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^users$/i })).toBeVisible();
    expect(await screen.findByText(/last owner/i)).toBeVisible();
  });

  it("forbids a standard user from opening Users", async () => {
    mockApi({
      ...common,
      "/api/v1/me": { status: 200, body: viewer },
      "GET /api/v1/users": { status: 403, body: { error: "forbidden" } },
    });
    window.history.replaceState({}, "", "/users");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^forbidden$/i })).toBeVisible();
  });
});
