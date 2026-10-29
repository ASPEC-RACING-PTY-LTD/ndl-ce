import type { BackupArtifact } from "../generated/openapi";

// Size helpers for the Backups workload table: what a workload holds before
// a backup, what one backup added to storage, and the total kept.

const UNIT_KEY = "ndl.backups.sizes.unit";

/** FREE_TIER_BYTES is Cloudflare R2's free storage. */
export const FREE_TIER_BYTES = 10e9;

export type Unit = "MB" | "GB" | "TB";

export const UNITS: Unit[] = ["MB", "GB", "TB"];
const UNIT_BYTES: Record<Unit, number> = { MB: 1e6, GB: 1e9, TB: 1e12 };

/** gb shows a source size in decimal gigabytes. */
export function gb(bytes: number): string {
  return `${(bytes / 1e9).toFixed(bytes >= 10e9 ? 0 : 1)} GB`;
}

/** small shows a stored size in the chosen unit (decimal MB, GB or TB). */
export function small(bytes: number, unit: Unit): string {
  const n = bytes / UNIT_BYTES[unit];
  return `${n.toFixed(n >= 100 || n === 0 ? 0 : n >= 10 ? 1 : 2)} ${unit}`;
}

export function readUnit(): Unit {
  try {
    const saved = window.localStorage.getItem(UNIT_KEY);
    return saved === "GB" || saved === "TB" ? saved : "MB";
  } catch {
    return "MB";
  }
}

export function writeUnit(unit: Unit) {
  try {
    window.localStorage.setItem(UNIT_KEY, unit);
  } catch {
    // A convenience only; the table works without it.
  }
}

/** before is a restore point's size on the workload before compression. */
export function before(a: BackupArtifact): number {
  return a.logical_bytes || a.size_bytes || 0;
}

/**
 * landed is roughly what a restore point added to storage: the new
 * compressed packs it uploaded. Older restore points that only report what
 * they stored locally fall back to that.
 */
export function landed(a: BackupArtifact): number {
  if (a.remote_state === "protected" && a.upload_total_bytes) {
    return a.upload_total_bytes;
  }
  return a.physical_new_data ?? 0;
}

export type WorkloadSize = {
  /** latest is the newest restore point of the workload. */
  latest?: BackupArtifact;
  /** before is the newest restore point's size before compression. */
  before: number;
  /** latestStored is what the newest restore point added to storage. */
  latestStored: number;
  /** total is what every restore point of the workload added to storage. */
  total: number;
  count: number;
};

/** sizesByWorkload sums restore points per workload, newest first. */
export function sizesByWorkload(artifacts: BackupArtifact[]): Map<string, WorkloadSize> {
  const sorted = [...artifacts].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  const out = new Map<string, WorkloadSize>();
  for (const a of sorted) {
    let row = out.get(a.workload_id);
    if (!row) {
      row = { latest: a, before: before(a), latestStored: landed(a), total: 0, count: 0 };
      out.set(a.workload_id, row);
    } else if (row.before === 0 && before(a) > 0) {
      // The newest point may still be capturing; size it by the newest one that reported.
      row.before = before(a);
    }
    row.total += landed(a);
    row.count += 1;
  }
  return out;
}
