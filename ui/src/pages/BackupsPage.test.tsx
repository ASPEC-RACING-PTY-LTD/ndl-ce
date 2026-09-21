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
  "/api/v1/backups/workspace": {
    status: 200,
    body: {
      max_local_bytes: 50 * 1024 * 1024 * 1024,
      min_host_free_bytes: 10 * 1024 * 1024 * 1024,
      capture_concurrency: 2,
      upload_workers: 4,
      bandwidth_limit_bps: 0,
      cache_retention_hours: 24,
      repo_bytes: 8 * 1024 * 1024,
      physical_bytes: 8 * 1024 * 1024,
      logical_bytes: 32 * 1024 * 1024,
      pending_bytes: 1024 * 1024,
      pending_uploads: 1,
      capture_active: 0,
      root: "/var/lib/ndl/backup-repo",
    },
  },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Backups page", () => {
  it("defaults new policies to selected workloads and Full Machine", async () => {
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
          capture_mode: "full",
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
    expect(screen.getByText(/^remote protected$/i)).toBeVisible();
    expect(screen.getByText(/^local repository$/i)).toBeVisible();
    fireEvent.click(screen.getAllByRole("button", { name: /^create policy$/i })[0]);
    const dialog = await screen.findByRole("dialog", { name: /create backup policy/i });
    expect(within(dialog).getByLabelText(/^selected workloads$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^all workloads$/i)).not.toBeChecked();
    expect(within(dialog).getByLabelText(/^full machine \/ full lxc$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^smart application data$/i)).not.toBeChecked();
    expect(within(dialog).getByRole("group", { name: /^workloads$/i })).toBeVisible();
    expect(within(dialog).getByText(/full machine \/ full lxc is the default/i)).toBeVisible();
    expect(within(dialog).queryByText(/backup scope preview/i)).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("group", { name: /detected data categories/i })).not.toBeInTheDocument();
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
      expect(body.capture_mode).toBe("full");
      expect(body.workload_ids).toEqual(["wl-a", "wl-b"]);
    });
    expect(
      fetchMock.mock.calls.some((call) => String(call[0]).includes("/backups/scope-preview")),
    ).toBe(false);
  });

  it("does not scan filesystems for Full Machine and loads Custom discovery one workload at a time", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "POST /api/v1/backups/scope-preview": {
        status: 200,
        body: {
          capture_mode: "custom",
          protected_bytes: 4,
          excluded_bytes: 2,
          full_bytes: 6,
          items: [
            {
              workload_id: "wl-a",
              workload_name: "alpha",
              items: [
                { id: "db:postgresql", kind: "database", label: "PostgreSQL", paths: ["/var/lib/postgresql"], bytes: 4, selected: true },
                { id: "repro:apt", kind: "reproducible", label: "Package cache", paths: ["/var/cache/apt"], bytes: 2, selected: false },
              ],
            },
          ],
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^policies$/i })).toBeVisible();
    fireEvent.click(screen.getAllByRole("button", { name: /^create policy$/i })[0]);
    const dialog = await screen.findByRole("dialog", { name: /create backup policy/i });
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /alpha/i }));
    fireEvent.click(within(dialog).getByLabelText(/^full machine \/ full lxc$/i));
    expect(within(dialog).getByText(/captures the complete recoverable guest filesystem/i)).toBeVisible();
    expect(within(dialog).queryByText(/backup scope preview/i)).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: /configure alpha/i })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText(/^custom$/i));
    expect(within(dialog).getByRole("button", { name: /configure alpha/i })).toBeVisible();
    expect(
      fetchMock.mock.calls.some((call) => String(call[0]).includes("/backups/scope-preview")),
    ).toBe(false);
    fireEvent.click(within(dialog).getByRole("button", { name: /configure alpha/i }));
    expect(await within(dialog).findByRole("group", { name: /detected data categories/i })).toBeVisible();
    expect(within(dialog).getByText(/^databases$/i)).toBeVisible();
    const preview = fetchMock.mock.calls.find((call) => String(call[0]).includes("/backups/scope-preview"));
    expect(preview).toBeTruthy();
    expect(JSON.parse(String(preview?.[1]?.body)).workload_ids).toEqual(["wl-a"]);
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
    fireEvent.click(screen.getByRole("button", { name: /^workspace$/i }));
    const workspace = await screen.findByRole("dialog", { name: /backup workspace/i });
    expect(within(workspace).getByText(/r2 local uploads/i)).toBeVisible();
    expect(within(workspace).getByLabelText(/^capture concurrency$/i)).toHaveValue(2);
    expect(within(workspace).getByLabelText(/^upload bandwidth limit \(mib\/s\)$/i)).toBeVisible();
    expect(within(workspace).getByLabelText(/^local cache retention \(hours\)$/i)).toHaveValue(24);
  });

  it("shows protection states, repository statistics, consistency, and restore-point detail", async () => {
    mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": {
        status: 200,
        body: {
          items: [
            {
              id: "pol-1",
              name: "nightly",
              scope: "selected",
              workload_ids: ["wl-a"],
              capture_mode: "full",
              target_id: "tgt-1",
              schedule: "nightly",
              keep_daily: 7,
              keep_weekly: 4,
              keep_monthly: 3,
            },
          ],
        },
      },
      "/api/v1/backups/runs": {
        status: 200,
        body: {
          items: [
            {
              id: "run-1",
              policy_id: "pol-1",
              target_id: "tgt-1",
              workload_id: "wl-a",
              status: "succeeded",
              started_at: "2026-09-01T12:00:00Z",
            },
          ],
        },
      },
      "/api/v1/backups/artifacts": {
        status: 200,
        body: {
          items: [
            {
              id: "art-protected",
              run_id: "run-1",
              workload_id: "wl-a",
              checksum_sha256: "a",
              size_bytes: 100,
              locator: "ndl-cab://a",
              format: "ndl-cab",
              created_at: "2026-09-01T12:00:00Z",
              local_complete: true,
              remote_state: "protected",
              protection: "Protected",
              consistency: "crash-consistent",
              logical_bytes: 4096,
              physical_new_data: 1024,
              capture_duration_ns: 1500000000,
              upload_duration_ns: 2500000000,
              capture_mode: "full",
              capture_mode_label: "Full Machine",
              blueprint: { name: "alpha", os: "alpine", arch: "amd64" },
            },
            {
              id: "art-queued",
              run_id: "run-q",
              workload_id: "wl-a",
              checksum_sha256: "b",
              size_bytes: 50,
              locator: "ndl-cab://b",
              format: "ndl-cab",
              created_at: "2026-09-01T12:01:00Z",
              local_complete: true,
              remote_state: "queued",
              protection: "Queued",
            },
            {
              id: "art-uploading",
              run_id: "run-u",
              workload_id: "wl-a",
              checksum_sha256: "c",
              size_bytes: 50,
              locator: "ndl-cab://c",
              format: "ndl-cab",
              created_at: "2026-09-01T12:02:00Z",
              local_complete: true,
              remote_state: "uploading",
              protection: "Uploading",
            },
            {
              id: "art-local",
              run_id: "run-l",
              workload_id: "wl-a",
              checksum_sha256: "d",
              size_bytes: 50,
              locator: "ndl-cab://d",
              format: "ndl-cab",
              created_at: "2026-09-01T12:03:00Z",
              local_complete: true,
              remote_state: "local-only",
              protection: "Local complete",
            },
            {
              id: "art-failed",
              run_id: "run-f",
              workload_id: "wl-a",
              checksum_sha256: "e",
              size_bytes: 50,
              locator: "ndl-cab://e",
              format: "ndl-cab",
              created_at: "2026-09-01T12:04:00Z",
              local_complete: true,
              remote_state: "failed",
              protection: "Failed",
            },
            {
              id: "art-legacy",
              run_id: "run-legacy",
              workload_id: "wl-b",
              checksum_sha256: "f",
              size_bytes: 50,
              locator: "/var/lib/ndl/backups/old.tar.zst",
              format: "tar.zst",
              created_at: "2026-08-01T12:00:00Z",
              protection: "Legacy",
            },
          ],
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);
    expect(await screen.findByText("nightly")).toBeVisible();
    expect(screen.getByLabelText(/^protection summary$/i)).toBeVisible();
    expect(screen.getByLabelText(/^repository statistics$/i)).toBeVisible();
    expect(screen.getByText(/^remote protected$/i)).toBeVisible();
    expect(screen.getByText(/^local pending$/i)).toBeVisible();
    expect(screen.getByText(/^logical protected data$/i)).toBeVisible();
    expect(screen.getByText(/^physical stored data$/i)).toBeVisible();
    expect(screen.getAllByText(/^queued$/i).length).toBeGreaterThan(1);
    expect(screen.getAllByText(/^uploading$/i).length).toBeGreaterThan(1);
    expect(screen.getAllByText(/^failed$/i).length).toBeGreaterThan(1);
    expect(screen.getAllByText(/^legacy$/i).length).toBeGreaterThan(1);
    expect(screen.getByText(/crash-consistent/i)).toBeVisible();
    expect(screen.getAllByText("Protected").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Local complete").length).toBeGreaterThan(0);
    fireEvent.click(screen.getAllByRole("button", { name: /^restore or verify$/i })[0]);
    const detail = await screen.findByRole("dialog", { name: /restore or verify/i });
    expect(within(detail).getByText("Full Machine")).toBeVisible();
    expect(within(detail).getByText("Protected")).toBeVisible();
    expect(within(detail).getByText("Crash-consistent")).toBeVisible();
    expect(within(detail).getByText(/alpha · alpine · amd64/i)).toBeVisible();
  });

  it("saves workspace capture concurrency, bandwidth, and cache retention", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "PATCH /api/v1/backups/workspace": {
        status: 200,
        body: {
          max_local_bytes: 20 * 1024 * 1024 * 1024,
          min_host_free_bytes: 5 * 1024 * 1024 * 1024,
          capture_concurrency: 4,
          upload_workers: 6,
          bandwidth_limit_bps: 8 * 1024 * 1024,
          cache_retention_hours: 12,
        },
      },
    });
    window.history.replaceState({}, "", "/backups");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^policies$/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^workspace$/i }));
    const dialog = await screen.findByRole("dialog", { name: /backup workspace/i });
    fireEvent.change(within(dialog).getByLabelText(/^capture concurrency$/i), { target: { value: "4" } });
    fireEvent.change(within(dialog).getByLabelText(/^upload workers$/i), { target: { value: "6" } });
    fireEvent.change(within(dialog).getByLabelText(/^upload bandwidth limit \(mib\/s\)$/i), { target: { value: "8" } });
    fireEvent.change(within(dialog).getByLabelText(/^local cache retention \(hours\)$/i), { target: { value: "12" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^save workspace$/i }));
    await waitFor(() => {
      const patch = fetchMock.mock.calls.find((call) => {
        const url = String(call[0]);
        return url.includes("/api/v1/backups/workspace") && call[1]?.method === "PATCH";
      });
      expect(patch).toBeTruthy();
      const body = JSON.parse(String(patch?.[1]?.body));
      expect(body.capture_concurrency).toBe(4);
      expect(body.upload_workers).toBe(6);
      expect(body.bandwidth_limit_bps).toBe(8 * 1024 * 1024);
      expect(body.cache_retention_hours).toBe(12);
    });
  });
});
