import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DDoSOverview } from "../api/client";
import { SessionProvider } from "../session";
import { DDoSPage } from "./DDoSPage";

const me = { user_id: "u1", username: "admin", roles: ["superadmin"], edition: "ce", ux_level: "guided", expert_ack: false };

const source = {
  id: "source",
  title: "Source engine",
  summary: "Garry's Mod and other Source servers.",
  protocol: "both" as const,
  action: "ban" as const,
  limits: { per_source_pps: 400, per_source_burst: 1200, query_per_source: 5, block_amplification: true, drop_fragments: true },
};

function overview(attack: boolean): DDoSOverview {
  return {
    settings: {
      mode: "enforce",
      interfaces: [],
      trusted: [],
      trust_local: true,
      blocked: [],
      drop_invalid_tcp: true,
      hardening: true,
      auto_game_servers: true,
      attack_pps: 1000,
      notify: true,
    },
    targets: [
      {
        id: "auto:gs:g1",
        name: "JvS",
        kind: "gameserver",
        ref_id: "g1",
        ref_name: "JvS",
        enabled: true,
        profile: "source",
        protocol: "both",
        ports: ["27015", "27016"],
        dest: null,
        limits: source.limits,
        action: "ban",
        allow: null,
        block: null,
        auto: true,
        rule: { ports: ["27015", "27016"], host: true, protocol: "both" },
        traffic: {
          in_packets: 90000,
          dropped_packets: 60000,
          dropped_bytes: 3600000,
          reasons: { query: { packets: 50000, bytes: 1 }, flood: { packets: 10000, bytes: 1 } },
          bans: 12,
          in_pps: attack ? 52000 : 120,
          drop_pps: attack ? 51000 : 0,
          under_attack: attack,
        },
        history: [
          { at: "2026-10-27T05:00:00Z", in_pps: 100, drop_pps: 0 },
          { at: "2026-10-27T05:00:15Z", in_pps: 52000, drop_pps: 51000 },
        ],
      },
    ],
    bans: [{ addr: "203.0.113.5", target_id: "auto:gs:g1", target: "JvS", seconds: 240, manual: false }],
    status: {
      active: true,
      mode: "enforce",
      devices: ["enp6s0"],
      host: ["192.168.2.82"],
      blocked: { packets: 0, bytes: 0 },
      banned: { packets: 5, bytes: 300 },
      conntrack: { count: 371, max: 262144 },
      hardening: [],
    },
    profiles: [source],
    amp_ports: [53, 123],
    under_attack: attack,
  };
}

function mock(attack: boolean) {
  const calls: { method: string; path: string; body?: string }[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({ method, path, body: typeof init?.body === "string" ? init.body : undefined });
    const body =
      path === "/api/v1/me"
        ? me
        : path === "/api/v1/ddos" || path.startsWith("/api/v1/ddos/bans") || path === "/api/v1/ddos/settings"
          ? overview(attack)
          : path === "/api/v1/ddos/events"
            ? { items: [] }
            : { items: [] };
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("DDoS protection page", () => {
  it("shows the protected game server, its traffic and an attack", async () => {
    mock(true);
    render(
      <SessionProvider>
        <DDoSPage />
      </SessionProvider>,
    );
    expect(await screen.findByRole("heading", { name: /ddos protection/i })).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent(/attack in progress on jvs: dropping 51,000 packets per second/i);
    const row = screen.getByRole("link", { name: "JvS" }).closest("tr") as HTMLElement;
    expect(within(row).getByText(/protected by default/i)).toBeVisible();
    expect(within(row).getByText(/This node · UDP and TCP 27015, 27016/)).toBeVisible();
    expect(within(row).getByText("Under attack")).toBeVisible();
    expect(within(row).getByText(/server query floods · 12 banned/i)).toBeVisible();
    expect(screen.getByText("Enforcing")).toBeVisible();
  });

  it("unbans from the Bans tab and saves settings", async () => {
    const calls = mock(false);
    render(
      <SessionProvider>
        <DDoSPage />
      </SessionProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: /bans \(1\)/i }));
    fireEvent.click(screen.getByRole("button", { name: "Unban" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/v1/ddos/bans/203.0.113.5")).toBe(true));

    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Monitor only" }));
    fireEvent.change(screen.getByLabelText("Trusted networks"), { target: { value: "203.0.113.4\n198.51.100.0/24" } });
    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.path === "/api/v1/ddos/settings")).toBe(true));
    const put = JSON.parse(calls.find((c) => c.method === "PUT")?.body ?? "{}");
    expect(put.mode).toBe("monitor");
    expect(put.trusted).toEqual(["203.0.113.4", "198.51.100.0/24"]);
  });
});
