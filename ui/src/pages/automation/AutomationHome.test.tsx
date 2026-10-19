import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AutomationHome } from "./AutomationHome";

vi.mock("../../session", () => ({
  useSession: () => ({ status: "ready", setupOpen: false, user: { user_id: "u1", username: "admin", roles: ["superadmin"], grants: ["*"] } }),
}));

function mockFetch(routes: Record<string, { status: number; body: unknown }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const path = new URL(url, "http://localhost").pathname;
      const hit = routes[path] ?? { status: 404, body: { error: `unmocked ${path}` } };
      return new Response(JSON.stringify(hit.body), { status: hit.status, headers: { "Content-Type": "application/json" } });
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("Automations home", () => {
  it("lists flows with their trigger, state and last run", async () => {
    window.history.replaceState({}, "", "/automation");
    mockFetch({
      "/api/v1/automations/flows": {
        status: 200,
        body: {
          items: [
            {
              id: "f1",
              name: "Restart unhealthy containers",
              enabled: true,
              published_version: 2,
              trigger_kind: "event",
              draft_changed: true,
              destructive: ["restart"],
              last_run: { id: "r1", status: "failed", created_at: "2026-10-20T09:00:00Z" },
            },
          ],
        },
      },
    });
    render(<AutomationHome />);
    expect(screen.getByRole("heading", { name: /^automation$/i })).toBeVisible();
    expect(await screen.findByRole("link", { name: "Restart unhealthy containers" })).toBeVisible();
    expect(screen.getByText("Unpublished changes")).toBeVisible();
    expect(screen.getByText("v2")).toBeVisible();
    expect(screen.getByRole("tab", { name: "Runs" })).toBeVisible();
    expect(screen.getByRole("button", { name: "New flow" })).toBeVisible();
  });

  it("says how to turn the feature on when it is off", async () => {
    window.history.replaceState({}, "", "/automation");
    mockFetch({ "/api/v1/automations/flows": { status: 409, body: { error: "automation is disabled" } } });
    render(<AutomationHome />);
    expect(await screen.findByRole("link", { name: "Features" })).toBeVisible();
  });

  it("shows the starter templates", async () => {
    window.history.replaceState({}, "", "/automation");
    mockFetch({
      "/api/v1/automations/flows": { status: 200, body: { items: [] } },
      "/api/v1/automations/templates": {
        status: 200,
        body: { items: [{ id: "notify-failed-backup", name: "Notify on failed backup", description: "When a backup fails, send a notification." }] },
      },
    });
    render(<AutomationHome />);
    fireEvent.click(screen.getByRole("tab", { name: "Templates" }));
    expect(await screen.findByText("Notify on failed backup")).toBeVisible();
    expect(screen.getByRole("button", { name: "Use this template" })).toBeVisible();
  });
});
