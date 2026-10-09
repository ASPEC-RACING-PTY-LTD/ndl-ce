import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BackupStorageReport, BackupTarget } from "../generated/openapi";
import { BackupStoragePanel } from "./BackupStorage";

const api = vi.hoisted(() => ({
  exportBackupKey: vi.fn(),
  getBackupStorage: vi.fn(),
  getBackupTargetUsage: vi.fn(),
  listBackupLocations: vi.fn(),
  relocateBackupRepository: vi.fn(),
  runBackupMaintenance: vi.fn(),
  runBackupVerify: vi.fn(),
  wipeBackupTarget: vi.fn(),
}));
vi.mock("../api/client", () => api);

const GIB = 1024 ** 3;

const report: BackupStorageReport = {
  workspace: {
    root: "/var/lib/ndl/backup-repo",
    repo_bytes: 182 * GIB,
    max_local_bytes: 250 * GIB,
    host_total_bytes: 868 * GIB,
    host_free_bytes: 104 * GIB,
    effective_reserve_bytes: 69 * GIB,
    usage: { total_bytes: 182 * GIB, pack_bytes: 179 * GIB, snapshot_bytes: 2 * GIB, restore_points: 66 },
    last_gc: {
      reason: "policy Backup (22 expired)",
      finished_at: "2026-10-09T20:00:00Z",
      local: { bytes_freed: 40 * GIB, packs_repacked: 12, dead_bytes: 3 * GIB },
      remote: { r2: { bytes_freed: 2 * GIB, packs_deleted: 9 } },
    },
    pending_remote_sweep: 4,
  },
  pools: [
    { id: "p1", name: "local", backend_type: "directory", root_filesystem: true, allocated_bytes: 450 * GIB, usable_bytes: 280 * GIB },
    { id: "p2", name: "storage", backend_type: "zfs", root_filesystem: false, allocated_bytes: 19 * GIB, usable_bytes: 3700 * GIB },
  ],
  warnings: ["New workloads are placed on the local pool, which shares the host root disk."],
  events: [
    { at: "2026-10-09T19:40:00Z", kind: "retention", ok: false, message: "backup a1 was not removed and will be retried: agent unreachable" },
    { at: "2026-10-09T19:41:00Z", kind: "retention", ok: true, message: "removed 2 expired backup(s)" },
  ],
  retention: [],
  unmanaged_restore_points: [{ backup_id: "lost", namespace: "ns" }],
  deleted_workload_backups: [{ workload_id: "gone", artifacts: 4, logical_bytes: 2 * GIB }],
};

const r2: BackupTarget = { id: "r2", name: "R2", kind: "r2", locator: "s3://ndl-ce/", status: "available" };

