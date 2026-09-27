import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Workload } from "../api/phase5";
import type { GPUDiagnosis, GPUNodeDiagnosis, WorkloadGPUDiagnostics } from "../generated/openapi";
import { WorkloadDiagnosticsPage, gpuState } from "./WorkloadDiagnosticsPage";

const api = vi.hoisted(() => ({
  getWorkload: vi.fn(),
  getWorkloadGPUDiagnostics: vi.fn(),
  reapplyWorkloadGPUs: vi.fn(),
  workloadAction: vi.fn(),
  getWorkloadLogs: vi.fn(),
  getDocker: vi.fn(),
  getWorkloadMetrics: vi.fn(),
}));
const session = vi.hoisted(() => ({ roles: ["admin"] as string[] }));

vi.mock("../api/client", () => api);
vi.mock("../session", () => ({
  useSession: () => ({ status: "ready", user: { user_id: "u1", username: "u", roles: session.roles } }),
}));

const ID = "0c8c3ad2-6f0e-4f55-9a1a-7d3f1b2e4c10";

const workload: Workload = {
  id: ID,
  name: "encoder",
  kind: "system-container",
  status: "running",
  unit_active: true,
  pid: 4242,
  memory_bytes: 2 * 1024 ** 3,
};

const expected = [
  "/dev/dri/by-path/pci-0000:01:00.0-render",
  "/dev/nvidia1",
  "/dev/nvidiactl",
  "/dev/nvidia-uvm",
];

function node(path: string, guest: GPUNodeDiagnosis["guest"], extra: Partial<GPUNodeDiagnosis> = {}): GPUNodeDiagnosis {
  return { path, host_rule: "c 195:1 rwm", mounted: true, allowed: true, guest, ...extra };
}

function diagnosis(guest: GPUNodeDiagnosis["guest"], restart: boolean): GPUDiagnosis {
  return {
    saved: expected,
    nodes: expected.map((p) => node(p, p === "/dev/nvidia1" ? guest : "present")),
    running: true,
    pid: 4242,
    config_present: true,
    config_current: true,
    restart_required: restart,
    devices_missing: false,
    issues: restart ? ["/dev/nvidia1 is not present in the running container; restart to apply"] : [],
  };
}

function report(d: GPUDiagnosis, current = true): WorkloadGPUDiagnostics {
  return {
    workload_id: ID,
    supported: true,
    assignments: [
      {
        id: "a1",
        gpu_id: "gpu-0000:01:00.0",
        mode: "encode",
        saved_nodes: current ? expected : ["/dev/dri/by-path/pci-0000:01:00.0-render"],
        expected_nodes: expected,
        current,
      },
    ],
    diagnosis: d,
  };
}

