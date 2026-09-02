import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

const uploads: string[] = [];

function mockApi(
  routes: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })>,
) {
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
  class FakeXHR {
    status = 201;
    responseText = JSON.stringify({ ok: true });
    withCredentials = false;
    upload = { onprogress: null as ((ev: ProgressEvent) => void) | null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    open() {}
    send(body?: Document | XMLHttpRequestBodyInit | null) {
      const form = body as FormData | undefined;
      if (typeof form?.get === "function") {
        uploads.push(String(form.get("path") ?? ""));
      }
      queueMicrotask(() => this.onload?.());
    }
    abort() {
      this.onabort?.();
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
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
  "/api/v1/workloads": { status: 200, body: { items: [] } },
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
  "/api/v1/workloads/wl-1": {
    status: 200,
    body: { id: "wl-1", name: "accept-ct", kind: "system-container", status: "running" },
  },
};

const listed = {
  path: "/",
  entries: [
    { name: "etc", type: "dir", size: 0, path: "etc", mode: 493, mtime: "2026-09-01T00:00:00Z", owner: "root", group: "root" },
    { name: "readme.txt", type: "file", size: 2, path: "readme.txt", mode: 420, mtime: "2026-09-01T00:00:00Z", owner: "root", group: "root" },
  ],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  uploads.length = 0;
});

async function openFiles(extra: Record<string, { status: number; body?: unknown } | ((init?: RequestInit) => { status: number; body?: unknown })> = {}) {
  const fetchMock = mockApi({
    ...baseRoutes,
    "GET /api/v1/workloads/wl-1/files": { status: 200, body: listed },
    ...extra,
  });
  window.history.replaceState({}, "", "/workloads/wl-1/files");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /^files$/i })).toBeVisible();
  expect(await screen.findByRole("button", { name: /^upload$/i })).toBeVisible();
  return fetchMock;
}

function called(fetchMock: ReturnType<typeof vi.fn>, fragment: string, method?: string) {
  return fetchMock.mock.calls.some((c) => {
    const url = String(c[0]);
    const m = String((c[1] as RequestInit | undefined)?.method ?? "GET").toUpperCase();
    return url.includes(fragment) && (!method || m === method);
  });
}

describe("Files manager", () => {
  it("lists files with metadata and opens the editor for a text file", async () => {
    await openFiles({
      "GET /api/v1/workloads/wl-1/files/content": {
        status: 200,
        body: { name: "readme.txt", type: "file", size: 2, path: "readme.txt", content: "hi", editable: true, binary: false },
      },
    });
    expect(await screen.findByRole("button", { name: "readme.txt" })).toBeVisible();
    expect(screen.getAllByText("root:root").length).toBeGreaterThan(0);
    expect(screen.getByText("644")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "readme.txt" }));
    expect(await screen.findByTestId("monaco")).toHaveValue("hi");
  });

  it("creates a file through the upload API and opens it", async () => {
    const fetchMock = await openFiles({
      "GET /api/v1/workloads/wl-1/files/content": {
        status: 200,
        body: { name: "new.txt", type: "file", size: 0, path: "new.txt", content: "", editable: true },
      },
    });
    fireEvent.click(screen.getByRole("button", { name: /new file/i }));
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "new.txt" } });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(uploads).toContain("new.txt"));
    expect(await screen.findByTestId("monaco")).toBeVisible();
    expect(called(fetchMock, "/files/content")).toBe(true);
  });

  it("uploads dropped files into the current directory", async () => {
    await openFiles();
    const file = new File(["hi"], "drop.txt", { type: "text/plain" });
    fireEvent.drop(screen.getByRole("heading", { name: /^files$/i }).closest("section") as HTMLElement, {
      dataTransfer: { files: [file] },
    });
    await waitFor(() => expect(uploads).toContain("drop.txt"));
  });

  it("renames, moves, and copies through typed APIs", async () => {
    const fetchMock = await openFiles({
      "POST /api/v1/workloads/wl-1/files/move": { status: 200, body: { ok: true } },
      "POST /api/v1/workloads/wl-1/files/copy": { status: 201, body: { ok: true } },
    });
    expect(await screen.findByRole("button", { name: "readme.txt" })).toBeVisible();
    fireEvent.click((await screen.findAllByLabelText("More actions"))[1]);
    fireEvent.click(screen.getByRole("menuitem", { name: /^rename$/i }));
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "readme.md" } });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(called(fetchMock, "/files/move", "POST")).toBe(true));

    fireEvent.click(await screen.findByLabelText("Select readme.txt"));
    fireEvent.click(screen.getByRole("button", { name: /copy selected/i }));
    fireEvent.change(screen.getByLabelText(/destination directory/i), { target: { value: "etc" } });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(called(fetchMock, "/files/copy", "POST")).toBe(true));

    fireEvent.click(await screen.findByLabelText("Select readme.txt"));
    fireEvent.click(screen.getByRole("button", { name: /move selected/i }));
    fireEvent.change(screen.getByLabelText(/destination directory/i), { target: { value: "etc" } });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(fetchMock.mock.calls.filter((c) => String(c[0]).includes("/files/move")).length).toBeGreaterThan(1));
  });

  it("confirms recursive directory delete", async () => {
    const fetchMock = await openFiles({
      "POST /api/v1/workloads/wl-1/files/delete": { status: 200, body: { ok: true, path: "etc" } },
    });
    fireEvent.click(await screen.findByLabelText("Select etc"));
    fireEvent.click(await screen.findByRole("button", { name: /delete selected/i }));
    expect(await screen.findByText(/including 1 director/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    await waitFor(() => expect(called(fetchMock, "/files/delete", "POST")).toBe(true));
  });

  it("surfaces a permissions failure", async () => {
    await openFiles({
      "POST /api/v1/workloads/wl-1/files/chmod": { status: 403, body: { error: "permission denied" } },
    });
    expect(await screen.findByRole("button", { name: "readme.txt" })).toBeVisible();
    fireEvent.click((await screen.findAllByLabelText("More actions"))[1]);
    fireEvent.click(screen.getByRole("menuitem", { name: /^permissions$/i }));
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/permission denied/i);
  });

  it("shows binary and large-file editor fallbacks", async () => {
    await openFiles({
      "GET /api/v1/workloads/wl-1/files": {
        status: 200,
        body: { path: "/", entries: [{ name: "blob.bin", type: "file", size: 4, path: "blob.bin" }] },
      },
      "GET /api/v1/workloads/wl-1/files/content": {
        status: 200,
        body: { name: "blob.bin", binary: true, editable: false, size: 4, path: "blob.bin" },
      },
    });
    fireEvent.click(await screen.findByRole("button", { name: "blob.bin" }));
    expect(await screen.findByText(/looks binary/i)).toBeVisible();
    cleanup();
    await openFiles({
      "GET /api/v1/workloads/wl-1/files": {
        status: 200,
        body: { path: "/", entries: [{ name: "huge.txt", type: "file", size: 4000000, path: "huge.txt" }] },
      },
      "GET /api/v1/workloads/wl-1/files/content": {
        status: 200,
        body: { name: "huge.txt", too_large: true, editable: false, size: 4000000, path: "huge.txt" },
      },
    });
    fireEvent.click(await screen.findByRole("button", { name: "huge.txt" }));
    expect(await screen.findByText(/too large/i)).toBeVisible();
  });
});
