import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionProvider } from "../session";
import { WorkloadsPage } from "./WorkloadsPage";

const me = { user_id: "u1", username: "admin", roles: ["superadmin"], edition: "ce", ux_level: "guided", expert_ack: false };

afterEach(() => vi.unstubAllGlobals());

describe("create buttons", () => {
  it("are only in + Create in the Instance layout", async () => {
    vi.stubGlobal("fetch", async (input: RequestInfo | URL) => {
      const path = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost").pathname;
      const body = path === "/api/v1/me" ? me : path === "/api/v1/workloads" ? { items: [] } : path === "/api/v1/nodes" ? { items: [] } : {};
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    render(
      <SessionProvider>
        <WorkloadsPage fixedKind="vm" />
      </SessionProvider>,
    );
    expect(await screen.findByRole("heading", { name: /^vms$/i })).toBeVisible();
    expect(screen.queryByRole("link", { name: /create vm/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /import vm/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^create$/i })).toBeNull();
  });
});
