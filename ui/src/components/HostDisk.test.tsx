import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HostDiskResult, HostDiskStatus } from "../generated/openapi";
import { HostDiskBanner, HostDiskPanel } from "./HostDisk";

const api = vi.hoisted(() => ({
  getHostDisk: vi.fn(),
  getHostDiskUsage: vi.fn(),
  cleanupHostDisk: vi.fn(),
  releaseHostDiskReserve: vi.fn(),
}));
vi.mock("../api/client", () => api);

const GIB = 1024 ** 3;

function status(level: HostDiskStatus["level"], free: number): HostDiskStatus {
  return {
    level,
    filesystems: [
      {
        mount: "/",
        paths: ["/", "/var/lib/ndl", "/var/lib/postgresql"],
        roles: ["root", "data", "postgresql"],
        total_bytes: 868 * GIB,
        free_bytes: free,
        used_percent: (100 * (868 * GIB - free)) / (868 * GIB),
        level,
        critical_below_bytes: 69 * GIB,
      },
    ],
    reserve_bytes: 50 * GIB,
    reserve_target_bytes: 50 * GIB,
    reserve_held: true,
  };
}

const usage: HostDiskResult = {
  status: status("warning", 100 * GIB),
  usage: {
    data_dir: "/var/lib/ndl",
    categories: [
      { id: "storage", label: "Storage pools on this disk (workload disks)", dir: "storage", bytes: 337 * GIB },
      {
        id: "update-checkpoints",
        label: "Update checkpoints",
        dir: "update-checkpoints",
        cleanup: "Keeps the newest checkpoint and removes older ones.",
        bytes: 120 * GIB,
      },
    ],
    other_bytes: GIB,
    outside_bytes: 30 * GIB,
  },
};

describe("HostDisk", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("only warns on every page once a disk is critical", async () => {
    api.getHostDisk.mockResolvedValue({ status: status("warning", 100 * GIB) });
    const { unmount } = render(<HostDiskBanner enabled />);
    await waitFor(() => expect(api.getHostDisk).toHaveBeenCalled());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    unmount();

    api.getHostDisk.mockResolvedValue({ status: status("critical", 40 * GIB) });
    render(<HostDiskBanner enabled />);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/paused to protect postgresql/i);
    expect(within(alert).getByRole("link", { name: /free up space/i })).toHaveAttribute("href", "/storage#host-disk");
  });

  it("does not poll without storage access", () => {
    render(<HostDiskBanner enabled={false} />);
    expect(api.getHostDisk).not.toHaveBeenCalled();
  });

  it("measures usage and only offers cleanup for disposable files", async () => {
    api.getHostDisk.mockResolvedValue({ status: status("warning", 100 * GIB) });
    api.getHostDiskUsage.mockResolvedValue(usage);
    api.cleanupHostDisk.mockResolvedValue({
      status: status("ok", 220 * GIB),
      clean: { category: "update-checkpoints", removed_bytes: 120 * GIB, kept: 1 },
    });
    render(<HostDiskPanel mutate />);

    expect(await screen.findByRole("meter", { name: "/ used" })).toBeVisible();
    expect(screen.getByText(/50.0 GiB of the root disk is reserved for No-dal/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /what is using space/i }));
    const table = await screen.findByRole("table", { name: /disk usage by category/i });
    const pools = within(table).getByText(/storage pools on this disk/i).closest("tr")!;
    expect(within(pools).queryByRole("button")).not.toBeInTheDocument();
    expect(within(pools).getByText(/your data/i)).toBeVisible();

    fireEvent.click(within(table).getByRole("button", { name: /clean up update checkpoints/i }));
    const dialog = await screen.findByRole("dialog", { name: /clean up/i });
    expect(within(dialog).getByText(/never touched/i)).toBeVisible();
    expect(api.cleanupHostDisk).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: /^clean up$/i }));
    await waitFor(() => expect(api.cleanupHostDisk).toHaveBeenCalledWith("update-checkpoints"));
    expect(await screen.findByText(/freed/i)).toBeVisible();
  });

  it("asks before releasing the reserve", async () => {
    api.getHostDisk.mockResolvedValue({ status: status("critical", 40 * GIB) });
    api.releaseHostDiskReserve.mockResolvedValue({ status: { ...status("critical", 44 * GIB), reserve_held: false } });
    render(<HostDiskPanel mutate />);
    fireEvent.click(await screen.findByRole("button", { name: /release reserve now/i }));
    const dialog = await screen.findByRole("dialog", { name: /release emergency reserve/i });
    expect(api.releaseHostDiskReserve).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: /^release$/i }));
    await waitFor(() => expect(api.releaseHostDiskReserve).toHaveBeenCalled());
  });
});
