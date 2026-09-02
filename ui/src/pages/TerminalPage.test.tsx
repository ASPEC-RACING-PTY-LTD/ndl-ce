import { cleanup, createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
});
