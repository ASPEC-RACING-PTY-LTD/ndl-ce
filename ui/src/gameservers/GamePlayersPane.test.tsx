import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GamePlayersPane } from "./GamePlayersPane";

function mock(players: unknown) {
  const calls: { method: string; path: string; body?: string }[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    calls.push({ method: (init?.method ?? "GET").toUpperCase(), path: url.pathname, body: typeof init?.body === "string" ? init.body : undefined });
    const body = url.pathname.endsWith("/players/action") ? { sent: true, command: 'kick "Rex"' } : players;
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Players tab", () => {
  it("lists who is online and kicks a player", async () => {
    const calls = mock({
      supported: true,
      running: true,
      actions: ["kick"],
      list: {
        protocol: "a2s",
        map: "rp_jedi_galaxy",
        online: 2,
        max: 16,
        players: [
          { name: "Wolffe", score: 3, seconds: 4000 },
          { name: "Rex", seconds: 90 },
        ],
      },
    });
    render(<GamePlayersPane id="g1" />);
    expect(await screen.findByText("2 of 16 online")).toBeTruthy();
    expect(screen.getByText(/rp_jedi_galaxy/)).toBeTruthy();
    expect(screen.getByText("1h 6m")).toBeTruthy();
    const names = screen
      .getAllByRole("row")
      .slice(1)
      .map((r) => r.textContent ?? "");
    expect(names[0]).toContain("Rex");
    fireEvent.click(screen.getAllByRole("button", { name: "Kick" })[0]);
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/game-servers/g1/players/action" && c.body === '{"action":"kick","name":"Rex"}')).toBe(true));
    expect(await screen.findByText('Sent: kick "Rex"')).toBeTruthy();
  });

  it("says when the game cannot be asked or is stopped", async () => {
    mock({ supported: false, running: false, actions: [] });
    render(<GamePlayersPane id="g1" />);
    expect(await screen.findByText(/no player query/)).toBeTruthy();
    cleanup();
    mock({ supported: true, running: false, actions: [] });
    render(<GamePlayersPane id="g1" />);
    expect(await screen.findByText("The server is not running.")).toBeTruthy();
  });
});
