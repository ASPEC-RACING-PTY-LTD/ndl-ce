import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HelixView } from "../helix/api";

const session = vi.hoisted(() => ({ roles: ["superadmin"] as string[] }));
vi.mock("../session", () => ({
  useSession: () => ({ status: "ready", user: { user_id: "u1", username: "u", roles: session.roles } }),
}));

const view = (): HelixView => ({
  enabled: true,
  kill: { engaged: false },
  credentials: [],
  allowed_ips: [],
  global_events: true,
  thresholds: { cpu_percent: 90, memory_percent: 90 },
  machines: [
    { id: "m1", name: "app", type: "lxc", status: "running", exec_capable: true },
    { id: "m2", name: "win", type: "vm", status: "running", exec_capable: false },
  ],
  approvals: [
    {
      id: "a1",
      op: "exec",
      machine_id: "m1",
      machine_name: "app",
      summary: "run as root in /tmp: docker system prune -af",
      reasons: ["the command looks destructive: deleting Docker containers, images or volumes"],
      status: "pending",
      requested_at: "2026-11-22T00:00:00Z",
      requested_by: "helix",
      expires_at: "2026-11-23T00:00:00Z",
    },
  ],
  pending_approvals: 1,
  running: [],
  listening: 0,
  latest_event: 0,
  api_base: "/api/v1/helix/v1",
});

const api = vi.hoisted(() => ({
  getHelix: vi.fn(),
  getHelixActivity: vi.fn(),
  getHelixKill: vi.fn(),
  putHelixMachines: vi.fn(),
  putHelixSettings: vi.fn(),
  createHelixCredential: vi.fn(),
  revokeHelixCredential: vi.fn(),
  decideHelixApproval: vi.fn(),
  engageHelixKill: vi.fn(),
  releaseHelixKill: vi.fn(),
}));
vi.mock("../helix/api", async (orig) => ({ ...(await orig<typeof import("../helix/api")>()), ...api }));
vi.mock("../confirm", () => ({ confirmAction: () => true }));

import { HelixPage } from "./HelixPage";

beforeEach(() => {
  session.roles = ["superadmin"];
  api.getHelix.mockImplementation(() => Promise.resolve(view()));
  api.getHelixActivity.mockResolvedValue({ items: [] });
  api.getHelixKill.mockResolvedValue({ enabled: true, kill: { engaged: false }, running: 2 });
  api.putHelixMachines.mockImplementation(() => Promise.resolve(view()));
  api.createHelixCredential.mockResolvedValue({ token: "hlx_secret_once", credential: {}, view: view() });
  api.decideHelixApproval.mockImplementation(() => Promise.resolve(view()));
  api.engageHelixKill.mockResolvedValue({ kill: { engaged: true }, stopped_operations: 2, cancelled_deployments: 1, revoked_credentials: 0 });
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("HelixPage", () => {
  it("grants machine access, with read implied and VM-only capabilities", async () => {
    render(<HelixPage />);
    const exec = await screen.findByLabelText("Execute on app");
    expect((screen.getByLabelText("Execute on win") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText("Power on win") as HTMLInputElement).disabled).toBe(false);
    fireEvent.click(exec);
    expect((screen.getByLabelText("Read on app") as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByLabelText("Protected: app"));
    fireEvent.click(screen.getByText("Save machine access"));
    await waitFor(() => expect(api.putHelixMachines).toHaveBeenCalled());
    const grants = api.putHelixMachines.mock.calls[0][0];
    expect(grants).toEqual([expect.objectContaining({ machine_id: "m1", caps: ["read", "execute"], protected: true, destructive: false })]);
  });

  it("shows a new credential once and decides approvals", async () => {
    render(<HelixPage />);
    fireEvent.click(await screen.findByText("Create credential"));
    expect(await screen.findByText("hlx_secret_once")).toBeTruthy();
    expect(api.createHelixCredential).toHaveBeenCalledWith("helix", 90);
    fireEvent.click(screen.getByText("Approve"));
    await waitFor(() => expect(api.decideHelixApproval).toHaveBeenCalledWith("a1", "approve", ""));
  });

  it("gives an Admin the kill switch only", async () => {
    session.roles = ["admin"];
    render(<HelixPage />);
    fireEvent.click(await screen.findByText("Stop Helix now"));
    await waitFor(() => expect(api.engageHelixKill).toHaveBeenCalledWith("", false));
    expect(await screen.findByText(/Helix is stopped: 2 operation/)).toBeTruthy();
    expect(api.getHelix).not.toHaveBeenCalled();
    expect(screen.queryByText("Release")).toBeNull();
  });
});
