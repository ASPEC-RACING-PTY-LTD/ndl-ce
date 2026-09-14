import { act, cleanup, createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MeResponse } from "../api/types";

vi.mock("../components/FileEditor", () => ({ FileEditor: () => null }));

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

const sent: string[] = [];
const uploads: { path: string; cancelled?: boolean }[] = [];
let xhrMode: "ok" | "fail" | "hang" = "ok";
let hung = false;

function installIO() {
  sent.length = 0;
  uploads.length = 0;
  hung = false;
  class FakeWS {
    static OPEN = 1;
    readyState = 1;
    binaryType = "arraybuffer";
    onopen: (() => void) | null = null;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: (() => void) | null = null;
    constructor() {
      queueMicrotask(() => this.onopen?.());
    }
    send(data: ArrayBuffer | Uint8Array) {
      const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
      sent.push(new TextDecoder().decode(bytes.slice(5)));
    }
    close() {}
  }
  vi.stubGlobal("WebSocket", FakeWS);
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    if (path === "/api/v1/health") return jsonResponse(200, { status: "ok", service: "ndl-control" });
    if (path === "/api/v1/setup/status") return jsonResponse(200, { open: false });
    if (path === "/api/v1/me") return jsonResponse(200, admin);
    if (path === "/api/v1/workloads/wl-1") {
      return jsonResponse(200, { id: "wl-1", name: "accept-ct", kind: "system-container", status: "running" });
    }
    if (path.endsWith("/terminal/sessions")) {
      return jsonResponse(201, { id: "s1", ticket: "t1", cwd: "/root", jail_root: "/var/lib/ndl/ct" });
    }
    if (path.endsWith("/files/mkdir")) return jsonResponse(201, { ok: true });
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
        hung = true;
        return;
      }
      queueMicrotask(() => this.onload?.());
    }
    abort() {
      uploads[uploads.length - 1] = { ...uploads[uploads.length - 1], cancelled: true };
      this.onabort?.();
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
}

function dropOnTerm(files: File[]) {
  const wrap = screen.getByTestId("term-wrap");
  const dt = { files, types: ["Files"], dropEffect: "none" };
  const over = createEvent.dragOver(wrap, { dataTransfer: dt });
  fireEvent(wrap, over);
  expect(over.defaultPrevented).toBe(true);
  const drop = createEvent.drop(wrap, { dataTransfer: dt });
  fireEvent(wrap, drop);
  expect(drop.defaultPrevented).toBe(true);
}

