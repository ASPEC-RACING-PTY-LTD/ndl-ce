import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GPUListResponse } from "../generated/openapi";
import { GpuPage } from "./GpuPage";

const api = vi.hoisted(() => ({
  listGpus: vi.fn(),
  assignGpu: vi.fn(),
  unassignGpu: vi.fn(),
}));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, ...api };
});

const removed = {
  id: "claim-1",
  gpu_id: "0000:01:00.0",
  workload_id: "wl-1",
  mode: "encode",
  exclusive: true,
  status: "assigned",
};

function list(orphaned: GPUListResponse["orphaned_assignments"]): GPUListResponse {
  return { items: [], orphaned_assignments: orphaned, acs_override: "refused", default_devices: [], note: "" };
}

beforeEach(() => {
  window.history.replaceState({}, "", "/workloads/wl-1/gpus");
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("GpuPage", () => {
  it("lists a claim on a removed GPU and unassigns it", async () => {
    api.listGpus.mockResolvedValueOnce(list([removed])).mockResolvedValueOnce(list([]));
    api.unassignGpu.mockResolvedValue({ ok: true, device_nodes: [], restart_required: true, message: "Restart to drop it." });
    render(<GpuPage />);

    expect(await screen.findByRole("heading", { name: /assigned gpus not on this node/i })).toBeVisible();
    expect(screen.getByText(/0000:01:00\.0 encode wl-1/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^unassign$/i }));

    await waitFor(() => expect(api.unassignGpu).toHaveBeenCalledWith("claim-1"));
    expect(await screen.findByText("Restart to drop it.")).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: /assigned gpus not on this node/i })).not.toBeInTheDocument(),
    );
  });
});
