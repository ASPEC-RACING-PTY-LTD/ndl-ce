import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodeholdCLIDialog } from "./CodeholdCLIDialog";

const settings = { url: "https://codehold.example.com", auto_install: true, concurrency: 5, timeout_seconds: 600 };

const dryRun = {
  id: "r1",
  dry_run: true,
  update: false,
  include_stopped: false,
  concurrency: 5,
  timeout_seconds: 600,
  url: settings.url,
  started_at: "",
  running: false,
  targets: [
    { workload_id: "a", name: "alpha", status: "running", installed: true, version: "ch 1.4.0", plan: "skip", done: true },
    { workload_id: "b", name: "bravo", status: "running", installed: false, plan: "install", done: true },
  ],
  skipped: [{ workload_id: "c", name: "charlie", status: "stopped", installed: false, outcome: "not_running", reason: "not running", done: true }],
  summary: { not_running: 1 },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("CodeholdCLIDialog", () => {
  it("needs a dry run before installing and shows the plan", async () => {
    const posts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        let body: unknown = {};
        if (url.pathname === "/api/v1/codehold-cli") body = { settings, rollout: null };
        if (url.pathname === "/api/v1/codehold-cli/rollout" && init?.method === "POST") {
          const req = JSON.parse(String(init.body));
          posts.push(req);
          body = { rollout: req.dry_run ? dryRun : { ...dryRun, dry_run: false, running: true, targets: [], skipped: [] } };
        }
        return new Response(JSON.stringify(body), { status: url.pathname.endsWith("rollout") ? 202 : 200, headers: { "Content-Type": "application/json" } });
      }),
    );
    render(<CodeholdCLIDialog open canChange onClose={() => undefined} />);
    expect(await screen.findByText(/ch auth login --server https:\/\/codehold.example.com/)).toBeTruthy();
    const install = screen.getByRole("button", { name: "Install now" }) as HTMLButtonElement;
    expect(install.disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(await screen.findByText(/would install 1, update 0, skip 1 already installed; 1 not running/)).toBeTruthy();
    expect(screen.getByText("ch 1.4.0")).toBeTruthy();
    expect(screen.getByText("Skipped, not running")).toBeTruthy();
    expect(posts[0]).toEqual({ dry_run: true, update: false, include_stopped: false });

    await waitFor(() => expect(install.disabled).toBe(false));
    fireEvent.click(install);
    await waitFor(() => expect(posts[1]).toEqual({ dry_run: false, update: false, include_stopped: false }));
  });

  it("hides the install button for read-only users", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ settings, rollout: null }), { status: 200, headers: { "Content-Type": "application/json" } })),
    );
    render(<CodeholdCLIDialog open canChange={false} onClose={() => undefined} />);
    await screen.findByText(/each user runs/);
    expect(screen.queryByRole("button", { name: "Install now" })).toBeNull();
    expect(screen.getByRole("button", { name: "Dry run" })).toBeTruthy();
  });
});
