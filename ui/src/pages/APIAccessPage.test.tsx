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

describe("APIAccessPage", () => {
  it("creates a scoped REST token and shows it once", async () => {
    const fetchMock = mockApi({
      "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
      "/api/v1/setup/status": { status: 200, body: { open: false } },
      "/api/v1/me": { status: 200, body: admin },
      "/api/v1/nodes": { status: 200, body: { items: [] } },
      "/api/v1/tasks": { status: 200, body: { items: [] } },
      "GET /api/v1/tokens": {
        status: 200,
        body: {
          items: [
            {
              id: "tok-1",
              name: "diag",
              prefix: "ndl_abcd",
              user_id: "user-1",
              username: "admin",
              permissions: ["compute.read", "migration.read"],
              disabled: false,
            },
          ],
        },
      },
      "POST /api/v1/tokens": {
        status: 201,
        body: { id: "tok-2", prefix: "ndl_efgh", token: "ndl_secret_once", preset: "readonly-debug" },
      },
      "GET /api/v1/service-principals": { status: 200, body: { items: [] } },
      "/api/v1/features": { status: 200, body: { items: [] } },
      "/api/v1/settings/license": {
        status: 200,
        body: { edition: "ce", status: "absent", has_key: false, workloads_stopped: false },
      },
    });
    window.history.replaceState({}, "", "/api-access");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /api access/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /api usage/i }));
    expect(await screen.findByText(/not MCP/i)).toBeVisible();
    expect(screen.getByText(/Authorization: Bearer/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));
    fireEvent.change(await screen.findByLabelText(/token name/i), { target: { value: "agent" } });
    fireEvent.click(screen.getAllByRole("button", { name: /create token/i }).at(-1) as HTMLElement);
    expect(await screen.findByText(/ndl_secret_once/)).toBeVisible();
    expect(fetchMock.mock.calls.some((call) => String(call[0]).includes("/api/v1/tokens") && (call[1] as RequestInit | undefined)?.method === "POST")).toBe(true);
  });
});
