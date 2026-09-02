import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

vi.mock("../components/FileEditor", () => ({
  FileEditor: () => null,
}));

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
  roles: ["admin"],
};

const operator: MeResponse = { ...admin, user_id: "user-3", username: "oper", roles: ["operator"] };
const viewer: MeResponse = { ...admin, user_id: "user-2", username: "view", roles: ["viewer"] };

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const alpine = { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-1" };
const sound = { id: "wl-s", name: "SoundDock", kind: "system-container", status: "running", node_id: "node-1" };
const ubuntu = { id: "wl-u", name: "Ubuntu", kind: "vm", status: "running", node_id: "node-1" };
const stopped = { id: "wl-stop", name: "Test VM", kind: "vm", status: "stopped", node_id: "node-1" };
const postgres = { id: "wl-pg", name: "PostgreSQL", kind: "oci", status: "running", node_id: "node-1" };
const longName = "this-is-an-extremely-long-workload-name-that-must-truncate-in-the-sidebar";
const longWl = { id: "wl-long", name: longName, kind: "system-container", status: "stopped", node_id: "node-1" };
const host = { id: "node-1", name: "no-dal-01", status: "available", role: "control", listen_addr: "127.0.0.1:8080" };

function mockApi(me: MeResponse, extra: Record<string, { status: number; body?: unknown }> = {}, bulk = false) {
  const workloads = bulk
    ? Array.from({ length: 210 }, (_, i) => ({
        id: `wl-bulk-${String(i).padStart(3, "0")}`,
        name: `bulk-${String(i).padStart(3, "0")}`,
        kind: "system-container",
        status: i % 2 === 0 ? "running" : "stopped",
        node_id: "node-1",
      }))
    : [alpine, sound, ubuntu, stopped, postgres, longWl];
  const routes: Record<string, { status: number; body?: unknown }> = {
    "/api/v1/health": { status: 200, body: { status: "ok", service: "ndl-control" } },
    "/api/v1/setup/status": { status: 200, body: { open: false } },
    "/api/v1/me": { status: 200, body: me },
    "/api/v1/nodes": { status: 200, body: { items: [host] } },
    "/api/v1/nodes/node-1": { status: 200, body: host },
    "/api/v1/workloads": { status: 200, body: { items: workloads } },
    "/api/v1/workloads/wl-a": { status: 200, body: alpine },
    "/api/v1/workloads/wl-s": { status: 200, body: sound },
    "/api/v1/workloads/wl-u": { status: 200, body: ubuntu },
    "/api/v1/workloads/wl-u/guest": {
      status: 200,
      body: { qemu_ga: { state: "ok" }, nodal_ga: { state: "ok" } },
    },
    "/api/v1/workloads/wl-stop": { status: 200, body: stopped },
    "/api/v1/workloads/wl-pg": { status: 200, body: postgres },
    "/api/v1/workloads/wl-long": { status: 200, body: longWl },
    "/api/v1/stacks": { status: 200, body: { items: [] } },
    "/api/v1/events": { status: 200, body: { items: [] } },
    "/api/v1/timeline": { status: 200, body: { items: [] } },
    "/api/v1/alerts": { status: 200, body: { items: [] } },
    "/api/v1/alerts/channels": { status: 200, body: { items: [] } },
    "/api/v1/tasks": { status: 200, body: { items: [] } },
    "/api/v1/storage/pools": { status: 200, body: { items: [] } },
    "/api/v1/networks": { status: 200, body: { items: [], nics: [] } },
    "/api/v1/cluster": { status: 200, body: { id: "cluster-1", name: "local", nodes: [] } },
    ...extra,
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    if (path.endsWith("/terminal/sessions") && method === "POST") {
      return jsonResponse(201, { id: "s1", ticket: "t1", cwd: "/", jail_root: "/var/lib/ndl/ct" });
    }
    const hit = routes[`${method} ${path}`] ?? routes[path];
    if (!hit) {
      return jsonResponse(404, { error: `unmocked ${path}` });
    }
    return jsonResponse(hit.status, hit.body);
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal(
    "WebSocket",
    class {
      static OPEN = 1;
      readyState = 1;
      binaryType = "arraybuffer";
      onopen: (() => void) | null = null;
      constructor() {
        queueMicrotask(() => this.onopen?.());
      }
      send() {}
      close() {}
    },
  );
  return fetchMock;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

function expandSidebar() {
  localStorage.setItem("ndl-sidebar-collapsed", "0");
}

function navTarget(kind: string, id: string): HTMLElement {
  const el = document.querySelector(`[data-nav-id="${kind}:${id}"]`);
  if (!el) {
    throw new Error(`missing ${kind}:${id}`);
  }
  return el as HTMLElement;
}

async function findNavTarget(kind: string, id: string): Promise<HTMLElement> {
  await waitFor(() => {
    expect(document.querySelector(`[data-nav-id="${kind}:${id}"]`)).toBeTruthy();
  });
  return navTarget(kind, id);
}

describe("contextual navigation", () => {
  beforeEach(() => {
    expandSidebar();
  });

  it("keeps the main sidebar until Workloads is opened", async () => {
    window.history.replaceState({}, "", "/");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("navigation", { name: /appliance/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^workloads$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^storage$/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /back to main menu/i })).not.toBeInTheDocument();
    expect(document.querySelectorAll("nav.sidebar-nav")).toHaveLength(1);
    expect(document.querySelectorAll("aside.sidebar")).toHaveLength(1);
  });

  it("replaces the main sidebar with Workloads contextual navigation", async () => {
    expandSidebar();
    window.history.replaceState({}, "", "/");
    mockApi(admin);
    render(<App />);
    fireEvent.click(await screen.findByRole("link", { name: /^workloads$/i }));
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
    expect(screen.queryByRole("navigation", { name: /appliance/i })).not.toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: /^workloads$/i })).toBeVisible();
    expect(document.querySelectorAll("nav.sidebar-nav")).toHaveLength(1);
    expect(await findNavTarget("node", "node-1")).toBeVisible();
    expect(navTarget("node", "node-1").textContent).toContain("●");
    expect(navTarget("node", "node-1").textContent).toMatch(/Host/i);
    expect(await findNavTarget("workload", "wl-a")).toBeVisible();
    expect(navTarget("workload", "wl-a").textContent).toContain("●");
    expect(navTarget("workload", "wl-u")).toBeVisible();
    expect(navTarget("workload", "wl-pg")).toBeVisible();
    expect(navTarget("workload", "wl-stop").textContent).toContain("○");
    expect(screen.getByRole("button", { name: /^hosts$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^system containers$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^virtual machines$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^applications$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^manage$/i })).toBeVisible();
    expect(screen.getByRole("heading", { name: /^workloads$/i })).toBeVisible();
  });

  it("opens a direct workload URL in Workloads context and restores main nav from Back to Main Menu", async () => {
    window.history.replaceState({}, "", "/workloads/wl-a");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^summary$/i })).toHaveAttribute("aria-current", "page");
    fireEvent.click(screen.getByRole("button", { name: /back to main menu/i }));
    expect(await screen.findByRole("navigation", { name: /appliance/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^storage$/i })).toBeVisible();
    expect(screen.getByRole("heading", { name: /^alpine$/i })).toBeVisible();
    fireEvent.click(within(screen.getByRole("navigation", { name: /appliance/i })).getByRole("link", { name: /^workloads$/i }));
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
  });

  it("strips a stale main-nav history flag so refresh of a deep link stays contextual", async () => {
    window.history.replaceState({ ndlNav: "main" }, "", "/workloads/wl-a/terminal");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
    expect(screen.queryByRole("navigation", { name: /appliance/i })).not.toBeInTheDocument();
  });

  it("keeps browser back and forward on workload routes inside the operator sidebar", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
    fireEvent.click(await screen.findByRole("treeitem", { name: /alpine/i }));
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
    fireEvent.click(screen.getByRole("treeitem", { name: /sounddock/i }));
    expect(await screen.findByRole("heading", { name: /^sounddock$/i })).toBeVisible();
    window.history.back();
    await waitFor(() => expect(window.location.pathname).toBe("/workloads/wl-a"));
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /back to main menu/i })).toBeVisible();
    window.history.forward();
    await waitFor(() => expect(window.location.pathname).toBe("/workloads/wl-s"));
    expect(await screen.findByRole("heading", { name: /^sounddock$/i })).toBeVisible();
  });

  it("switches operational surfaces immediately and remembers terminal when the next target allows it", async () => {
    window.history.replaceState({}, "", "/workloads/wl-a");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
    fireEvent.click(screen.getByRole("link", { name: /^terminal$/i }));
    await waitFor(() => expect(window.location.pathname).toBe("/workloads/wl-a/terminal"));
    fireEvent.click(await findNavTarget("workload", "wl-s"));
    await waitFor(() => expect(window.location.pathname).toBe("/workloads/wl-s/terminal"));
    fireEvent.click(navTarget("node", "node-1"));
    await waitFor(() => expect(window.location.pathname).toBe("/nodes/node-1/terminal"));
    fireEvent.click(navTarget("workload", "wl-stop"));
    await waitFor(() => expect(window.location.pathname).toBe("/workloads/wl-stop"));
  });

  it("filters the tree by name, type, and node and keeps create plus manage", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(admin);
    render(<App />);
    const search = await screen.findByLabelText(/^search targets$/i);
    fireEvent.change(search, { target: { value: "sound" } });
    expect(screen.getByRole("treeitem", { name: /sounddock/i })).toBeVisible();
    expect(screen.queryByRole("treeitem", { name: /alpine/i })).not.toBeInTheDocument();
    fireEvent.change(search, { target: { value: "virtual" } });
    expect(screen.getByRole("treeitem", { name: /ubuntu/i })).toBeVisible();
    fireEvent.change(search, { target: { value: "no-dal-01" } });
    expect(navTarget("node", "node-1")).toBeVisible();
    expect(navTarget("workload", "wl-a")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^virtual machines$/i }));
    expect(screen.getByRole("button", { name: /^virtual machines$/i })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("treeitem", { name: /ubuntu/i })).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem("ndl-ctx-groups") || "{}").vm).toBe(false);
    fireEvent.click(screen.getByLabelText(/^create$/i));
    expect(screen.getByRole("link", { name: /^system container$/i })).toHaveAttribute("href", "/workloads/new/system-container");
    fireEvent.click(screen.getByRole("link", { name: /^manage$/i }));
    expect(await screen.findByRole("heading", { name: /^workloads$/i })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: /^name$/i })).toBeVisible();
  });

  it("hides unauthorized rows and still enforces action permissions", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(viewer);
    render(<App />);
    expect(await screen.findByRole("treeitem", { name: /alpine/i })).toBeVisible();
    expect(screen.queryByText(/^create$/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("treeitem", { name: /alpine/i }));
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^start$/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: /^terminal$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/operator or admin/i);
  });

  it("shows hosts to operators without treating that as host-terminal authorization", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(operator);
    render(<App />);
    fireEvent.click(await findNavTarget("node", "node-1"));
    expect(await screen.findByRole("heading", { name: /^no-dal-01$/i })).toBeVisible();
    expect(screen.getByText(/appliance host/i)).toBeVisible();
    fireEvent.click(screen.getByRole("link", { name: /^terminal$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/host terminal requires admin/i);
  });

  it("truncates long names and caps very large lists until search narrows them", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(admin);
    render(<App />);
    const longItem = await screen.findByRole("treeitem", { name: new RegExp(longName, "i") });
    expect(longItem.querySelector(".ctx-item-label")).toBeTruthy();
    expect(longItem.getAttribute("title")).toContain(longName);
    cleanup();
    mockApi(admin, {}, true);
    window.history.replaceState({}, "", "/workloads");
    render(<App />);
    expect(await screen.findByText(/showing 200 of 211/i)).toBeVisible();
    expect(screen.getByLabelText(/collapse sidebar/i).closest(".sidebar-footer")).toBeTruthy();
    expect(document.querySelectorAll(".ctx-item").length).toBeGreaterThan(50);
    fireEvent.change(await screen.findByLabelText(/^search targets$/i), { target: { value: "bulk-205" } });
    expect(screen.getByRole("treeitem", { name: /bulk-205/i })).toBeVisible();
    expect(screen.queryByText(/showing 200 of/i)).not.toBeInTheDocument();
  });

  it("leaves Node, Storage, Network, and Cluster on the main sidebar", async () => {
    window.history.replaceState({}, "", "/storage");
    mockApi(admin);
    render(<App />);
    expect(await screen.findByRole("navigation", { name: /appliance/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^node$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^network$/i })).toBeVisible();
    expect(screen.getByRole("link", { name: /^cluster$/i })).toBeVisible();
    fireEvent.click(screen.getByRole("link", { name: /^node$/i }));
    await waitFor(() => expect(window.location.pathname).toBe("/node"));
    expect(screen.getByRole("navigation", { name: /appliance/i })).toBeVisible();
  });

  it("supports arrow and enter selection from the search field", async () => {
    window.history.replaceState({}, "", "/workloads");
    mockApi(admin);
    render(<App />);
    const search = await screen.findByLabelText(/^search targets$/i);
    fireEvent.change(search, { target: { value: "alp" } });
    fireEvent.keyDown(search, { key: "Enter" });
    expect(await screen.findByRole("heading", { name: /^alpine$/i })).toBeVisible();
  });
});
