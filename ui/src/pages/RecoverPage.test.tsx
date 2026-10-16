import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RecoverPage } from "./RecoverPage";

const api = vi.hoisted(() => ({
  ApiError: class ApiError extends Error {},
  getHealth: vi.fn(),
  getRecoveryStatus: vi.fn(),
  recoverConnect: vi.fn(),
  recoverRestore: vi.fn(),
  recoverUnlock: vi.fn(),
}));
vi.mock("../api/client", () => api);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function type(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("RecoverPage", () => {
  it("connects with an API token, unlocks with an Owner password, maps the host and restores", async () => {
    api.getRecoveryStatus.mockResolvedValue({ available: true });
    api.recoverConnect.mockResolvedValue({ session_id: "s1", snapshots: 3, newest: "2026-10-16T01:00:00Z", schema_level: 66, local_schema_level: 66 });
    api.recoverUnlock.mockResolvedValue({
      hostname: "old-host",
      created_at: "2026-10-16T01:00:00Z",
      workloads: [{ id: "w", name: "viewdock" }],
      pools: [{ id: "p1", name: "local", backend: "directory", path: "/var/lib/ndl/storage/local" }],
      networks: [{ id: "n1", name: "lan", bridge: "ndlbr0" }],
    });
    api.recoverRestore.mockResolvedValue({ restored: true, connection_verified: true });
    api.getHealth.mockResolvedValue({});
    render(<RecoverPage />);
    await waitFor(() => expect(api.getRecoveryStatus).toHaveBeenCalled());
    type("Setup token", "tok");
    type("Cloudflare account id", "0123456789abcdef0123456789abcdef");
    type("Bucket", "ndl");
    type("API token", "cf-token");
    fireEvent.click(screen.getByRole("button", { name: "Find the backup" }));
    await screen.findByText(/Found 3 platform backups/);
    expect(api.recoverConnect).toHaveBeenCalledWith(expect.objectContaining({ api_token: "cf-token", bucket: "ndl" }));
    expect(api.recoverConnect.mock.calls[0][0]).not.toHaveProperty("secret_access_key");

    type("Owner username", "admin");
    type("Password", "pw");
    fireEvent.click(screen.getByRole("button", { name: "Unlock" }));
    await screen.findByText(/Backup of old-host/);
    type("local (directory)", "/srv/ndl");
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(api.recoverRestore).toHaveBeenCalledWith({ token: "tok", session_id: "s1", pools: { p1: "/srv/ndl" }, networks: { n1: "ndlbr0" } }),
    );
    expect(await screen.findByText(/saved in place of the old credentials and tested/, {}, { timeout: 5000 })).toBeTruthy();
  });

  it("says when recovery is not possible any more", async () => {
    api.getRecoveryStatus.mockResolvedValue({ available: false, locked: true });
    render(<RecoverPage />);
    expect(await screen.findByText("A recovery is already running on this host.")).toBeTruthy();
  });
});
