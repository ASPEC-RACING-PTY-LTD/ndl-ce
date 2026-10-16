import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RecoveryWorkloads as State } from "../generated/openapi";
import { RecoveryWorkloads } from "./RecoveryWorkloads";

const api = vi.hoisted(() => ({
  ApiError: class ApiError extends Error {},
  getRecovery: vi.fn(),
  restoreRecoveredWorkload: vi.fn(),
}));
vi.mock("../api/client", () => api);

const state: State = {
  recovered: true,
  recovery: { hostname: "old-host", connection_verified: true, at: "2026-10-16T00:00:00Z", snapshot_created_at: "2026-10-15T00:00:00Z" },
  workloads: [
    {
      workload_id: "a",
      name: "viewdock",
      backed_up_at: "2026-10-15T00:00:00Z",
      capture_mode: "smart",
      not_backed_up: [{ path: "/root/viewdock", unpushed: true, unpushed_commits: 2, not_backed_up: ["src/x.go"] }],
      restore: { status: "pending" },
    },
    {
      workload_id: "b",
      name: "codehold",
      restore: {
        status: "needs_attention",
        rehydrate: {
          needs_attention: true,
          repos: [{ path: "/srv/private", status: "needs_credentials", error: "no credentials could clone it" }],
          images: [{ ref: "redis:7", digest: "redis@sha256:gone", status: "needs_tag_approval" }],
        },
      },
    },
  ],
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RecoveryWorkloads", () => {
  it("shows what was not backed up and restores everything at once", async () => {
    api.getRecovery.mockResolvedValue(state);
    api.restoreRecoveredWorkload.mockResolvedValue(state);
    render(<RecoveryWorkloads owner />);
    expect(await screen.findByText("These changes were not backed up")).toBeTruthy();
    expect(screen.getByText(/2 commit\(s\) were never pushed/)).toBeTruthy();
    expect(screen.getByText(/uncommitted: src\/x.go/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restore all workloads" }));
    await waitFor(() => expect(api.restoreRecoveredWorkload).toHaveBeenCalledWith("all", { use_tags: [], repo_tokens: {} }));
  });

  it("asks for a token and the tag before continuing", async () => {
    api.getRecovery.mockResolvedValue(state);
    api.restoreRecoveredWorkload.mockResolvedValue(state);
    render(<RecoveryWorkloads owner />);
    await screen.findByText("Needs your input");
    fireEvent.change(screen.getByLabelText(/Token to clone/), { target: { value: "tok" } });
    fireEvent.click(screen.getByLabelText(/Use the tag/));
    fireEvent.click(screen.getByRole("button", { name: "Continue restoring" }));
    await waitFor(() =>
      expect(api.restoreRecoveredWorkload).toHaveBeenCalledWith("b", { use_tags: ["redis:7"], repo_tokens: { "/srv/private": "tok" } }),
    );
  });

  it("is hidden on a host that was not recovered", async () => {
    api.getRecovery.mockResolvedValue({ recovered: false });
    const { container } = render(<RecoveryWorkloads owner />);
    await waitFor(() => expect(api.getRecovery).toHaveBeenCalled());
    expect(container.textContent).toBe("");
  });
});
