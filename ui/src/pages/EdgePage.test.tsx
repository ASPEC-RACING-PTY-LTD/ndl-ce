import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { EdgeOverview } from "../api/client";
import { SessionProvider } from "../session";
import { EdgePage, parsePorts } from "./EdgePage";

const me = {
  user_id: "u1",
  username: "admin",
  roles: ["superadmin"],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
};

function overview(oldAgent: boolean): EdgeOverview {
  return {
    nodes: [
      {
        id: "home",
        name: "local",
        role: "control",
        local: true,
        checked_at: "2026-10-28T04:00:00Z",
      },
      {
        id: "vps",
        name: "r-server71377-llv",
        role: "worker",
        local: false,
        public_addr: "45.38.149.183",
        error: oldAgent ? "This node's No-dal agent is too old for this. Update No-dal on it." : undefined,
      },
    ],
    tunnels: [
      {
        id: "t1",
        edge_node_id: "vps",
        origin_node_id: "home",
        interface: "ndle1234abcd",
        edge_addr: "10.66.0.5",
        origin_addr: "10.66.0.6",
        listen_port: 51871,
        connected: !oldAgent,
        handshake: oldAgent ? undefined : "2026-10-28T04:00:00Z",
        rx_bytes: 2048,
        tx_bytes: 4096,
        egress: ["172.17.0.2"],
      },
    ],
    forwards: [
      {
        id: "f1",
        name: "JvS",
        kind: "gameserver",
        ref_id: "g1",
        edge_node_id: "vps",
        ports: [],
        keep_address: false,
        egress: true,
        enabled: true,
        ref_name: "JvS",
        edge_name: "r-server71377-llv",
        origin_name: "local",
        public_addr: "45.38.149.183",
        resolved_ports: [{ protocol: "udp", port: 27015 }],
        tunnel_id: "t1",
        packets: 1234,
        bytes: 99000,
        active: true,
      },
    ],
  };
}

function mock(oldAgent: boolean) {
  const calls: { method: string; path: string; confirm?: string | null }[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({
      method,
      path,
      confirm: new Headers(init?.headers).get("X-Nodal-Confirm"),
    });
    const body = path === "/api/v1/me" ? me : path.startsWith("/api/v1/edge") ? overview(oldAgent) : path.endsWith("/update") ? { status: "running" } : { items: [] };
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Edge forwarding page", () => {
  it("shows where players connect, the tunnel and that their addresses are passed on", async () => {
    mock(false);
    render(
      <SessionProvider>
        <EdgePage />
      </SessionProvider>,
    );
    expect(await screen.findByRole("heading", { name: /edge forwarding/i })).toBeVisible();
    const row = screen.getByRole("link", { name: "JvS" }).closest("tr") as HTMLElement;
    expect(within(row).getByText("45.38.149.183:27015")).toBeVisible();
    expect(within(row).getByText(/player addresses passed on/)).toBeVisible();
    expect(within(row).getByText(/outbound through the edge/)).toBeVisible();
    expect(within(row).getByText("Live")).toBeVisible();
    expect(screen.getByText("Connected")).toBeVisible();
  });

  it("offers to update a worker whose agent is too old", async () => {
    const calls = mock(true);
    vi.stubGlobal("confirm", () => true);
    render(
      <SessionProvider>
        <EdgePage />
      </SessionProvider>,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Update No-dal on r-server71377-llv",
      }),
    );
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/nodes/vps/update" && c.confirm === "update-node")).toBe(true));
    expect(screen.getByText("Tunnel down")).toBeVisible();
  });

  it("reads ports one per line", () => {
    expect(parsePorts("udp 27015\nTCP 25565\nudp 27000-27010\nudp 7777 to 27016")).toEqual([
      { protocol: "udp", port: 27015 },
      { protocol: "tcp", port: 25565 },
      { protocol: "udp", port: 27000, port_end: 27010 },
      { protocol: "udp", port: 7777, to_port: 27016 },
    ]);
    expect(() => parsePorts("27015")).toThrow(/not a port/);
  });
});
