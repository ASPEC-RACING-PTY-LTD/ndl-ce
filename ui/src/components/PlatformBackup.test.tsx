import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PlatformBackupStatus } from "../generated/openapi";
import { PlatformBackupPanel, recoverySummary } from "./PlatformBackup";

const api = vi.hoisted(() => ({
  ApiError: class ApiError extends Error {},
  getPlatformBackup: vi.fn(),
  rotatePlatformBackupKey: vi.fn(),
  runPlatformBackup: vi.fn(),
  updatePlatformBackup: vi.fn(),
  verifyPlatformBackup: vi.fn(),
}));
vi.mock("../api/client", () => api);

const status: PlatformBackupStatus = {
  enabled: true,
  target: { id: "t", name: "r2", kind: "r2", bucket: "ndl" },
  owners_ready: ["admin"],
  owners_waiting: ["sam"],
  snapshot: { last: { created_at: "2026-10-16T01:00:00Z", size: 2048 }, published: [{}] },
  health: {
    checked_at: "2026-10-16T01:00:00Z",
    total: 3,
    recoverable: 2,
    stale: 0,
    missing: 1,
    items: [
      { workload_id: "a", name: "viewdock", status: "recoverable", latest_at: "2026-10-16T00:00:00Z", deep: true },
      { workload_id: "b", name: "codehold", status: "missing", error: "missing manifest" },
    ],
  },
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("PlatformBackupPanel", () => {
  it("says how many workloads are recoverable and who still has to sign in", async () => {
    api.getPlatformBackup.mockResolvedValue(status);
    render(<PlatformBackupPanel owner targets={[]} />);
    expect(await screen.findByText("2 of 3")).toBeTruthy();
    expect(screen.getByText(/Waiting for sam to sign in/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Show workloads" }));
    expect(screen.getByText("missing manifest")).toBeTruthy();
    expect(recoverySummary(status)).toBe("2 of 3 workloads recoverable");
  });

  it("rotates the key only with the Owner's password", async () => {
    api.getPlatformBackup.mockResolvedValue(status);
    api.rotatePlatformBackupKey.mockResolvedValue(status);
    render(<PlatformBackupPanel owner targets={[]} />);
    fireEvent.click(await screen.findByRole("button", { name: "Rotate key" }));
    expect(screen.getByText(/already downloaded stay readable/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Your password"), { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: "Rotate" }));
    await waitFor(() => expect(api.rotatePlatformBackupKey).toHaveBeenCalledWith("pw"));
  });

  it("offers no actions to others", async () => {
    api.getPlatformBackup.mockResolvedValue(status);
    render(<PlatformBackupPanel owner={false} targets={[]} />);
    await screen.findByText("2 of 3");
    expect(screen.queryByRole("button", { name: "Back up platform now" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Rotate key" })).toBeNull();
  });
});
