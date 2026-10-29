import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["superadmin"],
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
  "/api/v1/backups/locations": {
    status: 200,
    body: { items: [{ pool_id: "pool-1", name: "local", backend_type: "directory", usable: true, root_filesystem: true, recommended: false, target_path: "/var/lib/ndl/storage/local/backups" }] },
  },
  "/api/v1/backups/artifacts": { status: 200, body: { items: [] } },
  "/api/v1/backups/storage": {
    status: 200,
    body: {
      workspace: { root: "/var/lib/ndl/backup-repo", repo_bytes: 8 * 1024 * 1024, max_local_bytes: 50 * 1024 * 1024 * 1024 },
      pools: [],
      warnings: ["The local backup repository may grow to 250.0 GiB, more than a quarter of the 867.6 GiB disk it is on."],
      events: [{ at: "2026-10-09T19:40:00Z", kind: "retention", ok: false, message: "backup a1 was not removed and will be retried" }],
      retention: [],
      unmanaged_restore_points: [],
      deleted_workload_backups: [],
    },
  },
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
  vi.restoreAllMocks();
  window.localStorage.removeItem("ndl.backups.sizes.unit");
  window.history.replaceState({}, "", "/");
});

async function openPage() {
  window.history.replaceState({}, "", "/backups");
  render(<App />);
  return screen.findByRole("table", { name: /^workload backups$/i });
}

async function openPolicies() {
  fireEvent.click(screen.getByRole("button", { name: /^policies$/i }));
  return screen.findByRole("dialog", { name: /^backup policies$/i });
}

async function openTargets() {
  fireEvent.click(screen.getByRole("button", { name: /^targets$/i }));
  return screen.findByRole("dialog", { name: /^backup targets$/i });
}

function pickMore(label: RegExp) {
  fireEvent.click(screen.getByRole("button", { name: /^more backup actions$/i }));
  fireEvent.click(screen.getByRole("menuitem", { name: label }));
}

function rowOf(table: HTMLElement, name: string): HTMLElement {
  const row = within(table)
    .getAllByRole("row")
    .find((r) => within(r).queryByText(name, { selector: ".bk-strong" }));
  if (!row) {
    throw new Error(`no row for ${name}`);
  }
  return row;
}

const posts = (fetchMock: ReturnType<typeof mockApi>, part: string) =>
  fetchMock.mock.calls.filter(([u, i]) => String(u).includes(part) && i?.method === "POST");