beforeEach(() => {
  window.history.replaceState({}, "", `/workloads/${ID}/diagnostics`);
  session.roles = ["admin"];
  api.getWorkload.mockResolvedValue(workload);
  api.getDocker.mockResolvedValue({ machines: [] });
  api.getWorkloadMetrics.mockResolvedValue({ status: "available", series: [] });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("WorkloadDiagnosticsPage", () => {
  it("compares the saved assignment with the devices the container received", async () => {
    api.getWorkloadGPUDiagnostics.mockResolvedValue(report(diagnosis("missing", true), false));
    render(<WorkloadDiagnosticsPage />);

    const saved = await screen.findByRole("table", { name: /saved assignment/i });
    expect(within(saved).getByText("Outdated")).toBeVisible();
    const exposure = screen.getByRole("table", { name: /device exposure/i });
    const row = within(exposure).getByText("/dev/nvidia1").closest("tr")!;
    expect(within(row).getByText("Missing")).toBeVisible();
    expect(within(screen.getByRole("list", { name: /gpu issues/i })).getByText(/restart to apply/i)).toBeVisible();
    expect(screen.getAllByText("Restart required").length).toBeGreaterThan(0);
    expect(api.workloadAction).not.toHaveBeenCalled();
  });

  it("asks before restarting and only reports success once the container has the devices", async () => {
    api.getWorkloadGPUDiagnostics
      .mockResolvedValueOnce(report(diagnosis("missing", true)))
      .mockResolvedValueOnce(report(diagnosis("missing", true)))
      .mockResolvedValueOnce(report(diagnosis("present", false)));
    api.reapplyWorkloadGPUs.mockResolvedValue({
      status: "restart_required",
      message: "Config regenerated. Restart encoder to give the running container its GPU devices.",
    });
    api.workloadAction.mockResolvedValue({});
    render(<WorkloadDiagnosticsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /reapply gpu configuration/i }));
    const dialog = await screen.findByRole("dialog", { name: /restart to apply gpu devices/i });
    expect(api.workloadAction).not.toHaveBeenCalled();
    expect(screen.getByRole("status")).toHaveTextContent(/config regenerated/i);

    fireEvent.click(within(dialog).getByRole("button", { name: /^restart$/i }));
    await waitFor(() => expect(api.workloadAction).toHaveBeenCalledWith(ID, "restart"));
    expect(api.workloadAction).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/container has every gpu device/i)).toBeVisible();
  });

  it("does not claim success when the restart did not deliver the devices", async () => {
    api.getWorkloadGPUDiagnostics.mockResolvedValue(report(diagnosis("missing", true)));
    api.reapplyWorkloadGPUs.mockResolvedValue({ status: "restart_required" });
    api.workloadAction.mockResolvedValue({});
    render(<WorkloadDiagnosticsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /reapply gpu configuration/i }));
    const dialog = await screen.findByRole("dialog", { name: /restart to apply gpu devices/i });
    fireEvent.click(within(dialog).getByRole("button", { name: /^restart$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/still does not have every gpu device/i);
  });

  it("cancelling the restart leaves the workload running", async () => {
    api.getWorkloadGPUDiagnostics.mockResolvedValue(report(diagnosis("missing", true)));
    api.reapplyWorkloadGPUs.mockResolvedValue({ status: "restart_required" });
    render(<WorkloadDiagnosticsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /reapply gpu configuration/i }));
    const dialog = await screen.findByRole("dialog", { name: /restart to apply gpu devices/i });
    fireEvent.click(within(dialog).getByRole("button", { name: /^cancel$/i }));
    expect(screen.getByRole("button", { name: /restart to apply/i })).toBeVisible();
    expect(api.workloadAction).not.toHaveBeenCalled();
  });

  it("reports a removed GPU and points to unassign", async () => {
    const removed = report(diagnosis("present", false));
    removed.assignments![0] = {
      ...removed.assignments![0],
      current: false,
      expected_nodes: [],
      error: "gpu is not present on this node; if it was removed, unassign it",
    };
    removed.diagnosis = {
      ...removed.diagnosis!,
      devices_missing: true,
      nodes: removed.diagnosis!.nodes.map((n) => (n.path === "/dev/nvidia1" ? { ...n, host_rule: undefined } : n)),
    };
    api.getWorkloadGPUDiagnostics.mockResolvedValue(removed);
    render(<WorkloadDiagnosticsPage />);

    expect(await screen.findByText("GPU unavailable")).toBeVisible();
    expect(screen.getByRole("link", { name: /gpu tab/i })).toHaveAttribute("href", `/workloads/${ID}/gpus`);
    const exposure = screen.getByRole("table", { name: /device exposure/i });
    const row = within(exposure).getByText("/dev/nvidia1").closest("tr")!;
    expect(within(row).getByText("Unavailable")).toBeVisible();
  });

  it("hides reapply from read-only roles", async () => {
    session.roles = ["viewer"];
    api.getWorkloadGPUDiagnostics.mockResolvedValue(report(diagnosis("present", false)));
    render(<WorkloadDiagnosticsPage />);

    expect(await screen.findByRole("table", { name: /device exposure/i })).toBeVisible();
    expect(screen.queryByRole("button", { name: /reapply gpu configuration/i })).not.toBeInTheDocument();
  });

  it("shows recent error lines from the unit log", async () => {
    api.getWorkloadGPUDiagnostics.mockResolvedValue(report(diagnosis("present", false)));
    api.getWorkloadLogs.mockResolvedValue({
      status: "available",
      lines: ["started ok", "nvidia-smi: failed to initialize NVML", "ready"],
    });
    render(<WorkloadDiagnosticsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /view recent errors/i }));
    const pre = await screen.findByLabelText(/recent errors/i);
    expect(pre).toHaveTextContent("failed to initialize NVML");
    expect(pre).not.toHaveTextContent("started ok");
  });
});

describe("gpuState", () => {
  it("summarizes the GPU health", () => {
    expect(gpuState(null).label).toBe("Loading");
    expect(gpuState({ workload_id: ID, supported: false }).label).toBe("Not applicable");
    expect(gpuState(report(diagnosis("present", false))).label).toBe("Healthy");
    expect(gpuState(report(diagnosis("present", false), false)).label).toBe("Needs reapply");
    expect(gpuState(report(diagnosis("missing", true))).label).toBe("Restart required");
    expect(gpuState({ ...report(diagnosis("present", false)), applied_matches_saved: false }).label).toBe(
      "Needs reapply",
    );
    const missing = report(diagnosis("present", false));
    missing.diagnosis!.devices_missing = true;
    expect(gpuState(missing).label).toBe("GPU missing");
  });
});
