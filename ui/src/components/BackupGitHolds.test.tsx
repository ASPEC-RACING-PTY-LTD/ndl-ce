import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BackupGitHold } from "../generated/openapi";
import { BackupGitHolds } from "./BackupGitHolds";

const api = vi.hoisted(() => ({
  ApiError: class ApiError extends Error {
    body?: unknown;
  },
  listBackupGitHolds: vi.fn(),
  patchBackupWorkspace: vi.fn(),
  resolveBackupGitHold: vi.fn(),
}));
vi.mock("../api/client", () => api);

const hold: BackupGitHold = {
  workload_id: "w1",
  workload_name: "viewdock",
  held_since: "2026-10-16T10:00:00Z",
  repos: [{ path: "/root/viewdock", branch: "main", unpushed: true, unsaved: ["src/main.go"] }],
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("BackupGitHolds", () => {
  it("shows nothing when no backup is held", async () => {
    api.listBackupGitHolds.mockResolvedValue({ items: [] });
    const { container } = render(<BackupGitHolds owner onChanged={() => {}} />);
    await waitFor(() => expect(api.listBackupGitHolds).toHaveBeenCalled());
    expect(container.textContent).toBe("");
  });

  it("lists the unsaved work and lets an Owner have it committed and pushed", async () => {
    api.listBackupGitHolds.mockResolvedValueOnce({ items: [hold] }).mockResolvedValue({ items: [] });
    api.resolveBackupGitHold.mockResolvedValue({ workload_id: "w1", held: false });
    const changed = vi.fn();
    render(<BackupGitHolds owner onChanged={changed} />);
    expect(await screen.findByText(/has commits that are on no remote/)).toBeTruthy();
    expect(screen.getByText(/src\/main.go/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Commit and push for me" }));
    expect(screen.getByText(/never added or\s+read/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Commit and push" }));
    await waitFor(() => expect(api.resolveBackupGitHold).toHaveBeenCalledWith("w1", "save"));
    expect(await screen.findByText("viewdock was backed up.")).toBeTruthy();
    expect(changed).toHaveBeenCalled();
  });

  it("does not offer the Owner actions to others", async () => {
    api.listBackupGitHolds.mockResolvedValue({ items: [hold] });
    render(<BackupGitHolds owner={false} onChanged={() => {}} />);
    expect(await screen.findByText("An Owner can release this backup.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Commit and push for me" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Ignore uncommitted Git" })).toBeNull();
  });
});
