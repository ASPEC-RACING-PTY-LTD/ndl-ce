import { cleanup, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";
import { encodeFrame } from "../terminal/frames";

vi.mock("../components/FileEditor", () => ({ FileEditor: () => null }));

const admin: MeResponse = {
  user_id: "user-1",
  username: "admin",
  roles: ["admin"],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
};

const operator: MeResponse = {
  user_id: "user-3",
  username: "oper",
  roles: ["operator"],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
};

const viewer: MeResponse = {
  user_id: "user-2",
  username: "view",
  roles: ["viewer"],
  edition: "ce",
  ux_level: "expert",
  expert_ack: true,
};

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body ?? {}), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type FakeWS = {
  readyState: number;
  onopen: (() => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onclose: (() => void) | null;
  onerror: (() => void) | null;
  send: (data: ArrayBuffer | Uint8Array) => void;
  close: () => void;
  pushCwd: (path: string) => void;
};

const sent: { tab: number; text: string; type: number }[] = [];
const sockets: FakeWS[] = [];
const created: { path: string; body: string }[] = [];
const uploads: { path: string }[] = [];
let sessionN = 0;
let xhrMode: "ok" | "fail" | "hang" = "ok";
let me: MeResponse = admin;
let started: string[] = [];

function installIO() {
  sent.length = 0;
  sockets.length = 0;
  created.length = 0;
  uploads.length = 0;
  started = [];
  sessionN = 0;
  class FakeSocket {
    static OPEN = 1;
    readyState = 1;
    binaryType = "arraybuffer";
    onopen: (() => void) | null = null;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: (() => void) | null = null;
    constructor() {
      sockets.push(this);
      queueMicrotask(() => this.onopen?.());
    }
    send(data: ArrayBuffer | Uint8Array) {
      const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
      sent.push({ tab: sockets.indexOf(this), type: bytes[0] ?? 0, text: new TextDecoder().decode(bytes.slice(5)) });
    }
    close() {
      this.readyState = 3;
      this.onclose?.();
    }
    pushCwd(path: string) {
      const payload = new TextEncoder().encode(path);
      const frame = encodeFrame(6, payload);
      this.onmessage?.({ data: frame.buffer } as MessageEvent);
    }
  }
  vi.stubGlobal("WebSocket", FakeSocket);
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    if (path === "/api/v1/health") return jsonResponse(200, { status: "ok", service: "ndl-control" });
    if (path === "/api/v1/setup/status") return jsonResponse(200, { open: false });
    if (path === "/api/v1/me") return jsonResponse(200, me);
    if (path === "/api/v1/nodes") {
      return jsonResponse(200, {
        items: [
          { id: "node-1", name: "no-dal-01", status: "available" },
          { id: "node-2", name: "no-dal-02", status: "available" },
        ],
      });
    }
    if (path === "/api/v1/nodes/node-1") {
      return jsonResponse(200, { id: "node-1", name: "no-dal-01", status: "available" });
    }
    if (path === "/api/v1/workloads") {
      return jsonResponse(200, {
        items: [
          { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-2" },
          { id: "wl-u", name: "Ubuntu", kind: "vm", status: "running", node_id: "node-1" },
          { id: "wl-stop", name: "Test VM", kind: "vm", status: "stopped", node_id: "node-1" },
        ],
      });
    }
    if (path === "/api/v1/workloads/wl-a") {
      return jsonResponse(200, { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-2" });
    }
    if (path === "/api/v1/workloads/wl-u/guest") {
      return jsonResponse(200, { nodal_ga: { state: "ok" }, qemu_ga: { state: "ok" } });
    }
    if (path === "/api/v1/workloads/wl-stop/start" && method === "POST") {
      started.push("wl-stop");
      return jsonResponse(200, { id: "wl-stop", name: "Test VM", kind: "vm", status: "running" });
    }
    if (path.endsWith("/terminal/sessions") && method === "POST") {
      sessionN += 1;
      created.push({ path, body: typeof init?.body === "string" ? init.body : "" });
      return jsonResponse(201, {
        id: `s${sessionN}`,
        ticket: `t${sessionN}`,
        cwd: "/",
        jail_root: "/var/lib/ndl/ct",
        node_id: path.includes("/nodes/") ? "node-1" : "node-2",
      });
    }
    if (path.endsWith("/files/mkdir")) return jsonResponse(201, { ok: true });
    if (path === "/api/v1/stacks") return jsonResponse(200, { items: [] });
    return jsonResponse(200, { items: [] });
  });
  vi.stubGlobal("fetch", fetchMock);
  class FakeXHR {
    status = xhrMode === "fail" ? 500 : 201;
    responseText = xhrMode === "fail" ? JSON.stringify({ error: "disk full" }) : JSON.stringify({ ok: true });
    withCredentials = false;
    upload = { onprogress: null as ((ev: ProgressEvent) => void) | null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    private dest = "";
    open(...args: string[]) {
      this.dest = args[1] ?? "";
    }
    send(body?: Document | XMLHttpRequestBodyInit | null) {
      const form = body as FormData | undefined;
      const rel = typeof form?.get === "function" ? String(form.get("path") ?? "") : "";
      uploads.push({ path: rel || this.dest });
      if (xhrMode === "hang") {
        return;
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

async function openWorkspace() {
  window.history.replaceState({}, "", "/terminal");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /^terminal$/i })).toBeVisible();
}

async function clickTarget(kind: string, id: string) {
  const sel = `[data-target="${kind}:${id}"]`;
  await waitFor(() => {
    expect(document.querySelector(sel)).toBeTruthy();
  });
  fireEvent.click(document.querySelector(sel) as HTMLElement);
}

async function waitConnected() {
  await waitFor(() => {
    expect(document.querySelector(".term-conn.is-active")).toHaveTextContent(/connected/i);
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  xhrMode = "ok";
  me = admin;
  window.history.replaceState({}, "", "/");
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

describe("Terminal workspace", () => {
  it("creates independent sessions against the same target and keeps siblings when one closes", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    const dialog = await screen.findByRole("dialog", { name: /quick switch/i });
    fireEvent.change(screen.getByLabelText(/^search$/i), { target: { value: "alp" } });
    fireEvent.keyDown(dialog, { key: "Enter" });
    expect(await screen.findByRole("tab", { name: /alpine/i })).toBeVisible();
    expect(created).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: /session actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /new terminal here/i }));
    await waitFor(() => expect(screen.getAllByRole("tab").length).toBeGreaterThanOrEqual(2));
    fireEvent.click(screen.getByRole("button", { name: /session actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /new terminal here/i }));
    await waitFor(() => expect(created.length).toBe(3));
    expect(new Set(created.map((c) => c.path)).size).toBe(1);
    expect(created.every((c) => c.path.includes("/workloads/wl-a/terminal/sessions"))).toBe(true);

    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((t) => t.textContent).join(" ")).toMatch(/Alpine \(2\)/);
    expect(tabs.map((t) => t.textContent).join(" ")).toMatch(/Alpine \(3\)/);

    vi.spyOn(window, "prompt").mockReturnValue("Alpine: LIVE LOGS");
    fireEvent.doubleClick(tabs[0]);
    expect(screen.getByRole("tab", { name: /alpine: live logs/i })).toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: /alpine \(2\)/i }));
    fireEvent.click(screen.getByRole("button", { name: /session actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /close session/i }));
    expect(screen.queryByRole("tab", { name: /alpine \(2\)/i })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /alpine: live logs/i })).toBeVisible();
    expect(screen.getByRole("tab", { name: /alpine \(3\)/i })).toBeVisible();
  });

  it("does not reconnect same-target sessions when switching tabs", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("workload", "wl-a");
    await waitConnected();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("workload", "wl-a");
    await waitFor(() => expect(created.length).toBe(2));
    await waitFor(() => expect(sockets.length).toBe(2));
    expect(new Set(screen.getAllByRole("tab").map((el) => el.getAttribute("data-io-session"))).size).toBe(2);
    expect(new Set(screen.getAllByRole("tab").map((el) => el.getAttribute("data-tab-id"))).size).toBe(2);

    sockets[0]?.pushCwd("/tmp");
    sockets[1]?.pushCwd("/etc");
    const first = screen.getAllByRole("tab")[0];
    const second = screen.getAllByRole("tab")[1];
    fireEvent.click(first);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/tmp/));
    const createdAt = created.length;
    const openCount = () => sockets.filter((s) => s.readyState === 1).length;
    expect(openCount()).toBe(2);
    for (let i = 0; i < 8; i += 1) {
      fireEvent.click(i % 2 === 0 ? second : first);
    }
    expect(created.length).toBe(createdAt);
    expect(openCount()).toBe(2);
    fireEvent.click(first);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/tmp/));
    fireEvent.click(second);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/etc/));

    fireEvent.click(first);
    fireEvent.click(screen.getByRole("button", { name: /session actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /close session/i }));
    expect(openCount()).toBe(1);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/etc/));
    expect(created.length).toBe(createdAt);
  });

  it("opens independent host sessions against the same node", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("node", "node-1");
    await waitConnected();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("node", "node-1");
    await waitFor(() => expect(created.length).toBe(2));
    expect(created.every((c) => c.path.includes("/nodes/node-1/terminal/sessions"))).toBe(true);
    expect(screen.getAllByRole("tab")).toHaveLength(2);
    expect(new Set(screen.getAllByRole("tab").map((el) => el.getAttribute("data-io-session"))).size).toBe(2);
    sockets[0]?.pushCwd("/root");
    sockets[1]?.pushCwd("/var");
    fireEvent.click(screen.getAllByRole("tab")[0]);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/root/));
    fireEvent.click(screen.getAllByRole("tab")[1]);
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/var/));
    expect(sockets.filter((s) => s.readyState === 1)).toHaveLength(2);
  });

  it("opens host and workload sessions together with distinct identity", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await waitFor(() => expect(document.querySelector('[data-target="node:node-1"]')).toBeTruthy());
    await clickTarget("node", "node-1");
    expect(await screen.findByTestId("term-identity")).toHaveAttribute("data-target-kind", "node");
    expect(screen.getByText(/^host$/i, { selector: ".term-host-badge" })).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    const dialog = await screen.findByRole("dialog", { name: /quick switch/i });
    fireEvent.change(screen.getByLabelText(/^search$/i), { target: { value: "ubuntu" } });
    fireEvent.keyDown(dialog, { key: "Enter" });
    await waitFor(() => expect(screen.getAllByRole("tab").length).toBe(2));
    fireEvent.click(screen.getByRole("tab", { name: /ubuntu/i }));
    expect(screen.getByTestId("term-identity")).toHaveAttribute("data-target-kind", "workload");
    expect(screen.getByText(/virtual machine/i)).toBeVisible();
  });

  it("keeps sessions alive when navigating away and restores disconnected after reload metadata", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("workload", "wl-a");
    await waitConnected();
    expect(created).toHaveLength(1);
    fireEvent.click(screen.getByRole("link", { name: /^workloads$/i }));
    expect(await screen.findByRole("heading", { name: /workloads/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /back to main menu/i }));
    fireEvent.click(screen.getByRole("link", { name: /^storage$/i }));
    expect(await screen.findByRole("heading", { name: /^storage$/i })).toBeVisible();
    fireEvent.click(screen.getByRole("link", { name: /^workloads$/i }));
    expect(await screen.findByRole("button", { name: /back to main menu/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /back to main menu/i }));
    fireEvent.click(screen.getByRole("link", { name: /^terminal$/i }));
    expect(await screen.findByRole("tab", { name: /alpine/i })).toBeVisible();
    expect(created).toHaveLength(1);
    expect(document.querySelector(".term-conn.is-active")).toHaveTextContent(/connected/i);
  });

  it("shows disconnected honestly after a socket close and reconnects with a new PTY", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("workload", "wl-a");
    await waitConnected();
    sockets[0]?.close();
    await waitFor(() => {
      expect(document.querySelector(".term-conn.is-disconnected")).toHaveTextContent(/disconnected/i);
    });
    fireEvent.click(screen.getByRole("button", { name: /^reconnect$/i }));
    await waitFor(() => expect(created.length).toBe(2));
    await waitConnected();
  });

  it("scopes file-drop cwd to the active session", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    expect(await screen.findByRole("dialog", { name: /quick switch/i })).toBeVisible();
    await clickTarget("workload", "wl-a");
    await waitConnected();
    fireEvent.click(screen.getByRole("button", { name: /session actions/i }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /new terminal here/i }));
    await waitFor(() => expect(sockets.length).toBe(2));
    sockets[0]?.pushCwd("/app");
    sockets[1]?.pushCwd("/var/db");
    fireEvent.click(screen.getByRole("tab", { name: /^alpine connected/i }));
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/app/));
    const wrap = screen.getByTestId("term-wrap");
    const dt = { files: [new File(["hi"], "my file.txt")], types: ["Files"], dropEffect: "none" };
    fireEvent(wrap, createEvent.drop(wrap, { dataTransfer: dt }));
    await waitFor(() => expect(sent.some((s) => s.text.includes("/app/my file.txt"))).toBe(true));
    fireEvent.click(screen.getByRole("tab", { name: /alpine \(2\)/i }));
    await waitFor(() => expect(screen.getByTestId("term-identity").textContent).toMatch(/\/var\/db/));
    fireEvent(wrap, createEvent.drop(wrap, { dataTransfer: dt }));
    await waitFor(() => expect(sent.some((s) => s.text.includes("/var/db/my file.txt"))).toBe(true));
    expect(sent.map((s) => s.text).join("")).not.toMatch(/[\r\n]/);
  });

  it("does not silently start a stopped workload from Quick Switch", async () => {
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    const dialog = await screen.findByRole("dialog", { name: /quick switch/i });
    fireEvent.change(screen.getByLabelText(/^search$/i), { target: { value: "test vm" } });
    expect(within(dialog).getAllByText(/stopped/i).length).toBeGreaterThan(0);
    fireEvent.keyDown(dialog, { key: "Enter" });
    expect(started).toHaveLength(0);
    expect(created).toHaveLength(0);
    expect(await screen.findByRole("heading", { name: /start workload/i })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(started).toHaveLength(0);
  });

  it("hides hosts from operators and the workspace from viewers", async () => {
    me = operator;
    installIO();
    await openWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^quick switch$/i }));
    const dialog = await screen.findByRole("dialog", { name: /quick switch/i });
    expect(dialog.querySelector('[data-target="node:node-1"]')).toBeNull();
    expect(dialog.querySelector('[data-target="workload:wl-a"]')).toBeTruthy();
    cleanup();
    me = viewer;
    installIO();
    window.history.replaceState({}, "", "/terminal");
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/operator or admin/i);
  });

  it("keeps per-workload Terminal on the same session host", async () => {
    installIO();
    window.history.replaceState({}, "", "/workloads/wl-a/terminal");
    render(<App />);
    expect(await screen.findByRole("heading", { name: /^terminal$/i })).toBeVisible();
    await waitConnected();
    fireEvent.click(screen.getByRole("link", { name: /open in terminal workspace/i }));
    expect(await screen.findByRole("tab", { name: /alpine/i })).toBeVisible();
    expect(created).toHaveLength(1);
  });

  it("sends a throttled PTY resize when the terminal box changes", async () => {
    installIO();
    window.history.replaceState({}, "", "/workloads/wl-a/terminal");
    render(<App />);
    await waitConnected();
    const wrap = screen.getByTestId("term-wrap");
    Object.defineProperty(wrap, "clientWidth", { configurable: true, value: 810 });
    Object.defineProperty(wrap, "clientHeight", { configurable: true, value: 340 });
    fireEvent(window, new Event("resize"));
    await waitFor(() => {
      expect(sent.some((s) => s.type === 3)).toBe(true);
    });
    const resizes = sent.filter((s) => s.type === 3);
    expect(resizes.length).toBeGreaterThanOrEqual(1);
    expect(resizes.length).toBeLessThan(8);
  });
});
