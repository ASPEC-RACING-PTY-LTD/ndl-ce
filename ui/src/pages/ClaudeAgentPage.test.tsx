import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAutoConfirm } from "../confirm";
import { ClaudeAgentPage } from "./ClaudeAgentPage";

type Call = { method: string; path: string; body?: string };

function mock(state: Record<string, unknown>) {
  const calls: Call[] = [];
  let overview: Record<string, unknown> = {
    enabled: true,
    workload_id: "wl1",
    state: "ready",
    name: "No-dal",
    permission_mode: "default",
    permission_modes: ["default", "acceptEdits", "plan"],
    grants: [],
    servers: [
      {
        id: "gs1",
        name: "JvS",
        game: "gmod",
        family: "source",
        slug: "jvs",
        default_folders: [
          { path: "garrysmod/addons", write: true },
          { path: "garrysmod/gamemodes", write: true },
        ],
      },
    ],
    status: { installed: true, logged_in: false, version: "2.1.312", service: "inactive" },
    ...state,
  };
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? init.body : undefined;
    calls.push({ method, path: url.pathname + url.search, body });
    let out: unknown = overview;
    if (url.pathname === "/api/v1/claude-agent/login") {
      const action = JSON.parse(body ?? "{}").action;
      out =
        action === "start"
          ? { installed: true, logged_in: false, login_url: "https://claude.com/cai/oauth/authorize?x=1" }
          : { installed: true, logged_in: true, account: "op@example.com" };
      if (action === "code") {
        overview = { ...overview, status: { installed: true, logged_in: true, account: "op@example.com", plan: "max", service: "active" } };
      }
    } else if (url.pathname === "/api/v1/claude-agent/grants") {
      const grants = JSON.parse(body ?? "{}").grants;
      overview = { ...overview, grants };
      out = { grants, applied: true };
    } else if (url.pathname.startsWith("/api/v1/game-servers/gs1/files")) {
      const path = url.searchParams.get("path");
      out = {
        path,
        items:
          path === ""
            ? [
                { name: "srcds", dir: true },
                { name: "garrysmod", dir: true },
                { name: "srcds_run", dir: false },
              ]
            : [
                { name: "lua", dir: true },
                { name: "data", dir: true },
              ],
      };
    }
    return new Response(JSON.stringify(out), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Claude Agent", () => {
  it("offers setup before the container exists", async () => {
    const calls = mock({ workload_id: "", state: "", status: undefined });
    render(<ClaudeAgentPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Set up agent" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/claude-agent/setup" && c.body === '{"name":"No-dal","permission_mode":"default"}')).toBe(true),
    );
  });

  it("signs in with the code from the sign-in page", async () => {
    const calls = mock({});
    render(<ClaudeAgentPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Get sign-in link" }));
    const link = await screen.findByRole("link", { name: "the sign-in page" });
    expect(link.getAttribute("href")).toBe("https://claude.com/cai/oauth/authorize?x=1");
    fireEvent.change(screen.getByLabelText("Code from the sign-in page"), { target: { value: "AbCdEf123456#xyz" } });
    fireEvent.click(screen.getByRole("button", { name: "Finish sign-in" }));
    expect(await screen.findByText("op@example.com", { selector: "strong" })).toBeTruthy();
    expect(calls.some((c) => c.body === '{"action":"code","code":"AbCdEf123456#xyz"}')).toBe(true);
  });

  it("shares a server with its default folders, adds one from the picker and saves", async () => {
    const calls = mock({});
    render(<ClaudeAgentPage />);
    const server = await screen.findByRole("group", { name: "JvS" });
    fireEvent.click(within(server).getByRole("checkbox", { name: /JvS/ }));
    expect(within(server).getByText("garrysmod/addons")).toBeTruthy();
    expect(within(server).getByText("garrysmod/gamemodes")).toBeTruthy();
    fireEvent.change(within(server).getByLabelText("Access to garrysmod/gamemodes"), { target: { value: "ro" } });
    fireEvent.click(within(server).getByRole("checkbox", { name: "Read the server log" }));

    // The picker lists folders A to Z and shares the opened one.
    fireEvent.click(within(server).getByRole("button", { name: "Add folder" }));
    const picker = await screen.findByRole("group", { name: "Choose a folder" });
    await within(picker).findByRole("button", { name: "garrysmod/" });
    const names = within(picker)
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(names).toEqual(["garrysmod/", "srcds/"]);
    fireEvent.click(within(picker).getByRole("button", { name: "garrysmod/" }));
    fireEvent.click(await within(picker).findByRole("button", { name: "data/" }));
    fireEvent.click(await within(picker).findByRole("button", { name: "Share garrysmod/data" }));

    fireEvent.click(screen.getByRole("button", { name: "Save access" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.path === "/api/v1/claude-agent/grants")).toBe(true));
    const sent = JSON.parse(calls.find((c) => c.method === "PUT")!.body!).grants[0];
    expect(sent.id).toBe("gs1");
    expect(sent.log).toBe(true);
    expect(sent.folders).toEqual([
      { path: "garrysmod/addons", write: true },
      { path: "garrysmod/gamemodes", write: false },
      { path: "garrysmod/data", write: true },
    ]);
  });

  it("removes the agent after confirming", async () => {
    setAutoConfirm(true);
    const calls = mock({});
    render(<ClaudeAgentPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Remove agent" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/claude-agent/remove")).toBe(true));
    setAutoConfirm(false);
  });

  it("points to Add Features while off", async () => {
    mock({ enabled: false });
    render(<ClaudeAgentPage />);
    expect(await screen.findByText("Claude Agent is off")).toBeTruthy();
  });
});
