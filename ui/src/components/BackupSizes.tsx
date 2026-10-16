import { useMemo, useState } from "react";
import { getBackupTargetUsage } from "../api/client";
import type { BackupArtifact, BackupTarget } from "../generated/openapi";

const OPEN_KEY = "ndl.backups.sizes.open";
const UNIT_KEY = "ndl.backups.sizes.unit";
const FREE_TIER_BYTES = 10e9;

type Unit = "MB" | "MiB";

function readPref(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writePref(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Preferences are a convenience; the box works without them.
  }
}

/** gb shows a source size in decimal gigabytes. */
export function gb(bytes: number): string {
  return `${(bytes / 1e9).toFixed(bytes >= 10e9 ? 0 : 1)} GB`;
}

/** small shows a stored size in MB or MiB, switching to GB past 1 GB. */
export function small(bytes: number, unit: Unit): string {
  if (bytes >= 1e9) {
    return gb(bytes);
  }
  const n = unit === "MiB" ? bytes / 1048576 : bytes / 1e6;
  return `${n.toFixed(n >= 100 ? 0 : 1)} ${unit}`;
}

/** before is a restore point's size on the workload before compression. */
function before(a: BackupArtifact): number {
  return a.logical_bytes || a.size_bytes || 0;
}

/**
 * landed is roughly what a restore point added to cloud storage: the new
 * compressed packs it uploaded. Older restore points that only report what
 * they stored locally fall back to that.
 */
function landed(a: BackupArtifact): number {
  if (a.remote_state === "protected" && a.upload_total_bytes) {
    return a.upload_total_bytes;
  }
  return a.physical_new_data ?? 0;
}

type Row = { id: string; name: string; latestBefore: number; latestLanded: number; count: number; totalLanded: number };

/**
 * BackupSizes shows, in one collapsible line, how big backups are before and
 * after they land in cloud storage, with per-backup, average and total sizes
 * and a per-workload breakdown when opened.
 */
