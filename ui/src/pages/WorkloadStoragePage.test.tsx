import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Workload } from "../api/phase5";
import type { StoragePool, WorkloadStorage } from "../generated/openapi";
import { WorkloadStoragePage } from "./WorkloadStoragePage";

const api = vi.hoisted(() => ({
  getWorkload: vi.fn(),
  getWorkloadStorage: vi.fn(),
  setWorkloadMounts: vi.fn(),
  workloadAction: vi.fn(),
}));
const session = vi.hoisted(() => ({ roles: ["admin"] as string[] }));

vi.mock("../api/client", () => api);
vi.mock("../session", () => ({
  useSession: () => ({ status: "ready", user: { user_id: "u1", username: "u", roles: session.roles } }),
}));

const ID = "7459dd09-5c4f-4adc-bcca-78ce379df5ee";
const GIB = 1024 ** 3;

const workload: Workload = { id: ID, name: "ViewDock", kind: "system-container", status: "running", unit_active: true };

const hdd = {
  id: "pool-hdd",
  name: "storage",
  backend_type: "zfs",
  status: "available",
  usable_bytes: 3712 * GIB,
  warning_text: [],
} as unknown as StoragePool;

function storage(over: Partial<WorkloadStorage> = {}): WorkloadStorage {
  return { mounts: [], pools: [hdd], mapped_root_uid: 100000, restart_required: false, ...over };
}

describe("WorkloadStoragePage", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", `/workloads/${ID}/storage`);
    session.roles = ["admin"];
    api.getWorkload.mockResolvedValue(workload);
    api.getWorkloadStorage.mockResolvedValue(storage());
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("adds a folder on the HDD pool without restarting the container", async () => {
    api.setWorkloadMounts.mockResolvedValue(
      storage({
        mounts: [{ source: "/var/lib/ndl/storage/zfs/pool-hdd/volumes/container-root/v1", target: "/mnt/media", read_only: false, label: "Media", pool_id: "pool-hdd", pool_name: "storage" }],
        restart_required: true,
      }),
    );
    render(<WorkloadStoragePage />);

    expect(await screen.findByText(/no extra storage is mounted/i)).toBeVisible();
    expect(screen.getByLabelText(/size \(gib\)/i)).toHaveValue("3712");
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: "Media" } });
    fireEvent.click(screen.getByRole("button", { name: /add storage/i }));

    await waitFor(() =>
      expect(api.setWorkloadMounts).toHaveBeenCalledWith(ID, [
        { target: "/mnt/media", read_only: false, label: "Media", pool_id: "pool-hdd", size_bytes: 3712 * GIB },
      ]),
    );
    expect(await screen.findByText("/mnt/media")).toBeVisible();
    expect(screen.getByText(/loads the next time viewdock restarts/i)).toBeVisible();
    expect(api.workloadAction).not.toHaveBeenCalled();
  });

  it("mounts an existing host folder without changing it", async () => {
    api.setWorkloadMounts.mockResolvedValue(storage());
    render(<WorkloadStoragePage />);
    fireEvent.click(await screen.findByLabelText(/existing folder on the host/i));
    expect(screen.getByText(/left exactly as they are/i)).toBeVisible();
    fireEvent.change(screen.getByLabelText(/host folder/i), { target: { value: "/mnt/hdd2/movies" } });
    fireEvent.change(screen.getByLabelText(/path inside the container/i), { target: { value: "/media/movies" } });
    fireEvent.click(screen.getByLabelText(/read-only/i));
    fireEvent.click(screen.getByRole("button", { name: /add storage/i }));
    await waitFor(() =>
      expect(api.setWorkloadMounts).toHaveBeenCalledWith(ID, [
        { target: "/media/movies", read_only: true, label: "", source: "/mnt/hdd2/movies" },
      ]),
    );
  });

  it("asks before unmounting and says the files stay", async () => {
    const mount = { source: "/mnt/hdd2/movies", target: "/media/movies", read_only: true, label: "Movies" };
    const other = { source: "/srv/music", target: "/media/music", read_only: false, label: "Music" };
    api.getWorkloadStorage.mockResolvedValue(storage({ mounts: [mount, other] }));
    api.setWorkloadMounts.mockResolvedValue(storage({ mounts: [other] }));
    render(<WorkloadStoragePage />);

    fireEvent.click(await screen.findByRole("button", { name: "Unmount /media/movies" }));
    const dialog = await screen.findByRole("dialog", { name: /unmount storage/i });
    expect(within(dialog).getByText(/nothing is deleted/i)).toBeVisible();
    expect(api.setWorkloadMounts).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: /^unmount$/i }));
    await waitFor(() =>
      expect(api.setWorkloadMounts).toHaveBeenCalledWith(ID, [
        { source: "/srv/music", target: "/media/music", read_only: false, label: "Music", pool_id: undefined },
      ]),
    );
    expect(await screen.findByText(/still on the host/i)).toBeVisible();
  });

  it("is read-only for viewers", async () => {
    session.roles = ["viewer"];
    render(<WorkloadStoragePage />);
    expect(await screen.findByText(/no extra storage is mounted/i)).toBeVisible();
    expect(screen.queryByRole("button", { name: /add storage/i })).not.toBeInTheDocument();
  });
});
