import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";
import { featuresOnRoute } from "../test/features";

const admin: MeResponse = { user_id: "user-1", username: "admin", roles: ["superadmin"], edition: "ce", ux_level: "guided", expert_ack: false };

type Route = { status: number; body?: unknown } | ((init?: RequestInit, url?: URL) => { status: number; body?: unknown });

function mockApi(routes: Record<string, Route>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const hit = routes[`${method} ${url.pathname}`] ?? routes[url.pathname];
    if (!hit) {
      return new Response(JSON.stringify({ error: `unmocked ${url.pathname}` }), { status: 404, headers: { "Content-Type": "application/json" } });
    }
    const res = typeof hit === "function" ? hit(init, url) : hit;
    return new Response(JSON.stringify(res.body ?? {}), { status: res.status, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const base: Record<string, Route> = {
  "/api/v1/health": { status: 200, body: { status: "ok" } },
  "/api/v1/setup/status": { status: 200, body: { open: false } },
  "/api/v1/me": { status: 200, body: admin },
  "/api/v1/nodes": { status: 200, body: { items: [] } },
  "/api/v1/events": { status: 200, body: { items: [] } },
  "/api/v1/timeline": { status: 200, body: { items: [] } },
  "/api/v1/alerts": { status: 200, body: { items: [] } },
  "/api/v1/tasks": { status: 200, body: { items: [] } },
  "/api/v1/cluster": { status: 200, body: { id: "c", name: "local", nodes: [] } },
  "/api/v1/workloads": { status: 200, body: { items: [] } },
  "/api/v1/features": featuresOnRoute,
};

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("Components page", () => {
  it("shows a failed Compose install with its reason and retries, updates all", async () => {
    const posts: string[] = [];
    mockApi({
      ...base,
      "/api/v1/workloads/w1": { status: 200, body: { id: "w1", name: "VaultDock", kind: "system-container", status: "running" } },
      "/api/v1/workloads/w1/extensions": {
        status: 200,
        body: {
          busy: false,
          items: [
            { id: "docker", name: "Docker Engine", description: "Container runtime", category: "containers", available: true, state: "installed", requested: true, version: "26.1.5" },
            {
              id: "compose",
              name: "Docker Compose",
              description: "Compose v2 plugin",
              category: "containers",
              available: true,
              state: "failed",
              requested: true,
              message: "context canceled",
            },
            { id: "gh", name: "GitHub CLI", description: "GitHub command line", category: "development", available: false, state: "missing", requested: false, unavailable_reason: "GitHub CLI is not in this image's default repositories" },
          ],
        },
      },
      "POST /api/v1/workloads/w1/extensions": (init) => {
        posts.push(String(init?.body ?? ""));
        return { status: 202, body: { started: true, extras: ["docker", "compose"] } };
      },
    });
    window.history.replaceState({}, "", "/workloads/w1/extensions");
    render(<App />);
    const table = await screen.findByRole("table", { name: /components/i });
    expect(within(table).getByText("context canceled")).toBeVisible();
    expect(within(table).getByText(/not in this image/i)).toBeVisible();
    fireEvent.click(within(table).getByRole("button", { name: /^retry$/i }));
    await waitFor(() => expect(posts[0]).toBeTruthy());
    expect(JSON.parse(posts[0])).toEqual({ action: "install", extras: ["compose"] });
    fireEvent.click(screen.getByRole("button", { name: /^update all$/i }));
    await waitFor(() => expect(posts[1]).toBeTruthy());
    expect(JSON.parse(posts[1])).toEqual({ action: "update", all: true });
  });
});

describe("Storage manager", () => {
  it("scans, explains items, protects workload disks and confirms bulk deletes", async () => {
    let deleted = "";
    const scan = { id: "s1", root: "/var/lib/ndl/storage/local", one_filesystem: true, state: "done", started_at: "", entries: 9, bytes: 300, fs_total: 1000, fs_free: 200 };
    mockApi({
      ...base,
      "/api/v1/storage/pools": { status: 200, body: { items: [] } },
      "/api/v1/storage/images": { status: 200, body: { items: [] } },
      "/api/v1/host/disk": { status: 200, body: { status: { level: "ok", filesystems: [], reserve_bytes: 0 } } },
      "/api/v1/storage/scan/targets": { status: 200, body: { can_delete: true, items: [{ label: "local pool (directory)", path: "/var/lib/ndl/storage/local", kind: "pool" }] } },
      "POST /api/v1/storage/scan": { status: 202, body: scan },
      "/api/v1/storage/scan/s1": {
        status: 200,
        body: {
          scan,
          path: "",
          node: { name: "local", path: "", dir: true, size: 300, apparent: 300, files: 3, dirs: 2, mtime: 0, insight: { purpose: "No-dal storage pool data", importance: "important", confidence: "likely" } },
          items: [
            { name: "vault.img", path: "vault.img", dir: false, size: 200, apparent: 900, files: 1, dirs: 0, mtime: 1700000000, insight: { purpose: "The disk of system-container VaultDock", importance: "critical", confidence: "known", protected: true } },
            { name: "old.tar.gz", path: "old.tar.gz", dir: false, size: 60, apparent: 60, files: 1, dirs: 0, mtime: 1700000000, insight: { purpose: "Archive", importance: "review", confidence: "likely" } },
            { name: "cache", path: "cache", dir: true, size: 40, apparent: 40, files: 1, dirs: 0, mtime: 1700000000, has_children: true, insight: { purpose: "Application caches", importance: "low", confidence: "likely" } },
          ],
        },
      },
      "POST /api/v1/storage/scan/s1/delete": (init) => {
        deleted = String(init?.body ?? "");
        return { status: 200, body: { deleted: 2, freed_bytes: 100, results: [{ path: "old.tar.gz", deleted: true, freed: 60 }, { path: "cache", deleted: true, freed: 40 }] } };
      },
    });
    window.history.replaceState({}, "", "/storage/manage");
    render(<App />);
    expect(await screen.findByRole("group", { name: /storage sections/i })).toBeVisible();
    fireEvent.click(await screen.findByRole("button", { name: /^scan$/i }));
    const table = await screen.findByRole("table", { name: /folder contents/i });
    expect(within(table).getByText(/the disk of system-container vaultdock/i)).toBeVisible();
    expect(within(table).getByRole("checkbox", { name: /select vault.img/i })).toBeDisabled();
    fireEvent.click(within(table).getByRole("checkbox", { name: /select old.tar.gz/i }));
    fireEvent.click(within(table).getByRole("checkbox", { name: /select cache/i }));
    fireEvent.click(screen.getByRole("button", { name: /delete selected/i }));
    const dialog = await screen.findByRole("dialog", { name: /delete 2 item/i });
    expect(within(dialog).getByText(/not marked usually safe/i)).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: /^delete /i }));
    await waitFor(() => expect(deleted).toBeTruthy());
    expect(JSON.parse(deleted)).toEqual({ items: ["old.tar.gz", "cache"], confirm: "delete 2" });
    expect(await screen.findByText(/deleted 2 item/i)).toBeVisible();
  });
});

