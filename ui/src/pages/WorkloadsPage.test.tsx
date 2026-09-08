import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

const viewer: MeResponse = {
  ...admin,
  user_id: "user-2",
  username: "view",
  roles: ["viewer"],
};

const containers = [
  { id: "wl-a", name: "AspecRacing", kind: "system-container", status: "stopped" },
  { id: "wl-s", name: "SoundDock", kind: "system-container", status: "stopped" },
  { id: "wl-v", name: "Ubuntu", kind: "vm", status: "stopped" },
];

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body ?? {}), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mockApi(
  me: MeResponse,
  extra: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })> = {},
  items: unknown[] = containers,
) {
  const routes: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })> = {
    "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
    "/api/v1/setup/status": { status: 200, body: { open: false } },
    "/api/v1/me": { status: 200, body: me },
    "/api/v1/nodes": { status: 200, body: { items: [] } },
    "/api/v1/events": { status: 200, body: { items: [] } },
    "/api/v1/timeline": { status: 200, body: { items: [] } },
    "/api/v1/alerts": { status: 200, body: { items: [] } },
    "/api/v1/alerts/channels": { status: 200, body: { items: [] } },
    "/api/v1/tasks": { status: 200, body: { items: [] } },
    "/api/v1/storage/pools": { status: 200, body: { items: [] } },
    "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
    "/api/v1/cluster/wg": { status: 200, body: { items: [], nodes: [] } },
    "/api/v1/cluster": { status: 200, body: { id: "cluster-1", name: "local", nodes: [] } },
    "/api/v1/cluster/ha": {
      status: 200,
      body: { mode: "single-writer", writer: true, replica_status: "not_configured", fencing_mode: "operator", multi_master: false },
    },
    "/api/v1/cluster/update": { status: 200, body: { preview: [], note: "Rolling drains one node" } },
    "/api/v1/workloads": { status: 200, body: { items } },
    "/api/v1/storage/images": { status: 200, body: { items: [] } },
    "/api/v1/policies": { status: 200, body: { items: [] } },
    "/api/v1/policy-runs": { status: 200, body: { items: [] } },
    "/api/v1/ai/ask": { status: 200, body: { answer: "", citations: [], provider_status: "not_configured", mutate: false } },
    "/api/v1/ai/plans": { status: 200, body: { items: [] } },
    "/api/v1/settings/license": {
      status: 200,
      body: {
        edition: "ce",
        status: "absent",
        reason: "Community Edition. License activation is not required.",
        has_key: false,
        workloads_stopped: false,
        ee_blobs: false,
        contacts_api: false,
      },
    },
    "/api/v1/migration/adapters": { status: 200, body: { items: [] } },
    "/api/v1/migration/modes": { status: 200, body: { items: [], source_safety: "PROTECTED" } },
    "/api/v1/migration/sources": { status: 200, body: { items: [] } },
    "/api/v1/migration/jobs": { status: 200, body: { items: [] } },
    ...extra,
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
    }
    const resolved = typeof hit === "function" ? hit(init) : hit;
    return jsonResponse(resolved.status, resolved.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

async function openWorkloads(
  me: MeResponse = admin,
  extra: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })> = {},
  items: unknown[] = containers,
) {
  const fetchMock = mockApi(me, extra, items);
  window.history.replaceState({}, "", "/workloads");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /^workloads$/i })).toBeVisible();
  expect(await screen.findByRole("link", { name: "AspecRacing" })).toBeVisible();
  return fetchMock;
}

describe("Workloads bulk delete", () => {
  it("selects multiple containers and confirms once", async () => {
    const posted: string[] = [];
    await openWorkloads(admin, {
      "POST /api/v1/workloads/bulk-delete": (init) => {
        const body = JSON.parse(String(init?.body ?? "{}")) as { ids?: string[] };
        posted.push(...(body.ids ?? []));
        return {
          status: 200,
          body: {
            results: [
              { id: "wl-a", name: "AspecRacing", ok: true },
              { id: "wl-s", name: "SoundDock", ok: true },
            ],
          },
        };
      },
    });
    fireEvent.click(screen.getByLabelText("Select AspecRacing"));
    fireEvent.click(screen.getByLabelText("Select SoundDock"));
    expect(screen.queryByLabelText("Select Ubuntu")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /delete selected/i }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/delete 2 containers/i)).toBeVisible();
    expect(within(dialog).getByText("AspecRacing")).toBeVisible();
    expect(within(dialog).getByText("SoundDock")).toBeVisible();
    expect(screen.getAllByRole("dialog").length).toBe(1);
    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    await waitFor(() => expect(posted).toEqual(["wl-a", "wl-s"]));
    expect(await screen.findByText(/deleted 2 of 2 containers/i)).toBeVisible();
  });

  it("selects all visible containers from one checkbox", async () => {
    await openWorkloads();
    fireEvent.click(screen.getByLabelText("Select all"));
    expect(screen.getByLabelText("Select AspecRacing")).toBeChecked();
    expect(screen.getByLabelText("Select SoundDock")).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: /delete selected/i }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/delete 2 containers/i)).toBeVisible();
    expect(within(dialog).queryByText("Ubuntu")).not.toBeInTheDocument();
  });

  it("shows partial failures from one submit", async () => {
    await openWorkloads(admin, {
      "POST /api/v1/workloads/bulk-delete": {
        status: 200,
        body: {
          results: [
            { id: "wl-a", name: "AspecRacing", ok: true },
            { id: "wl-s", name: "SoundDock", ok: false, error: "agent refused delete" },
          ],
        },
      },
    });
    fireEvent.click(screen.getByLabelText("Select all"));
    fireEvent.click(screen.getByRole("button", { name: /delete selected/i }));
    fireEvent.click(await screen.findByRole("button", { name: /^delete$/i }));
    expect(await screen.findByText(/deleted 1 of 2 containers/i)).toBeVisible();
    expect(screen.getByText(/SoundDock: agent refused delete/i)).toBeVisible();
    expect(screen.getByText(/AspecRacing: deleted/i)).toBeVisible();
  });

  it("hides bulk delete from viewers", async () => {
    await openWorkloads(viewer);
    expect(screen.queryByLabelText("Select all")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Select AspecRacing")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete selected/i })).not.toBeInTheDocument();
  });
});
