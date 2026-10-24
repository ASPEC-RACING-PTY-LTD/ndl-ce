import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { decodeFrame, encodeFrame } from "../terminal/frames";
import { GameConsoleTerminal } from "./GameConsoleTerminal";

class FakeSocket {
  static last: FakeSocket | null = null;
  static OPEN = 1;
  url: string;
  protocols: string[];
  readyState = 0;
  binaryType = "";
  sent: Uint8Array[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: ArrayBuffer }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(url: string, protocols: string[]) {
    this.url = url;
    this.protocols = protocols;
    FakeSocket.last = this;
  }
  send(data: Uint8Array) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
  open() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
  }
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  FakeSocket.last = null;
});

describe("GameConsoleTerminal", () => {
  it("streams the console over the terminal socket and sends saved commands as typed lines", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        const path = new URL(url, "http://localhost").pathname;
        calls.push(`${init?.method ?? "GET"} ${path}`);
        if (path === "/api/v1/game-servers/gs-1/console/sessions") {
          return json(201, { id: "io-1", ticket: "t0k3n", target_kind: "gameserver", target_id: "gs-1", kind: "terminal", cwd: "/", state: "pending" });
        }
        if (path === "/api/v1/game-servers/gs-1/console") {
          return json(200, { log: "", running: true, favorites: [{ id: "f1", name: "Day", command: "time set day" }] });
        }
        if (path === "/api/v1/game-servers/gs-1/console/history") {
          return json(200, { items: ["list"] });
        }
        return json(204, {});
      }),
    );
    vi.stubGlobal("WebSocket", FakeSocket);

    render(<GameConsoleTerminal id="gs-1" name="Paper Lab" />);
    await waitFor(() => expect(FakeSocket.last).not.toBeNull());
    const sock = FakeSocket.last as FakeSocket;
    expect(sock.url).toMatch(/\/api\/v1\/io\/sessions\/io-1\/ws$/);
    expect(sock.protocols).toEqual(["ndl.ticket.t0k3n"]);
    expect(screen.getByText("Connecting")).toBeVisible();

    sock.open();
    expect(await screen.findByText("Live")).toBeVisible();
    // The PTY size goes out as soon as the socket opens.
    expect(sock.sent.some((f) => decodeFrame(f)?.type === 3)).toBe(true);

    // Output frames are accepted while connected.
    sock.onmessage?.({ data: encodeFrame(2, new TextEncoder().encode("[Server] Done!\n")).buffer as ArrayBuffer });

    fireEvent.click(await screen.findByRole("button", { name: "Day" }));
    const typed = sock.sent.map((f) => decodeFrame(f)).filter((f) => f?.type === 1);
    expect(new TextDecoder().decode(typed.at(-1)?.payload)).toBe("\x15time set day\r");
  });

  it("ends its console session when it closes", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        const path = new URL(url, "http://localhost").pathname;
        calls.push(`${init?.method ?? "GET"} ${path}`);
        if (path.endsWith("/console/sessions")) {
          return json(201, { id: "io-2", ticket: "x", target_kind: "gameserver", target_id: "gs-1", kind: "terminal", cwd: "/", state: "pending" });
        }
        if (path.endsWith("/console")) {
          return json(200, { log: "", running: false, favorites: [] });
        }
        return json(200, { items: [] });
      }),
    );
    vi.stubGlobal("WebSocket", FakeSocket);
    const view = render(<GameConsoleTerminal id="gs-1" name="Paper Lab" />);
    await waitFor(() => expect(FakeSocket.last).not.toBeNull());
    FakeSocket.last?.open();
    expect(await screen.findByText("Server stopped")).toBeVisible();
    view.unmount();
    await waitFor(() => expect(calls).toContain("POST /api/v1/io/sessions/io-2/end"));
  });
});