describe("Backups page", () => {
  it("shows one workload table, a target summary and at most three page buttons", async () => {
    mockApi({ ...baseRoutes, "/api/v1/backups/policies": { status: 200, body: { items: [] } } });
    const table = await openPage();
    expect(within(table).getByText("alpha")).toBeVisible();
    expect(within(table).getByText("bravo")).toBeVisible();
    for (const col of [/^workload/i, /^size before/i, /^latest backup/i, /^total stored/i]) {
      expect(within(table).getByRole("columnheader", { name: col })).toBeVisible();
    }
    // Disaster recovery and image copies are columns hidden by default.
    expect(within(table).queryByRole("columnheader", { name: /disaster recovery/i })).not.toBeInTheDocument();
    expect(within(table).queryByRole("columnheader", { name: /image copies/i })).not.toBeInTheDocument();
    const summary = screen.getByRole("region", { name: /^backup summary$/i });
    expect(within(summary).getByText("local-disk")).toBeVisible();
    expect(within(summary).getByText(/^safe in the cloud/i)).toBeVisible();
    expect(within(summary).getByText(/^stored on this host/i)).toBeVisible();
    // The old boxes are gone.
    expect(screen.queryByRole("heading", { name: /^restore points$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /^management$/i })).not.toBeInTheDocument();
    for (const name of [/^run now$/i, /^policies$/i, /^targets$/i, /^more backup actions$/i]) {
      expect(screen.getByRole("button", { name })).toBeVisible();
    }
    // Everything else lives in the windows and menus.
    for (const name of [/^create policy$/i, /^add target$/i, /^settings$/i, /^export dr metadata$/i, /^restore points$/i, /full reset/i]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
  });

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
    await openPage();
    const manager = await openPolicies();
    expect(within(manager).getByText(/no backup policies yet/i)).toBeVisible();
    fireEvent.click(within(manager).getByRole("button", { name: /^create policy$/i }));
    const dialog = await screen.findByRole("dialog", { name: /create backup policy/i });
    expect(within(dialog).getByLabelText(/^selected workloads$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^all workloads$/i)).not.toBeChecked();
    expect(within(dialog).getByLabelText(/^full machine \/ full lxc$/i)).toBeChecked();
    expect(within(dialog).getByLabelText(/^smart application data$/i)).not.toBeChecked();
    expect(within(dialog).getByRole("group", { name: /^workloads$/i })).toBeVisible();
    expect(within(dialog).getByText(/full machine \/ full lxc is the default/i)).toBeVisible();
    expect(within(dialog).queryByRole("group", { name: /detected data categories/i })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /alpha/i }));
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /bravo/i }));
    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: "nightly-subset" } });
    fireEvent.change(within(dialog).getByLabelText(/^target$/i), { target: { value: "tgt-1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^create policy$/i }));
    await waitFor(() => {
      const post = posts(fetchMock, "/api/v1/backups/policies")[0];
      expect(post).toBeTruthy();
      const body = JSON.parse(String(post?.[1]?.body));
      expect(body.scope).toBe("selected");
      expect(body.capture_mode).toBe("full");
      expect(body.workload_ids).toEqual(["wl-a", "wl-b"]);
    });
    expect(fetchMock.mock.calls.some((call) => String(call[0]).includes("/backups/scope-preview"))).toBe(false);
  });

  it("leaves workloads out of an All workloads policy, and only offers that for All", async () => {
    const policy = {
      id: "pol-1", name: "everything", scope: "all", workload_ids: [], excluded_workload_ids: [],
      capture_mode: "smart", target_id: "tgt-1", schedule: "nightly", keep_daily: 7, keep_weekly: 4, keep_monthly: 3,
    };
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [policy] } },
      "PATCH /api/v1/backups/policies/pol-1": { status: 200, body: { ...policy, excluded_workload_ids: ["wl-b"] } },
    });
    await openPage();
    const manager = await openPolicies();
    fireEvent.click(within(manager).getByRole("button", { name: /^edit everything$/i }));
    const dialog = await screen.findByRole("dialog", { name: /edit backup policy/i });
    expect(within(dialog).getByLabelText(/^all workloads$/i)).toBeChecked();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /leave out bravo/i }));
    fireEvent.click(within(dialog).getByLabelText(/^selected workloads$/i));
    expect(within(dialog).queryByRole("checkbox", { name: /leave out/i })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText(/^all workloads$/i));
    expect(within(dialog).getByRole("checkbox", { name: /leave out bravo/i })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: /^save/i }));
    await waitFor(() => {
      const patch = fetchMock.mock.calls.find((call) => String(call[0]).includes("/backups/policies/pol-1") && call[1]?.method === "PATCH");
      expect(patch).toBeTruthy();
      const body = JSON.parse(String(patch?.[1]?.body));
      expect(body.scope).toBe("all");
      expect(body.excluded_workload_ids).toEqual(["wl-b"]);
      expect(body.workload_ids).toBeUndefined();
    });
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
    await openPage();
    const manager = await openPolicies();
    fireEvent.click(within(manager).getByRole("button", { name: /^create policy$/i }));
    const dialog = await screen.findByRole("dialog", { name: /create backup policy/i });
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /alpha/i }));
    fireEvent.click(within(dialog).getByLabelText(/^full machine \/ full lxc$/i));
    expect(within(dialog).getByText(/captures the complete recoverable guest filesystem/i)).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: /configure alpha/i })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText(/^custom$/i));
    expect(within(dialog).getByRole("button", { name: /configure alpha/i })).toBeVisible();
    expect(fetchMock.mock.calls.some((call) => String(call[0]).includes("/backups/scope-preview"))).toBe(false);
    fireEvent.click(within(dialog).getByRole("button", { name: /configure alpha/i }));
    expect(await within(dialog).findByRole("group", { name: /detected data categories/i })).toBeVisible();
    expect(within(dialog).getByText(/^databases$/i)).toBeVisible();
    const preview = fetchMock.mock.calls.find((call) => String(call[0]).includes("/backups/scope-preview"));
    expect(JSON.parse(String(preview?.[1]?.body)).workload_ids).toEqual(["wl-a"]);
  });

  const fleet = {
    id: "pol-all",
    name: "fleet-nightly",
    scope: "all",
    workload_ids: [],
    target_id: "tgt-1",
    schedule: "nightly",
    keep_daily: 7,
    keep_weekly: 4,
    keep_monthly: 3,
  };

  it("runs a policy from the page's Run now and from the Policies window", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [fleet] } },
      "POST /api/v1/backups/policies/pol-all/run": {
        status: 202,
        body: { items: [{ id: "run-1", target_id: "tgt-1", workload_id: "wl-a", status: "succeeded", started_at: "2026-09-01T12:00:00Z" }] },
      },
    });
    await openPage();
    fireEvent.click(screen.getByRole("button", { name: /^run now$/i }));
    const runNow = await screen.findByRole("dialog", { name: /^run backups now$/i });
    expect(within(runNow).getByRole("checkbox", { name: /fleet-nightly/i })).toBeChecked();
    fireEvent.click(within(runNow).getByRole("button", { name: /^run now$/i }));
    await waitFor(() => expect(posts(fetchMock, "/backups/policies/pol-all/run")).toHaveLength(1));
    expect(await screen.findByText(/the policy is running/i)).toBeVisible();

    const manager = await openPolicies();
    expect(within(manager).getByText(/all workloads \(2 current\)/i)).toBeVisible();
    fireEvent.click(within(manager).getByRole("button", { name: /^run fleet-nightly now$/i }));
    await waitFor(() => expect(posts(fetchMock, "/backups/policies/pol-all/run")).toHaveLength(2));
  });

  it("shows why Run now failed when no workload can be copied", async () => {
    mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [{ ...fleet, name: "Backup" }] } },
      "POST /api/v1/backups/policies/pol-all/run": {
        status: 422,
        body: {
          error:
            "no eligible workloads in policy scope (1 skipped). Workloads whose root disk is iSCSI or a distributed volume cannot be backed up.",
        },
      },
    });
    await openPage();
    fireEvent.click(screen.getByRole("button", { name: /^run now$/i }));
    const runNow = await screen.findByRole("dialog", { name: /^run backups now$/i });
    fireEvent.click(within(runNow).getByRole("button", { name: /^run now$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/root disk is iscsi or a distributed volume/i);
  });

  it("selects every workload and backs the selection up in one go", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "POST /api/v1/backups/run": { status: 202, body: { id: "run-x", target_id: "tgt-1", workload_id: "wl-a", status: "running", started_at: "2026-10-30T10:00:00Z" } },
    });
    const table = await openPage();
    expect(screen.queryByRole("group", { name: /^selected workloads$/i })).not.toBeInTheDocument();
    fireEvent.click(within(table).getByRole("checkbox", { name: /^select every workload$/i }));
    expect(within(table).getByRole("checkbox", { name: /^select alpha$/i })).toBeChecked();
    expect(within(table).getByRole("checkbox", { name: /^select bravo$/i })).toBeChecked();
    const bulk = screen.getByRole("group", { name: /^selected workloads$/i });
    expect(bulk).toHaveTextContent("2 selected");
    fireEvent.click(within(bulk).getByRole("button", { name: /^back up now$/i }));
    const dialog = await screen.findByRole("dialog", { name: /^back up 2 workloads$/i });
    expect(within(dialog).getByLabelText(/^target$/i)).toHaveValue("tgt-1");
    fireEvent.click(within(dialog).getByRole("button", { name: /^run backup$/i }));
    await waitFor(() => {
      const bodies = posts(fetchMock, "/api/v1/backups/run").map(([, i]) => JSON.parse(String(i?.body)));
      expect(bodies.map((b) => b.workload_id)).toEqual(["wl-a", "wl-b"]);
      expect(bodies.every((b) => b.target_id === "tgt-1" && b.capture_mode === "full")).toBe(true);
    });
    expect(await screen.findByText(/backing up 2 workloads/i)).toBeVisible();
    // Unticking one leaves a partial selection.
    fireEvent.click(within(table).getByRole("checkbox", { name: /^select bravo$/i }));
    expect(within(table).getByRole("checkbox", { name: /^select every workload$/i })).not.toBeChecked();
    expect(screen.getByRole("group", { name: /^selected workloads$/i })).toHaveTextContent("1 selected");
  });

  it("shows the hidden Disaster recovery and Image copies columns from the pencil", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/workloads": {
        status: 200,
        body: {
          items: [
            { id: "wl-a", name: "alpha", kind: "vm", status: "running" },
            { id: "wl-c", name: "charlie", kind: "system-container", status: "running" },
          ],
        },
      },
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/platform-backup": {
        status: 200,
        body: {
          enabled: true,
          health: {
            checked_at: "2026-10-30T09:00:00Z",
            total: 1,
            recoverable: 1,
            stale: 0,
            missing: 0,
            items: [{ workload_id: "wl-a", name: "alpha", status: "recoverable", latest_at: "2026-10-30T08:00:00Z", deep: true }],
          },
        },
      },
      "/api/v1/backups/workload-options": { status: 200, body: { items: [{ workload_id: "wl-c", keep_images: false }] } },
      "PUT /api/v1/backups/workload-options/wl-c": { status: 200, body: { workload_id: "wl-c", keep_images: true } },
    });
    const table = await openPage();
    fireEvent.click(within(table).getByRole("button", { name: /^edit backup columns$/i }));
    const editor = screen.getByRole("dialog", { name: /^edit backup columns$/i });
    fireEvent.click(within(editor).getByRole("checkbox", { name: /^disaster recovery$/i }));
    fireEvent.click(within(editor).getByRole("checkbox", { name: /^image copies$/i }));
    expect(await within(table).findByRole("columnheader", { name: /disaster recovery/i })).toBeVisible();
    expect(within(rowOf(table, "alpha")).getByText("Recoverable")).toBeVisible();
    expect(within(rowOf(table, "alpha")).getByText(/read back/i)).toBeVisible();
    expect(within(rowOf(table, "charlie")).getByText("Not covered")).toBeVisible();
    expect(within(rowOf(table, "alpha")).getByText("Not a container")).toBeVisible();
    fireEvent.click(within(rowOf(table, "charlie")).getByRole("checkbox", { name: /digest only/i }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([u, i]) => String(u).includes("/backups/workload-options/wl-c") && i?.method && i.method !== "GET")).toBe(true),
    );
    expect(within(rowOf(table, "charlie")).getByRole("checkbox", { name: /kept/i })).toBeChecked();
    // Back to the default columns for the tests that follow.
    fireEvent.click(within(editor).getByRole("button", { name: /^reset to default$/i }));
    await waitFor(() => expect(within(table).queryByRole("columnheader", { name: /disaster recovery/i })).not.toBeInTheDocument());
  });

  it("manages targets in the Targets window and tests an object target", async () => {
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/backups/targets": {
        status: 200,
        body: {
          items: [
            { id: "tgt-r2", name: "R2", kind: "r2", locator: "s3://ndl-ce/", status: "untested", bucket: "ndl-ce", no_check_bucket: true, has_encryption_key: true },
          ],
        },
      },
      "POST /api/v1/backups/targets/tgt-r2/test": {
        status: 200,
        body: { id: "tgt-r2", name: "R2", kind: "r2", locator: "s3://ndl-ce/", status: "available", bucket: "ndl-ce", no_check_bucket: true, has_encryption_key: true },
      },
    });
    await openPage();
    const summary = screen.getByRole("region", { name: /^backup summary$/i });
    expect(within(summary).getByText("R2")).toBeVisible();
    expect(within(summary).getByText("Untested")).toBeVisible();
    // Target buttons live in the Targets window only.
    expect(screen.queryByRole("button", { name: /^test connection$/i })).not.toBeInTheDocument();
    const targets = await openTargets();
    expect(within(targets).getByRole("list", { name: /^backup targets$/i })).toHaveTextContent("R2");
    expect(within(targets).getByRole("button", { name: /^remove r2 with its backups$/i })).toBeVisible();
    expect(within(targets).getByRole("button", { name: /^wipe r2$/i })).toBeVisible();
    fireEvent.click(within(targets).getByRole("button", { name: /^test connection$/i }));
    await waitFor(() => expect(posts(fetchMock, "/api/v1/backups/targets/tgt-r2/test")).toHaveLength(1));
    // Adding a target opens its form on top of the window.
    fireEvent.click(within(targets).getByRole("button", { name: /^add target$/i }));
    const add = await screen.findByRole("dialog", { name: /^add backup target$/i });
    expect(within(add).getByLabelText(/^kind$/i)).toHaveValue("local");
    expect(screen.getByRole("dialog", { name: /^backup targets$/i })).toBeInTheDocument();

    fireEvent.click(within(add).getByRole("button", { name: /^cancel$/i }));
    fireEvent.click(within(targets).getByRole("button", { name: /^close$/i }));
    pickMore(/^settings$/i);
    const workspace = await screen.findByRole("dialog", { name: /backup settings/i });
    expect(within(workspace).getByText(/r2 local uploads/i)).toBeVisible();
    expect(within(workspace).getByLabelText(/^capture concurrency$/i)).toHaveValue(2);
    expect(within(workspace).getByLabelText(/^local cache retention \(hours\)$/i)).toHaveValue(24);
    expect(within(workspace).queryByRole("switch", { name: /legacy ui/i })).not.toBeInTheDocument();
  });

  it("shows protection states, repository statistics, consistency, and restore-point detail", async () => {
    const art = (id: string, extra: Record<string, unknown>) => ({
      id,
      run_id: `run-${id}`,
      workload_id: "wl-a",
      checksum_sha256: id,
      size_bytes: 50,
      locator: `ndl-cab://${id}`,
      format: "ndl-cab",
      created_at: "2026-09-01T12:01:00Z",
      local_complete: true,
      ...extra,
    });
    mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/backups/artifacts": {
        status: 200,
        body: {
          items: [
            art("art-protected", {
              created_at: "2026-09-01T12:00:00Z",
              remote_state: "protected",
              protection: "Protected",
              consistency: "crash-consistent",
              logical_bytes: 4096,
              physical_new_data: 1024,
              capture_mode: "full",
              capture_mode_label: "Full Machine",
              blueprint: { name: "alpha", os: "alpine", arch: "amd64" },
            }),
            art("art-queued", { remote_state: "queued", protection: "Queued" }),
            art("art-uploading", { created_at: "2026-09-01T12:02:00Z", remote_state: "uploading", protection: "Uploading" }),
            art("art-local", { created_at: "2026-09-01T12:03:00Z", remote_state: "local-only", protection: "Local complete" }),
            art("art-failed", { created_at: "2026-09-01T12:04:00Z", remote_state: "failed", protection: "Failed" }),
            art("art-legacy", { workload_id: "wl-b", format: "tar.zst", created_at: "2026-08-01T12:00:00Z", protection: "Legacy", local_complete: undefined }),
          ],
        },
      },
    });
    const table = await openPage();
    const summary = screen.getByRole("region", { name: /^backup summary$/i });
    for (const label of [/^safe in the cloud/i, /^only on this host/i, /^waiting to upload/i, /^uploading/i, /^failed/i, /^old format/i, /^stored on this host/i, /^left to upload/i]) {
      expect(within(summary).getByText(label)).toBeVisible();
    }
    // The newest restore point decides the row's Last backup state.
    expect(within(rowOf(table, "alpha")).getByText("Failed")).toBeVisible();
    pickMore(/^restore points$/i);
    const points = await screen.findByRole("dialog", { name: /^restore points$/i });
    for (const label of [/^queued$/i, /^uploading$/i, /^failed$/i, /^legacy$/i, /^protected$/i, /^local complete$/i]) {
      expect(within(points).getAllByText(label).length).toBeGreaterThan(0);
    }
    fireEvent.click(within(points).getAllByRole("button", { name: /^restore or verify$/i })[0]);
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
    await openPage();
    pickMore(/^settings$/i);
    const dialog = await screen.findByRole("dialog", { name: /backup settings/i });
    fireEvent.change(within(dialog).getByLabelText(/^capture concurrency$/i), { target: { value: "4" } });
    fireEvent.change(within(dialog).getByLabelText(/^upload workers$/i), { target: { value: "6" } });
    fireEvent.change(within(dialog).getByLabelText(/^upload bandwidth limit \(mib\/s\)$/i), { target: { value: "8" } });
    fireEvent.change(within(dialog).getByLabelText(/^local cache retention \(hours\)$/i), { target: { value: "12" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^save settings$/i }));
    await waitFor(() => {
      const patch = fetchMock.mock.calls.find((call) => String(call[0]).includes("/api/v1/backups/workspace") && call[1]?.method === "PATCH");
      expect(patch).toBeTruthy();
      const body = JSON.parse(String(patch?.[1]?.body));
      expect(body.capture_concurrency).toBe(4);
      expect(body.upload_workers).toBe(6);
      expect(body.bandwidth_limit_bps).toBe(8 * 1024 * 1024);
      expect(body.cache_retention_hours).toBe(12);
    });
  });

  it("sums sizes per workload, explains a run, and removes a deleted restore point at once", async () => {
    const routes: Record<string, { status: number; body?: unknown }> = {
      ...baseRoutes,
      "/api/v1/backups/targets": { status: 200, body: { items: [{ id: "tgt-r2", name: "R2", kind: "r2", status: "available", bucket: "b" }] } },
      "/api/v1/backups/policies": { status: 200, body: { items: [] } },
      "/api/v1/backups/runs": {
        status: 200,
        body: {
          items: [
            {
              id: "run-1",
              target_id: "tgt-r2",
              workload_id: "wl-a",
              status: "succeeded",
              incremental: true,
              transferred_bytes: 38_000_000,
              started_at: "2026-10-16T10:00:00Z",
              plan: { method: "content-addressed", consistency: "cgroup-freezer" },
            },
          ],
        },
      },
      "/api/v1/backups/artifacts": {
        status: 200,
        body: {
          items: [
            {
              id: "art-1",
              run_id: "run-1",
              workload_id: "wl-a",
              format: "ndl-cab",
              remote_state: "protected",
              logical_bytes: 2_000_000_000,
              upload_total_bytes: 40_000_000,
              created_at: "2026-10-16T10:00:00Z",
            },
            {
              id: "art-0",
              run_id: "run-0",
              workload_id: "wl-a",
              format: "ndl-cab",
              remote_state: "protected",
              logical_bytes: 1_900_000_000,
              upload_total_bytes: 600_000_000,
              created_at: "2026-10-15T10:00:00Z",
            },
          ],
        },
      },
      "DELETE /api/v1/backups/artifacts/art-1": { status: 200, body: { id: "art-1", deleted: true } },
    };
    mockApi(routes);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const table = await openPage();
    const alpha = rowOf(table, "alpha");
    // Before is the newest point's size on the workload, Latest what it added, Total every point.
    expect(alpha).toHaveTextContent("2.0 GB");
    expect(alpha).toHaveTextContent("40.0 MB");
    expect(alpha).toHaveTextContent("640 MB");
    expect(rowOf(table, "bravo")).toHaveTextContent("None");
    // The size unit sits with the column choice.
    fireEvent.click(within(table).getByRole("button", { name: /^edit backup columns$/i }));
    fireEvent.change(screen.getByRole("combobox", { name: /^size unit$/i }), { target: { value: "GB" } });
    expect(rowOf(table, "alpha")).toHaveTextContent("0.04 GB");
    expect(rowOf(table, "alpha")).toHaveTextContent("0.64 GB");
    fireEvent.change(screen.getByRole("combobox", { name: /^size unit$/i }), { target: { value: "MB" } });

    fireEvent.click(within(rowOf(table, "alpha")).getByRole("button", { name: /^alpha backup actions$/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /^run history$/i }));
    const history = await screen.findByRole("dialog", { name: /^backup runs$/i });
    expect(within(history).getByLabelText(/^workload$/i)).toHaveValue("wl-a");
    fireEvent.click(within(history).getByRole("button", { name: /^details of alpha run/i }));
    const run = await screen.findByRole("dialog", { name: /backup run details/i });
    expect(within(run).getByText(/storing only data not already backed up, paused for a moment/i)).toBeVisible();
    fireEvent.click(within(run).getByRole("button", { name: /close/i }));
    fireEvent.click(within(history).getByRole("button", { name: /close/i }));

    fireEvent.click(within(rowOf(table, "alpha")).getByRole("button", { name: /^alpha backup actions$/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /^restore points$/i }));
    const points = await screen.findByRole("dialog", { name: /^alpha restore points$/i });
    routes["/api/v1/backups/artifacts"] = { status: 200, body: { items: [] } };
    fireEvent.click(within(points).getAllByRole("button", { name: /^delete backup from/i })[0]);
    expect(await within(points).findByText(/no backup artifacts yet/i)).toBeVisible();
  });

  it("clears history without asking, resets everything after confirming, and keeps target buttons in Targets", async () => {
    const policy = {
      id: "pol-1", name: "nightly", scope: "all", workload_ids: [], excluded_workload_ids: [],
      capture_mode: "smart", target_id: "tgt-1", schedule: "nightly", keep_daily: 1, keep_weekly: 1, keep_monthly: 1,
    };
    const run = { id: "run-1", policy_id: "pol-1", target_id: "tgt-1", workload_id: "wl-a", status: "succeeded", started_at: "2026-10-20T03:28:08Z", finished_at: "2026-10-20T03:31:28Z", transferred_bytes: 3.2e9 };
    const fetchMock = mockApi({
      ...baseRoutes,
      "/api/v1/backups/policies": { status: 200, body: { items: [policy] } },
      "/api/v1/backups/runs": { status: 200, body: { items: [run] } },
      "DELETE /api/v1/backups/policies/pol-1/runs": { status: 200, body: { removed: 1, kept: 0 } },
      "POST /api/v1/backups/reset": { status: 200, body: { stopped: 0, legacy_removed: 0 } },
    });
    const confirm = vi.spyOn(window, "confirm");
    await openPage();
    const targets = await openTargets();
    expect(within(targets).getByRole("button", { name: /^remove local-disk with its backups$/i })).toBeVisible();
    fireEvent.click(within(targets).getByRole("button", { name: /^close$/i }));

    const manager = await openPolicies();
    fireEvent.click(within(manager).getByRole("button", { name: /^nightly policy actions$/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /^history$/i }));
    const history = await screen.findByRole("dialog", { name: /nightly history/i });
    expect(within(history).getByText("3 min")).toBeVisible();
    fireEvent.click(within(history).getByRole("button", { name: /^delete all history$/i }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([u, i]) => String(u).includes("/backups/policies/pol-1/runs") && i?.method === "DELETE")).toBe(true),
    );
    expect(confirm).not.toHaveBeenCalled();
    fireEvent.click(within(history).getAllByRole("button", { name: /^cancel$|^close$/i })[0]);
    fireEvent.click(within(manager).getByRole("button", { name: /^close$/i }));

    pickMore(/^full reset$/i);
    const reset = await screen.findByRole("dialog", { name: /full reset of backups/i });
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/backups/reset"))).toEqual([]);
    expect(within(reset).getByRole("button", { name: /^wipe all backup data$/i })).toBeDisabled();
    fireEvent.change(within(reset).getByLabelText(/type reset to confirm/i), { target: { value: "RESET" } });
    fireEvent.click(within(reset).getByRole("button", { name: /^wipe all backup data$/i }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([u]) => String(u).includes("/backups/reset"));
      expect(new Headers(call?.[1]?.headers).get("X-Nodal-Confirm")).toBe("backup-reset");
    });
  });
});
