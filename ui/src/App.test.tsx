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
    expect(screen.queryByText(/storage pool/i)).not.toBeInTheDocument();

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

  it("imports generated OpenAPI path types", () => {
    const path: GetHealthPath = "/api/v1/health";
    expect(path).toBe("/api/v1/health");
  });
});
