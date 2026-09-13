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
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${method} ${path}` });
    }
    return jsonResponse(hit.status, hit.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const baseRoutes = {
  "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/me": { status: 200, body: admin },
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
  "/api/v1/workloads": {
    status: 200,
    body: {
      items: [
        { id: "wl-a", name: "alpha", kind: "vm", status: "running" },
        { id: "wl-b", name: "bravo", kind: "vm", status: "stopped" },
      ],
    },
  },
  "/api/v1/stacks": { status: 200, body: { items: [] } },
  "/api/v1/features": { status: 200, body: { items: [] } },
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
  "/api/v1/backups/targets": {
    status: 200,
    body: { items: [{ id: "tgt-1", name: "local-disk", kind: "local", locator: "/var/lib/ndl/backups", status: "available" }] },
  },
  "/api/v1/backups/runs": { status: 200, body: { items: [] } },
  "/api/v1/backups/artifacts": { status: 200, body: { items: [] } },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Backups page", () => {
  it("defaults new policies to selected workloads and Smart Application Data", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "POST /api/v1/backups/policies": {
        status: 201,
        body: {
          id: "pol-1",
          name: "nightly-subset",
          scope: "selected",
          workload_ids: ["wl-a", "wl-b"],
          capture_mode: "smart",
          target_id: "tgt-1",
          schedule: "nightly",
          keep_daily: 7,
          keep_weekly: 4,
          keep_monthly: 3,
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);

    expect(await screen.findByRole("heading", { name: /^policies$/i })).toBeVisible();
    expect(screen.getByText(/no backup policies yet/i)).toBeVisible();
    expect(screen.getByText(/no backup runs yet/i)).toBeVisible();
    expect(screen.getByText(/no backup artifacts yet/i)).toBeVisible();
    fireEvent.click(screen.getAllByRole("button", { name: /^create policy$/i })[0]);
    const dialog = await screen.findByRole("dialog", { name: /create backup policy/i });
    expect(within(dialog).getByLabelText(/^selected workloads$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^all workloads$/i)).not.toBeChecked();
    expect(within(dialog).getByLabelText(/^smart application data$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^full machine \/ full lxc$/i)).not.toBeChecked();
    expect(within(dialog).getByRole("group", { name: /^workloads$/i })).toBeVisible();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /alpha/i }));
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /bravo/i }));
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: "nightly-subset" } });
    fireEvent.change(within(dialog).getByLabelText(/^target$/i), { target: { value: "tgt-1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^create policy$/i }));
    await waitFor(() => {
      const post = fetchMock.mock.calls.find((call) => {
        const url = String(call[0]);
        return url.includes("/api/v1/backups/policies") && call[1]?.method === "POST";
      });
      expect(post).toBeTruthy();
      const body = JSON.parse(String(post?.[1]?.body));
      expect(body.scope).toBe("selected");
      expect(body.capture_mode).toBe("smart");
      expect(body.workload_ids).toEqual(["wl-a", "wl-b"]);
    });
  });

  it("runs a policy from its card instead of a separate Run backup panel", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": {
        status: 200,
        body: {
          items: [
            {
              id: "pol-all",
              name: "fleet-nightly",
              scope: "all",
              workload_ids: [],
              target_id: "tgt-1",
              schedule: "nightly",
              keep_daily: 7,
              keep_weekly: 4,
              keep_monthly: 3,
            },
          ],
        },
      },
      "POST /api/v1/backups/policies/pol-all/run": {
        status: 202,
        body: { items: [{ id: "run-1", target_id: "tgt-1", workload_id: "wl-a", status: "succeeded", started_at: "2026-09-01T12:00:00Z" }] },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);

    expect(await screen.findByText("fleet-nightly")).toBeVisible();
    expect(screen.getByText(/all workloads \(2 current\)/i)).toBeVisible();
    expect(screen.queryByRole("heading", { name: /^run backup$/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^run now$/i }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringMatching(/\/api\/v1\/backups\/policies\/pol-all\/run$/),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("shows why Run now failed when no workload can be copied", async () => {
    mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": {
        status: 200,
        body: {
          items: [
            {
              id: "pol-all",
              name: "Backup",
              scope: "all",
              workload_ids: [],
              target_id: "tgt-1",
              schedule: "nightly",
              keep_daily: 2,
              keep_weekly: 1,
              keep_monthly: 1,
            },
          ],
        },
      },
      "POST /api/v1/backups/policies/pol-all/run": {
        status: 422,
        body: {
          error:
            "no eligible workloads in policy scope (1 skipped). Workloads whose root disk is iSCSI or a distributed volume cannot be backed up.",
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);
    expect(await screen.findByText("Backup")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^run now$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/root disk is iscsi or a distributed volume/i);
  });

  it("tests an object target without inventing available on create", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/backups/targets": {
        status: 200,
        body: {
          items: [
            {
              id: "tgt-r2",
              name: "R2",
              kind: "r2",
              locator: "s3://ndl-ce/",
              status: "untested",
              bucket: "ndl-ce",
              no_check_bucket: true,
              has_encryption_key: true,
            },
          ],
        },
      },
      "POST /api/v1/backups/targets/tgt-r2/test": {
        status: 200,
        body: {
          id: "tgt-r2",
          name: "R2",
          kind: "r2",
          locator: "s3://ndl-ce/",
          status: "available",
          bucket: "ndl-ce",
          no_check_bucket: true,
          has_encryption_key: true,
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);
    expect(await screen.findByText("R2")).toBeVisible();
    expect(screen.getByText("Untested")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^test connection$/i }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringMatching(/\/api\/v1\/backups\/targets\/tgt-r2\/test$/),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });
});