describe("BackupStoragePanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("explains repository usage, warnings, cleanup results and failed cleanups", async () => {
    api.getBackupStorage.mockResolvedValue(report);
    render(<BackupStoragePanel mutate={false} targets={[r2]} />);
    expect(await screen.findByText(/shares the host root disk\./i, { selector: "li" })).toBeVisible();
    expect(screen.getByText(/182\.0 GiB used of a 250\.0 GiB limit/)).toBeVisible();
    expect(screen.getByText(/compacted 12 pack/)).toBeVisible();
    expect(screen.getByText(/4 remote pack\(s\) of expired backups wait/)).toBeVisible();
    expect(screen.getByText(/agent unreachable/)).toBeVisible();
    expect(screen.queryByText(/removed 2 expired/)).toBeNull();
    expect(screen.getByText(/1 local restore point\(s\) have no backup record/)).toBeVisible();
    expect(screen.queryByRole("button", { name: /clean up now/i })).toBeNull();
  });

  it("runs cleanup and measures a remote target on request", async () => {
    api.getBackupStorage.mockResolvedValue(report);
    api.runBackupMaintenance.mockResolvedValue({ gc: { local: { bytes_freed: GIB }, remote: { r2: { bytes_freed: GIB } } } });
    api.getBackupTargetUsage.mockResolvedValue({
      target_id: "r2",
      usage: { total_bytes: 60 * GIB, objects: 900, restore_points: 22, pack_bytes: 8 * GIB, other_bytes: 52 * GIB, foreign_points: 3, unreferenced_pack_bytes: GIB },
    });
    render(<BackupStoragePanel mutate targets={[r2]} />);
    fireEvent.click(await screen.findByRole("button", { name: /clean up now/i }));
    expect(await screen.findByText(/cleanup reclaimed 2\.0 GiB/i)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^measure$/i }));
    expect(await screen.findByText(/older whole-disk and archive backups 52\.0 GiB/)).toBeVisible();
    expect(screen.getByText(/3 restore point\(s\) use another repository key/)).toBeVisible();
    await waitFor(() => expect(api.getBackupStorage).toHaveBeenCalledTimes(2));
  });

  it("moves the repository to the recommended pool, never silently to the root disk", async () => {
    api.getBackupStorage.mockResolvedValue(report);
    api.listBackupLocations.mockResolvedValue({
      items: [
        { pool_id: "p1", name: "local", backend_type: "directory", usable: true, root_filesystem: true, recommended: false, repo_path: "/var/lib/ndl/storage/local/backup-repo" },
        { pool_id: "p2", name: "storage", backend_type: "zfs", usable: true, root_filesystem: false, recommended: true, repo_path: "/tank/backup-repo", usable_bytes: 3700 * GIB },
      ],
    });
    api.relocateBackupRepository.mockResolvedValue({ root: "/tank/backup-repo" });
    render(<BackupStoragePanel mutate targets={[]} />);
    fireEvent.click(await screen.findByRole("button", { name: /move repository/i }));
    const zfs = await screen.findByRole("radio", { name: /storage \(zfs\)/i });
    await waitFor(() => expect(zfs).toBeChecked());
    fireEvent.click(screen.getByRole("radio", { name: /local \(directory\)/i }));
    expect(screen.getByRole("checkbox", { name: /allow the host root disk/i })).not.toBeChecked();
    fireEvent.click(zfs);
    fireEvent.click(screen.getByRole("button", { name: /^move$/i }));
    expect(await screen.findByText(/repository is now at \/tank\/backup-repo/i)).toBeVisible();
    expect(api.relocateBackupRepository).toHaveBeenCalledWith({ pool_id: "p2", path: undefined, allow_root_filesystem: undefined });
  });

  it("asks for the backup key until it is exported, and verifies on request", async () => {
    api.getBackupStorage.mockResolvedValue({ ...report, workspace: { ...report.workspace, key_exported: false } });
    api.exportBackupKey.mockResolvedValue({ key: "ab".repeat(32), format: "hex" });
    api.runBackupVerify.mockResolvedValue({ verified: 7 });
    const createObjectURL = vi.fn(() => "blob:key");
    const revokeObjectURL = vi.fn();
    Object.assign(URL, { createObjectURL, revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    render(<BackupStoragePanel mutate targets={[]} />);
    expect(await screen.findByText(/save the backup key/i)).toBeVisible();
    fireEvent.click(screen.getAllByRole("button", { name: /download backup key/i })[0]);
    expect(await screen.findByText(/downloaded as ndl-backup-key\.txt/i)).toBeVisible();
    expect(createObjectURL).toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /verify backups/i }));
    expect(await screen.findByText(/verified 7 restore point/i)).toBeVisible();
  });

  it("wipes a cloud target only after its name is typed", async () => {
    api.getBackupStorage.mockResolvedValue(report);
    api.wipeBackupTarget.mockResolvedValue({ objects_deleted: 900, bytes_deleted: 60 * GIB, records_local_only: 22 });
    render(<BackupStoragePanel mutate targets={[r2]} />);
    fireEvent.click(await screen.findByRole("button", { name: /wipe r2/i }));
    const confirm = screen.getByRole("button", { name: /^wipe$/i });
    expect(confirm).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/type r2 to confirm/i), { target: { value: "r2" } });
    expect(confirm).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/type r2 to confirm/i), { target: { value: "R2" } });
    fireEvent.click(confirm);
    expect(await screen.findByText(/R2 wiped: 900 object\(s\), 60\.0 GiB deleted/)).toBeVisible();
    expect(api.wipeBackupTarget).toHaveBeenCalledWith("r2", "R2");
  });
});