describe("Create system container", () => {
  it("follows extras installing in the background and shows which failed and why", async () => {
    let polls = 0;
    mockApi({
      ...base,
      "/api/v1/storage/pools": { status: 200, body: { items: [{ id: "p1", name: "local", status: "available", backend_type: "directory" }] } },
      "/api/v1/networks": { status: 200, body: { items: [{ id: "n1", name: "LAN", status: "available", kind: "bridge" }], nics: [] } },
      "/api/v1/workloads": { status: 200, body: { items: [], image_pins: ["debian/13/amd64/default"] } },
      "/api/v1/workload-extras": {
        status: 200,
        body: {
          items: [
            { id: "docker", name: "Docker Engine", description: "Container runtime", category: "containers", available: true, needs_nesting: true },
            { id: "compose", name: "Docker Compose", description: "Compose v2 plugin", category: "containers", requires: ["docker"], available: true, needs_nesting: true },
          ],
        },
      },
      "POST /api/v1/workloads": { status: 201, body: { id: "w9", name: "vault", kind: "system-container", status: "running", setup_status: "installing", setup_operation_id: "op1" } },
      "/api/v1/workloads/w9/extensions": () => {
        polls++;
        const done = polls > 1;
        return {
          status: 200,
          body: {
            busy: !done,
            items: [
              { id: "docker", name: "Docker Engine", state: done ? "installed" : "installing", available: true, requested: true },
              { id: "compose", name: "Docker Compose", state: done ? "failed" : "installing", available: true, requested: true, message: done ? "context canceled" : undefined },
            ],
          },
        };
      },
    });
    window.history.replaceState({}, "", "/workloads/new/system-container");
    render(<App />);
    fireEvent.change(await screen.findByLabelText(/^name$/i), { target: { value: "vault" } });
    const steps = screen.getByRole("navigation", { name: /create steps/i });
    fireEvent.click(within(steps).getByRole("button", { name: /components/i }));
    fireEvent.click(await screen.findByText("Docker Compose"));
    fireEvent.click(within(steps).getByRole("button", { name: /review/i }));
    fireEvent.click(await screen.findByRole("button", { name: /create system container/i }));
    expect(await screen.findByText(/components install in the background/i)).toBeVisible();
    expect(await screen.findByText(/docker compose failed: context canceled/i, {}, { timeout: 8000 })).toBeVisible();
    expect(screen.getByText(/docker engine installed/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /retry failed setup/i })).toBeVisible();
  }, 15000);
});
