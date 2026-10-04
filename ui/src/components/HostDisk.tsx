import { useEffect, useState } from "react";
import {
  cleanupHostDisk,
  getHostDisk,
  getHostDiskUsage,
  releaseHostDiskReserve,
} from "../api/client";
import type {
  HostDiskCategory,
  HostDiskCleanupRequest,
  HostDiskFilesystem,
  HostDiskStatus,
  HostDiskUsage,
} from "../generated/openapi";
import { formatBytes } from "../format";
import { ConfirmDialog } from "./ConfirmDialog";
import { Link } from "./Link";

type Level = HostDiskStatus["level"];

export function levelLabel(level: Level): string {
  switch (level) {
    case "warning":
      return "Getting full";
    case "critical":
      return "Critical";
    case "emergency":
      return "Emergency";
    default:
      return "Healthy";
  }
}

function levelBanner(level: Level): string {
  return level === "ok" ? "banner banner-ok" : level === "warning" ? "banner banner-warn" : "banner banner-error";
}

function roleLabel(roles: string[]): string {
  const names: Record<string, string> = { root: "Host root", data: "No-dal data", postgresql: "PostgreSQL" };
  return roles.map((r) => names[r] ?? r).join(", ");
}

/** HostDiskBanner warns on every page while a host disk is critically full. */
export function HostDiskBanner({ enabled }: { enabled: boolean }) {
  const [status, setStatus] = useState<HostDiskStatus | null>(null);

  useEffect(() => {
    if (!enabled) {
      return;
    }
    let cancelled = false;
    const load = () =>
      getHostDisk()
        .then((res) => {
          if (!cancelled) {
            setStatus(res.status);
          }
        })
        .catch(() => undefined);
    void load();
    const timer = window.setInterval(() => void load(), 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [enabled]);

  if (!status || (status.level !== "critical" && status.level !== "emergency")) {
    return null;
  }
  const worst = status.filesystems[0];
  return (
    <p className="banner banner-error shell-banner" role="alert">
      Host disk {levelLabel(status.level).toLowerCase()}:{" "}
      {worst ? `${worst.mount} is ${worst.used_percent.toFixed(0)}% full with ${formatBytes(worst.free_bytes)} free. ` : ""}
      Backups, uploads and installs are paused to protect PostgreSQL.{" "}
      <Link href="/storage#host-disk">Free up space</Link>
    </p>
  );
}

function FilesystemRow({ fs }: { fs: HostDiskFilesystem }) {
  const used = Math.min(100, Math.max(0, fs.used_percent));
  return (
    <div className="host-disk-fs">
      <div className="host-disk-fs-head">
        <strong>{fs.mount}</strong>
        <span className="field-hint">{roleLabel(fs.roles)}</span>
        <span className={`host-disk-level host-disk-level-${fs.level}`}>{levelLabel(fs.level)}</span>
      </div>
      <div
        className={`host-disk-bar host-disk-bar-${fs.level}`}
        role="meter"
        aria-label={`${fs.mount} used`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(used)}
      >
        <span style={{ width: `${used}%` }} />
      </div>
      <p className="field-hint">
        {used.toFixed(1)}% used, {formatBytes(fs.free_bytes)} free of {formatBytes(fs.total_bytes)}.
        {fs.critical_below_bytes ? ` Bulk writes pause below ${formatBytes(fs.critical_below_bytes)} free.` : ""}
      </p>
    </div>
  );
}

/** HostDiskPanel shows disk protection and the safe ways to free space. */
export function HostDiskPanel({ mutate }: { mutate: boolean }) {
  const [status, setStatus] = useState<HostDiskStatus | null>(null);
  const [usage, setUsage] = useState<HostDiskUsage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [measuring, setMeasuring] = useState(false);
  const [busy, setBusy] = useState(false);
  const [cleaning, setCleaning] = useState<HostDiskCategory | null>(null);
  const [releasing, setReleasing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getHostDisk()
      .then((res) => {
        if (!cancelled) {
          setStatus(res.status);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Disk protection is unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function measure() {
    setMeasuring(true);
    setError(null);
    getHostDiskUsage()
      .then((res) => {
        setUsage(res.usage ?? null);
        setStatus(res.status);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Could not measure disk usage"))
      .finally(() => setMeasuring(false));
  }

  function onClean() {
    const cat = cleaning;
    setCleaning(null);
    if (!cat) {
      return;
    }
    setBusy(true);
    setError(null);
    cleanupHostDisk(cat.id as HostDiskCleanupRequest["category"])
      .then((res) => {
        setStatus(res.status);
        setNotice(`${cat.label}: freed ${formatBytes(res.clean?.removed_bytes ?? 0)}.`);
        measure();
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Cleanup failed"))
      .finally(() => setBusy(false));
  }

  function onRelease() {
    setReleasing(false);
    setBusy(true);
    releaseHostDiskReserve()
      .then((res) => {
        setStatus(res.status);
        setNotice(`Released the ${formatBytes(res.status.reserve_bytes)} emergency reserve.`);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Could not release the reserve"))
      .finally(() => setBusy(false));
  }

  return (
    <article className="panel" id="host-disk" aria-labelledby="host-disk-heading">
      <h2 id="host-disk-heading">Host disk protection</h2>
      {status ? (
        <p className={levelBanner(status.level)} role="status">
          {status.level === "ok"
            ? "Host disks have room. No-dal pauses backups, uploads and installs before a disk can fill."
            : status.level === "warning"
              ? "A host disk is getting full. Free space before backups and installs are paused."
              : "A host disk is critically full. Backups, uploads, migrations and installs are paused to protect PostgreSQL until space is freed."}
        </p>
      ) : null}
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {status?.filesystems.map((fs) => <FilesystemRow key={fs.mount} fs={fs} />)}
      {status && status.reserve_bytes > 0 ? (
        <p className="field-hint">
          {status.reserve_held
            ? `A ${formatBytes(status.reserve_bytes)} emergency reserve is held. It is released automatically if the disk nearly fills, so PostgreSQL keeps running.`
            : status.reserve_note || "No emergency reserve is held right now."}{" "}
          {mutate && status.reserve_held ? (
            <button className="btn btn-sm" type="button" disabled={busy} onClick={() => setReleasing(true)}>
              Release reserve now
            </button>
          ) : null}
        </p>
      ) : null}

      <div className="btn-row is-flush">
        <button className="btn" type="button" disabled={measuring} onClick={measure}>
          {measuring ? "Measuring" : usage ? "Measure again" : "What is using space"}
        </button>
      </div>

      {usage ? (
        <div className="table-wrap">
          <table aria-label="Disk usage by category">
            <thead>
              <tr>
                <th>What</th>
                <th>Size</th>
                <th>Cleanup</th>
              </tr>
            </thead>
            <tbody>
              {usage.categories
                .filter((c) => c.bytes > 0 || c.cleanup)
                .map((c) => (
                  <tr key={c.id}>
                    <td>
                      {c.label}
                      <div className="field-hint">
                        <code>{`${usage.data_dir}/${c.dir}`}</code>
                      </div>
                    </td>
                    <td>
                      {formatBytes(c.bytes)}
                      {c.partial ? " or more" : ""}
                    </td>
                    <td>
                      {c.cleanup ? (
                        <>
                          <span className="field-hint">{c.cleanup}</span>{" "}
                          {mutate ? (
                            <button
                              className="btn btn-sm"
                              type="button"
                              disabled={busy || c.bytes === 0}
                              aria-label={`Clean up ${c.label}`}
                              onClick={() => setCleaning(c)}
                            >
                              Clean up
                            </button>
                          ) : null}
                        </>
                      ) : (
                        <span className="field-hint">Your data. Manage it on its own page.</span>
                      )}
                    </td>
                  </tr>
                ))}
              <tr>
                <td>Other No-dal files</td>
                <td>{formatBytes(usage.other_bytes)}</td>
                <td />
              </tr>
              <tr>
                <td>
                  Outside No-dal on this disk
                  <div className="field-hint">The OS, logs, home folders and test output. No-dal does not remove these.</div>
                </td>
                <td>{formatBytes(usage.outside_bytes)}</td>
                <td />
              </tr>
            </tbody>
          </table>
        </div>
      ) : null}

      <ConfirmDialog
        open={cleaning != null}
        title="Clean up"
        confirmLabel="Clean up"
        onConfirm={onClean}
        onClose={() => setCleaning(null)}
      >
        <p>
          {cleaning?.label}: {cleaning?.cleanup} Pools, workload disks, backups and game data are never touched.
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={releasing}
        title="Release emergency reserve"
        confirmLabel="Release"
        onConfirm={onRelease}
        onClose={() => setReleasing(false)}
      >
        <p>
          Delete the reserve file to free {formatBytes(status?.reserve_bytes ?? 0)} now? It holds no data. It is
          recreated after six hours once the disk is healthy.
        </p>
      </ConfirmDialog>
    </article>
  );
}
