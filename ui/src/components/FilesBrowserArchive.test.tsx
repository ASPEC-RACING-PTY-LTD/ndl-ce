import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionProvider } from "../session";
import { FilesBrowser } from "./FilesBrowser";

const me = { user_id: "u1", username: "admin", roles: ["superadmin"], edition: "ce", ux_level: "guided", expert_ack: false };

function mock() {
  const calls: { method: string; path: string; body?: string }[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({ method, path: url.pathname, body: typeof init?.body === "string" ? init.body : undefined });
    let body: unknown = { items: [] };
    if (url.pathname === "/api/v1/me") {
      body = me;
    } else if (url.pathname === "/api/v1/game-servers/g1/files") {
      body = {
        path: "garrysmod",
        items: [
          { name: "addons", dir: true, size: 0 },
          { name: "pack.zip", dir: false, size: 1024 },
          { name: "server.cfg", dir: false, size: 20 },
        ],
      };
    } else if (url.pathname.endsWith("/files/extract")) {
      body = { extracted: true, dest: "garrysmod" };
    } else if (url.pathname.endsWith("/files/compress")) {
      body = { path: "garrysmod/addons.zip", bytes: 10, files: 3 };
    }
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

function rowMenu(name: string) {
  const row = screen.getByText(name).closest("tr") as HTMLElement;
  fireEvent.click(within(row).getByRole("button", { name: /more actions/i }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Game server files: extract and zip", () => {
  it("extracts an archive and zips a folder", async () => {
    const calls = mock();
    render(
      <SessionProvider>
        <FilesBrowser kind="game" id="g1" initialPath="/garrysmod" embedded />
      </SessionProvider>,
    );
    await screen.findByText("pack.zip");

    // Only archives offer Extract.
    rowMenu("server.cfg");
    expect(screen.queryByRole("menuitem", { name: "Extract" })).toBeNull();
    fireEvent.keyDown(document, { key: "Escape" });

    rowMenu("pack.zip");
    fireEvent.click(screen.getByRole("menuitem", { name: "Extract" }));
    fireEvent.click(await screen.findByRole("button", { name: "Extract" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/game-servers/g1/files/extract")).toBe(true));
    const ex = JSON.parse(calls.find((c) => c.path.endsWith("/files/extract"))?.body ?? "{}");
    expect(ex).toEqual({ path: "garrysmod/pack.zip", dest: "garrysmod" });
    expect(await screen.findByText(/extracted pack\.zip into/i)).toBeVisible();

    rowMenu("addons");
    fireEvent.click(screen.getByRole("menuitem", { name: "Zip" }));
    fireEvent.click(await screen.findByRole("button", { name: "Zip" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/game-servers/g1/files/compress")).toBe(true));
    const zip = JSON.parse(calls.find((c) => c.path.endsWith("/files/compress"))?.body ?? "{}");
    expect(zip).toEqual({ paths: ["garrysmod/addons"], dest: "garrysmod/addons.zip" });
    expect(await screen.findByText(/created \/garrysmod\/addons\.zip/i)).toBeVisible();
  });
});