async function openTerminal() {
  window.history.replaceState({}, "", "/workloads/wl-1/terminal?cwd=/root");
  render(<App />);
  expect(await screen.findByRole("heading", { name: /^terminal$/i })).toBeVisible();
  await screen.findByText(/connected/i);
  expect(screen.getByTestId("term-wrap")).toBeVisible();
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  xhrMode = "ok";
  hung = false;
  window.history.replaceState({}, "", "/");
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

describe("Terminal file drop", () => {
  it("uploads a single file and inserts a shell-escaped path without executing", async () => {
    installIO();
    await openTerminal();
    dropOnTerm([new File(["hi"], "my file.txt", { type: "text/plain" })]);
    await waitFor(() => {
      expect(sent.some((s) => s.includes("my file.txt"))).toBe(true);
    });
    expect(sent.join("")).toContain("'/root/my file.txt'");
    expect(sent.join("")).not.toContain("\n");
    expect(sent.join("")).not.toContain("\r");
  });

  it("uploads multiple files and inserts each escaped path", async () => {
    installIO();
    await openTerminal();
    dropOnTerm([
      new File(["a"], "a b.txt"),
      new File(["b"], `x"y$(rm).txt`),
    ]);
    await waitFor(() => expect(sent.length).toBeGreaterThan(0));
    const payload = sent.join("");
    expect(payload).toContain("'/root/a b.txt'");
    expect(payload).toContain(`'/root/x"y$(rm).txt'`);
    expect(payload).not.toMatch(/[\r\n]/);
  });

  it("shows a clear failure and does not insert a path", async () => {
    xhrMode = "fail";
    installIO();
    await openTerminal();
    dropOnTerm([new File(["x"], "nope.txt")]);
    expect(await screen.findByRole("alert")).toHaveTextContent(/disk full/i);
    expect(sent.join("")).not.toContain("nope.txt");
  });

  it("cancels an in-progress upload", async () => {
    xhrMode = "hang";
    installIO();
    await openTerminal();
    dropOnTerm([new File(["x"], "slow.txt")]);
    fireEvent.click(await screen.findByRole("button", { name: /cancel upload/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/cancelled/i);
    expect(sent.join("")).not.toContain("slow.txt");
    expect(hung).toBe(true);
  });

  it("asks before pasting three or more lines", async () => {
    installIO();
    await openTerminal();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const termEl = await screen.findByTestId("xterm");
    fireEvent.paste(termEl, { clipboardData: { getData: () => "a\nb\nc" } });
    expect(confirm).toHaveBeenCalled();
  });
});

function stubRect(el: Element, box: { top: number; left: number; width: number; height: number }) {
  vi.spyOn(el, "getBoundingClientRect").mockReturnValue({
    x: box.left,
    y: box.top,
    top: box.top,
    left: box.left,
    width: box.width,
    height: box.height,
    bottom: box.top + box.height,
    right: box.left + box.width,
    toJSON() {
      return {};
    },
  });
}

function dispatchPointer(target: EventTarget, type: string, init: MouseEventInit & { pointerId?: number }) {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, ...init });
  Object.defineProperty(event, "pointerId", { configurable: true, value: init.pointerId ?? 1 });
  act(() => {
    target.dispatchEvent(event);
  });
}

describe("Terminal sizing", () => {
  it("fills remaining space by default and keeps a resize handle", async () => {
    installIO();
    await openTerminal();
    const wrap = screen.getByTestId("term-wrap");
    expect(wrap.dataset.termSize).toBe("auto");
    expect(wrap.style.height).toMatch(/px$/);
    expect(parseFloat(wrap.style.height)).toBeGreaterThanOrEqual(200);
    expect(screen.getByRole("button", { name: /resize terminal/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /^reset size$/i })).not.toBeInTheDocument();
  });

  it("persists a dragged size across a new session and can reset to fill", async () => {
    installIO();
    await openTerminal();
    const wrap = screen.getByTestId("term-wrap");
    const main = wrap.closest(".shell-main");
    expect(main).toBeTruthy();
    stubRect(main as Element, { top: 0, left: 0, width: 1200, height: 900 });
    stubRect(wrap, { top: 160, left: 80, width: 480, height: 320 });
    const handle = screen.getByRole("button", { name: /resize terminal/i });
    dispatchPointer(handle, "pointerdown", { button: 0, buttons: 1, clientX: 560, clientY: 480, detail: 1, pointerId: 1 });
    dispatchPointer(handle, "pointermove", { button: 0, buttons: 1, clientX: 680, clientY: 600, pointerId: 1 });
    dispatchPointer(handle, "pointerup", { button: 0, buttons: 0, pointerId: 1 });
    expect(await screen.findByRole("button", { name: /^reset size$/i })).toBeVisible();
    expect(wrap.dataset.termSize).toBe("manual");
    expect(wrap.style.width).toBe("600px");
    expect(wrap.style.height).toBe("440px");
    const stored = JSON.parse(localStorage.getItem("ndl-term-size") || "{}") as { mode: string; width: number; height: number };
    expect(stored.mode).toBe("manual");
    expect(stored.width).toBe(600);
    expect(stored.height).toBe(440);

    cleanup();
    installIO();
    await openTerminal();
    const restored = screen.getByTestId("term-wrap");
    expect(restored.dataset.termSize).toBe("manual");
    expect(restored.style.width).toBe("600px");
    expect(restored.style.height).toBe("440px");
    expect(screen.getByRole("button", { name: /^reset size$/i })).toBeVisible();

    fireEvent.doubleClick(screen.getByRole("button", { name: /resize terminal/i }));
    expect(restored.dataset.termSize).toBe("auto");
    expect(restored.style.width).toBe("100%");
    expect(screen.queryByRole("button", { name: /^reset size$/i })).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem("ndl-term-size") || "{}").mode).toBe("auto");
  });
});