export function BackupSizes({
  artifacts,
  workloadName,
  targets,
}: {
  artifacts: BackupArtifact[];
  workloadName: (id: string) => string;
  targets: BackupTarget[];
}) {
  const [open, setOpen] = useState(() => readPref(OPEN_KEY) === "1");
  const [unit, setUnit] = useState<Unit>(() => (readPref(UNIT_KEY) === "MiB" ? "MiB" : "MB"));
  const [measured, setMeasured] = useState<number | null>(null);
  const [measuring, setMeasuring] = useState(false);
  const [measureError, setMeasureError] = useState<string | null>(null);
  const cloud = targets.find((t) => t.kind === "r2") ?? targets.find((t) => ["s3", "aws", "b2", "minio"].includes(t.kind));

  const stats = useMemo(() => {
    const points = artifacts.filter((a) => before(a) > 0).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
    const rows = new Map<string, Row>();
    for (const a of points) {
      const row = rows.get(a.workload_id);
      if (!row) {
        rows.set(a.workload_id, {
          id: a.workload_id,
          name: workloadName(a.workload_id),
          latestBefore: before(a),
          latestLanded: landed(a),
          count: 1,
          totalLanded: landed(a),
        });
      } else {
        row.count += 1;
        row.totalLanded += landed(a);
      }
    }
    const list = [...rows.values()].sort((a, b) => b.totalLanded - a.totalLanded);
    const totalLanded = list.reduce((n, r) => n + r.totalLanded, 0);
    return {
      list,
      count: points.length,
      latest: points[0],
      fleetBefore: list.reduce((n, r) => n + r.latestBefore, 0),
      totalLanded,
      avgBefore: points.length ? points.reduce((n, a) => n + before(a), 0) / points.length : 0,
      avgLanded: points.length ? totalLanded / points.length : 0,
    };
  }, [artifacts, workloadName]);

  const cloudTotal = measured ?? stats.totalLanded;
  const share = cloudTotal / FREE_TIER_BYTES;

  function toggle(next: boolean) {
    setOpen(next);
    writePref(OPEN_KEY, next ? "1" : "0");
  }

  function pickUnit(next: Unit) {
    setUnit(next);
    writePref(UNIT_KEY, next);
  }

  function measure() {
    if (!cloud) {
      return;
    }
    setMeasuring(true);
    setMeasureError(null);
    getBackupTargetUsage(cloud.id)
      .then((res) => setMeasured(res.usage?.total_bytes ?? 0))
      .catch((err) => setMeasureError(err instanceof Error ? err.message : "Could not measure"))
      .finally(() => setMeasuring(false));
  }

  if (stats.count === 0) {
    return null;
  }
  const saved = stats.fleetBefore > 0 ? Math.max(0, 1 - cloudTotal / stats.fleetBefore) : 0;
  return (
    <details className="bk-collapse" open={open} onToggle={(e) => toggle((e.target as HTMLDetailsElement).open)}>
      <summary aria-label="Backup size">
        <span className="bk-summary-title">Backup size</span>
        <span className="bk-summary-meta">
          ~{gb(stats.fleetBefore)} on your workloads → ~{small(cloudTotal, unit)} in {cloud?.name ?? "cloud storage"}
          {saved > 0 ? ` (${Math.round(saved * 100)}% smaller)` : ""}
        </span>
      </summary>
      <div className="bk-collapse-body">
        <div className="bk-head">
          <p className="bk-hint">
            "Before" is the data on the workload. "In the cloud" is what was uploaded after compression and skipping
            data already stored. Numbers are approximate.
          </p>
          <div className="bk-toggle" role="group" aria-label="Size unit">
            {(["MB", "MiB"] as Unit[]).map((u) => (
              <button key={u} type="button" className={"btn btn-sm" + (unit === u ? " is-selected" : "")} aria-pressed={unit === u} onClick={() => pickUnit(u)}>
                {u}
              </button>
            ))}
          </div>
        </div>
        <table className="bk-table" aria-label="Backup size summary">
          <thead>
            <tr>
              <th />
              <th className="bk-num">Before</th>
              <th className="bk-num">In the cloud</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">Latest backup{stats.latest ? ` (${workloadName(stats.latest.workload_id)})` : ""}</th>
              <td className="bk-num">{stats.latest ? gb(before(stats.latest)) : "None"}</td>
              <td className="bk-num">{stats.latest ? small(landed(stats.latest), unit) : "None"}</td>
            </tr>
            <tr>
              <th scope="row">Average per backup</th>
              <td className="bk-num">{gb(stats.avgBefore)}</td>
              <td className="bk-num">{small(stats.avgLanded, unit)}</td>
            </tr>
            <tr>
              <th scope="row">
                Total ({stats.count} backup{stats.count === 1 ? "" : "s"})
              </th>
              <td className="bk-num">{gb(stats.fleetBefore)}</td>
              <td className="bk-num">
                {small(cloudTotal, unit)}
                {measured != null ? " measured" : " estimated"}
              </td>
            </tr>
          </tbody>
        </table>
        {cloud ? (
          <div className="bk-meter-row">
            <div className={"bk-meter" + (share >= 0.9 ? " is-danger" : share >= 0.75 ? " is-warn" : "")} aria-label="Cloud free tier use">
              <span style={{ width: `${Math.min(100, share * 100)}%` }} />
            </div>
            <span className="bk-hint">
              {gb(cloudTotal)} of the 10 GB free tier
            </span>
            <button className="btn btn-sm" type="button" disabled={measuring} onClick={measure}>
              {measuring ? "Measuring" : "Measure exactly"}
            </button>
            {measureError ? <span className="bk-hint is-danger">{measureError}</span> : null}
          </div>
        ) : null}
        <div className="bk-scroll is-short">
          <table className="bk-table" aria-label="Backup size per workload">
            <thead>
              <tr>
                <th>Workload</th>
                <th className="bk-num">Latest before</th>
                <th className="bk-num">Latest in the cloud</th>
                <th className="bk-num">Backups</th>
                <th className="bk-num">Total in the cloud</th>
              </tr>
            </thead>
            <tbody>
              {stats.list.map((r) => (
                <tr key={r.id}>
                  <td>{r.name}</td>
                  <td className="bk-num">{gb(r.latestBefore)}</td>
                  <td className="bk-num">{small(r.latestLanded, unit)}</td>
                  <td className="bk-num">{r.count}</td>
                  <td className="bk-num">{small(r.totalLanded, unit)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </details>
  );
}
