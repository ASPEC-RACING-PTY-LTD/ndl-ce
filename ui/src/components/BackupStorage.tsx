import { useEffect, useState } from "react";
import {
  exportBackupKey,
  getBackupStorage,
  getBackupTargetUsage,
  listBackupLocations,
  relocateBackupRepository,
  runBackupMaintenance,
  purgeDeletedWorkloadBackups,
  runBackupVerify,
  wipeBackupTarget,
} from "../api/client";
import type {
  BackupLocation,
  BackupRemoteSweep,
  BackupRemoteUsage,
  BackupStorageReport,
  BackupTarget,
} from "../generated/openapi";
import { formatBytes, formatWhen } from "../format";
import { ConfirmDialog } from "./ConfirmDialog";
import { Field } from "./Field";
import { ErrorNotice } from "./ErrorNotice";

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export type LocationChoice = { poolId: string; path: string; allowRoot: boolean };

function locationSpace(l: BackupLocation): string {
  if (l.usable_bytes == null) {
    return "free space unknown";
  }
  return `${formatBytes(l.usable_bytes)} free${l.total_bytes != null ? ` of ${formatBytes(l.total_bytes)}` : ""}`;
}

/**
 * BackupLocationPicker chooses where backups are stored: a storage pool,
 * with pools on their own disk recommended and the host root disk only after
 * an explicit confirmation, or another folder.
 */
export function BackupLocationPicker({
  idPrefix,
  poolId,
  path,
  allowRoot,
  pathKind,
  onChange,
}: {
  idPrefix: string;
  poolId: string;
  path: string;
  allowRoot: boolean;
  pathKind: "target" | "repo";
  onChange: (next: LocationChoice) => void;
}) {
  const [locations, setLocations] = useState<BackupLocation[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [custom, setCustom] = useState(false);

  useEffect(() => {
    let cancelled = false;
    listBackupLocations()
      .then((res) => {
        if (cancelled) {
          return;
        }
        const items = res.items ?? [];
        setLocations(items);
        const recommended = items.find((l) => l.recommended);
        if (!poolId && !path && recommended?.pool_id) {
          onChange({ poolId: recommended.pool_id, path: "", allowRoot: false });
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(errorText(err, "Storage pools are unavailable"));
          setCustom(true);
        }
      });
    return () => {
      cancelled = true;
    };
    // Loaded once per open; the parent owns the selection.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selected = locations?.find((l) => l.pool_id === poolId);
  const needsRootConfirm = custom || Boolean(selected?.root_filesystem);
  const name = `${idPrefix}-location`;

  return (
    <fieldset className="field">
      <legend className="field-label">Where backups are stored</legend>
      {error ? <p className="field-hint">{error}</p> : null}
      {locations == null && !error ? <p className="field-hint">Collecting storage pools</p> : null}
      {(locations ?? []).map((l) => (
        <label key={l.pool_id} className="field-check">
          <input
            type="radio"
            name={name}
            value={l.pool_id}
            disabled={!l.usable}
            checked={!custom && poolId === l.pool_id}
            onChange={() => {
              setCustom(false);
              onChange({ poolId: l.pool_id ?? "", path: "", allowRoot: false });
            }}
          />
          <span>
            {`${l.name} (${l.backend_type}), ${locationSpace(l)}`}
            {l.recommended ? <span className="status-pill">Recommended</span> : null}
            {l.root_filesystem ? <span className="status-pill">Host root disk</span> : null}
            {l.reason ? <span className="field-hint"> {l.reason}</span> : null}
            {l.usable ? (
              <span className="field-hint">
                {" "}
                <code>{pathKind === "repo" ? l.repo_path : l.target_path}</code>
              </span>
            ) : null}
          </span>
        </label>
      ))}
      <label className="field-check">
        <input
          type="radio"
          name={name}
          value="custom"
          checked={custom}
          onChange={() => {
            setCustom(true);
            onChange({ poolId: "", path, allowRoot: false });
          }}
        />
        Another folder
      </label>
      {custom ? (
        <Field
          id={`${idPrefix}-path`}
          label="Folder"
          value={path}
          onChange={(e) => onChange({ poolId: "", path: e.target.value, allowRoot })}
          placeholder="/mnt/backup-disk/ndl"
          hint="An absolute path. Folders inside workload disks, No-dal state or another target are refused."
        />
      ) : null}
      {needsRootConfirm ? (
        <label className="field-check">
          <input
            type="checkbox"
            checked={allowRoot}
            onChange={(e) =>
              onChange({ poolId: custom ? "" : poolId, path: custom ? path : "", allowRoot: e.target.checked })
            }
          />
          Allow the host root disk. Backups there can fill the disk the operating system and PostgreSQL need.
        </label>
      ) : null}
    </fieldset>
  );
}

/**
 * BackupStoragePanel explains where backup storage goes: the local
 * repository, the pools workloads live on, what retention keeps, what the
 * last cleanup reclaimed, and anything that needs an operator.
 */
export function BackupStoragePanel({ mutate, targets }: { mutate: boolean; targets: BackupTarget[] }) {
  const [report, setReport] = useState<BackupStorageReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [remote, setRemote] = useState<Record<string, BackupRemoteUsage | string>>({});
  const [relocating, setRelocating] = useState(false);
  const [relocate, setRelocate] = useState<LocationChoice>({ poolId: "", path: "", allowRoot: false });
  const [keySaved, setKeySaved] = useState(false);
  const [wiping, setWiping] = useState<BackupTarget | null>(null);
  const [wipeName, setWipeName] = useState("");

  function load() {
    getBackupStorage()
      .then((res) => {
        setReport(res);
        setError(null);
      })
      .catch((err) => setError(errorText(err, "Backup storage is unavailable")));
  }

  useEffect(load, []);

  // A repository move runs in the background; follow it until it finishes.
  const moving = report?.repository_move?.running === true;
  useEffect(() => {
    if (!moving) {
      return;
    }
    const timer = window.setInterval(load, 10_000);
    return () => window.clearInterval(timer);
  }, [moving]);

  function onPurgeDeleted() {
    if (!window.confirm("Delete every backup of workloads that no longer exist? Protected backups are kept. This cannot be undone.")) {
      return;
    }
    setBusy(true);
    setNotice(null);
    purgeDeletedWorkloadBackups()
      .then((res) => {
        setNotice(
          `Deleted ${res.deleted ?? 0} backup(s) of deleted workloads.` +
            (res.data_unreachable ? ` ${res.data_unreachable} had no reachable data and only their records were removed.` : "") +
            " Space is reclaimed in the background.",
        );
        load();
      })
      .catch((err) => setError(errorText(err, "Could not delete those backups")))
      .finally(() => setBusy(false));
  }

  function onMaintenance() {
    setBusy(true);
    setNotice(null);
    runBackupMaintenance()
      .then((res) => {
        const gc = res.gc;
        let freed = gc?.local?.bytes_freed ?? 0;
        for (const r of Object.values(gc?.remote ?? {}) as BackupRemoteSweep[]) {
          freed += r.bytes_freed ?? 0;
        }
        setNotice(gc?.error ? `Cleanup stopped: ${gc.error}` : `Cleanup reclaimed ${formatBytes(freed)}.`);
        load();
      })
      .catch((err) => setError(errorText(err, "Cleanup failed")))
      .finally(() => setBusy(false));
  }

  function measureTarget(id: string) {
    setRemote((cur) => ({ ...cur, [id]: "Measuring" }));
    getBackupTargetUsage(id)
      .then((res) => setRemote((cur) => ({ ...cur, [id]: res.usage ?? "No usage reported" })))
      .catch((err) => setRemote((cur) => ({ ...cur, [id]: errorText(err, "Could not measure") })));
  }

  function onExportKey() {
    setBusy(true);
    exportBackupKey()
      .then((res) => {
        const url = URL.createObjectURL(new Blob([`${res.key}\n`], { type: "text/plain" }));
        const a = document.createElement("a");
        a.href = url;
        a.download = "ndl-backup-key.txt";
        a.click();
        URL.revokeObjectURL(url);
        setKeySaved(true);
        load();
      })
      .catch((err) => setError(errorText(err, "Could not export the backup key")))
      .finally(() => setBusy(false));
  }

  function onWipe() {
    const tgt = wiping;
    setWiping(null);
    if (!tgt) {
      return;
    }
    setBusy(true);
    setNotice(null);
    wipeBackupTarget(tgt.id, wipeName)
      .then(() => {
        setNotice(
          `Wiping ${tgt.name} in the background. Backups still on this host are kept and upload fresh with the next runs. The result shows under recent activity.`,
        );
        setRemote((cur) => ({ ...cur, [tgt.id]: "Wiping" }));
        load();
      })
      .catch((err) => setError(errorText(err, "Could not wipe the target")))
      .finally(() => {
        setBusy(false);
        setWipeName("");
      });
  }

  function onVerify() {
    setBusy(true);
    setNotice(null);
    runBackupVerify()
      .then((res) => {
        setNotice(`Verified ${res.verified ?? 0} restore point(s). Results are shown on each backup.`);
        load();
      })
      .catch((err) => setError(errorText(err, "Verification failed")))
      .finally(() => setBusy(false));
  }

  function onRelocate() {
    setRelocating(false);
    setBusy(true);
    relocateBackupRepository({
      pool_id: relocate.poolId || undefined,
      path: relocate.poolId ? undefined : relocate.path.trim(),
      allow_root_filesystem: relocate.allowRoot || undefined,
    })
      .then((res) => {
        const to = (res as { to?: string; root?: string }).to ?? res.root ?? "the new location";
        setNotice(`Moving the backup repository to ${to}. Backups are paused until the copy is checked and in use; this panel shows when it is done.`);
        load();
      })
      .catch((err) => setError(errorText(err, "Could not move the repository")))
      .finally(() => setBusy(false));
  }

  const ws = report?.workspace;
  const usage = ws?.usage;
  const gc = ws?.last_gc;
  const objectTargets = targets.filter((t) => ["r2", "s3", "aws", "b2", "minio"].includes(t.kind));
  const failedEvents = (report?.events ?? []).filter((e) => !e.ok);

  return (
    <article className="panel" id="backup-storage" aria-labelledby="backup-storage-heading">
      <h2 id="backup-storage-heading">Backup storage</h2>
      {error ? (
        <ErrorNotice error={error} />
      ) : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {ws && ws.key_exported === false && !keySaved ? (
        <div className="banner banner-warn" role="status">
          <p>
            Save the backup key. It decrypts every backup here and in cloud storage. If this host is lost without it,
            no backup can be restored.
          </p>
          {mutate ? (
            <button className="btn btn-sm" type="button" disabled={busy} onClick={onExportKey}>
              Download backup key
            </button>
          ) : null}
        </div>
      ) : null}
      {keySaved ? (
        <p className="field-hint">
          The key was downloaded as ndl-backup-key.txt. Store it away from this host, for example in a password manager.
        </p>
      ) : null}
      {report && (report.warnings ?? []).length > 0 ? (
        <ul className="banner banner-warn" aria-label="Storage warnings">
          {(report.warnings ?? []).map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}

      {ws ? (
        <div className="table-wrap">
          <table aria-label="Local backup repository">
            <tbody>
              <tr>
                <th>Repository</th>
                <td>
                  <code>{ws.root}</code>
                  <div className="field-hint">
                    {formatBytes(ws.repo_bytes ?? 0)} used of a {formatBytes(ws.max_local_bytes ?? 0)} limit.
                    {ws.host_total_bytes ? ` The disk holds ${formatBytes(ws.host_total_bytes)}, ${formatBytes(ws.host_free_bytes ?? 0)} free.` : ""}
                    {ws.effective_reserve_bytes ? ` Backups stop when ${formatBytes(ws.effective_reserve_bytes)} is left.` : ""}
                  </div>
                </td>
              </tr>
              {usage ? (
                <tr>
                  <th>Contents</th>
                  <td>
                    {usage.restore_points ?? 0} restore point{usage.restore_points === 1 ? "" : "s"}: data{" "}
                    {formatBytes(usage.pack_bytes ?? 0)}, manifests {formatBytes(usage.snapshot_bytes ?? 0)}, caches{" "}
                    {formatBytes(usage.cache_bytes ?? 0)}, queue and state {formatBytes(usage.state_bytes ?? 0)}
                    {usage.temp_bytes ? `, temporary ${formatBytes(usage.temp_bytes)}` : ""}.
                    {usage.recovered_points ? (
                      <div className="field-hint">{usage.recovered_points} restore point(s) were recovered after a crash.</div>
                    ) : null}
                  </td>
                </tr>
              ) : null}
              <tr>
                <th>Last cleanup</th>
                <td>
                  {gc ? (
                    <>
                      {formatWhen(gc.finished_at)} ({gc.reason}): freed {formatBytes(gc.local?.bytes_freed ?? 0)} locally
                      {gc.local?.packs_repacked ? `, compacted ${gc.local.packs_repacked} pack(s)` : ""}
                      {(Object.values(gc.remote ?? {}) as BackupRemoteSweep[]).map((r) => `, ${formatBytes(r.bytes_freed ?? 0)} remotely`).join("")}.
                      {gc.local?.dead_bytes ? (
                        <div className="field-hint">{formatBytes(gc.local.dead_bytes)} of expired data is still inside packs that hold live data.</div>
                      ) : null}
                      {gc.error ? <div className="field-hint">Error: {gc.error}</div> : null}
                    </>
                  ) : (
                    "Not run yet."
                  )}
                  {ws.gc_pending ? <div className="field-hint">A cleanup is queued after the running backups.</div> : null}
                  {ws.pending_remote_sweep ? (
                    <div className="field-hint">{ws.pending_remote_sweep} remote pack(s) of expired backups wait for cleanup.</div>
                  ) : null}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      ) : report == null && !error ? (
        <p className="field-hint">Collecting</p>
      ) : null}

      {mutate ? (
        <div className="btn-row is-flush">
          <button className="btn" type="button" disabled={busy} onClick={onMaintenance}>
            {busy ? "Working" : "Clean up now"}
          </button>
          <button className="btn" type="button" disabled={busy} onClick={onVerify}>
            Verify backups
          </button>
          <button className="btn" type="button" disabled={busy} onClick={() => setRelocating(true)}>
            Move repository
          </button>
          {ws?.key_exported ? (
            <button className="btn" type="button" disabled={busy} onClick={onExportKey}>
              Download backup key
            </button>
          ) : null}
        </div>
      ) : null}

      {report && (report.pools ?? []).length > 0 ? (
        <div className="table-wrap">
          <table aria-label="Storage pools">
            <thead>
              <tr>
                <th>Pool</th>
                <th>Used</th>
                <th>Free</th>
              </tr>
            </thead>
            <tbody>
              {(report.pools ?? []).map((p) => (
                <tr key={p.id}>
                  <td>
                    {p.name} <span className="field-hint">{p.backend_type}</span>
                    {p.root_filesystem ? <div className="field-hint">Shares the host root disk.</div> : null}
                  </td>
                  <td>{p.allocated_bytes != null ? formatBytes(p.allocated_bytes) : "Unknown"}</td>
                  <td>
                    {p.usable_bytes != null ? formatBytes(p.usable_bytes) : "Unknown"}
                    {p.total_bytes != null ? ` of ${formatBytes(p.total_bytes)}` : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {objectTargets.length > 0 ? (
        <div className="table-wrap">
          <table aria-label="Backup target usage">
            <thead>
              <tr>
                <th>Target</th>
                <th>Holds</th>
              </tr>
            </thead>
            <tbody>
              {objectTargets.map((t) => {
                const u = remote[t.id];
                return (
                  <tr key={t.id}>
                    <td>
                      {`${t.name} (${t.kind.toUpperCase()})`}
                      {mutate ? (
                        <div>
                          <button
                            className="btn btn-sm btn-danger"
                            type="button"
                            disabled={busy}
                            aria-label={`Wipe ${t.name}`}
                            onClick={() => {
                              setWipeName("");
                              setWiping(t);
                            }}
                          >
                            Wipe
                          </button>
                        </div>
                      ) : null}
                    </td>
                    <td>
                      {u == null ? (
                        <button className="btn btn-sm" type="button" onClick={() => measureTarget(t.id)}>
                          Measure
                        </button>
                      ) : typeof u === "string" ? (
                        <span className="field-hint">{u}</span>
                      ) : (
                        <>
                          {formatBytes(u.total_bytes ?? 0)} in {u.objects ?? 0} objects: {u.restore_points ?? 0} restore point(s),
                          backup data {formatBytes(u.pack_bytes ?? 0)}
                          {u.other_bytes ? `, older whole-disk and archive backups ${formatBytes(u.other_bytes)}` : ""}.
                          {u.unreferenced_pack_bytes ? (
                            <div className="field-hint">
                              {formatBytes(u.unreferenced_pack_bytes)} of data no readable restore point needs
                              {u.foreign_points ? `; ${u.foreign_points} restore point(s) use another repository key, so some of it may be theirs` : ""}.
                            </div>
                          ) : null}
                        </>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}

      {report?.repository_move ? (
        <p className={"banner " + (report.repository_move.error ? "banner-error" : report.repository_move.running ? "" : "banner-ok")} role="status">
          {report.repository_move.running
            ? `Moving the backup repository to ${report.repository_move.to}. Backups are paused until it finishes.`
            : report.repository_move.error
              ? `The repository was not moved: ${report.repository_move.error}`
              : `The backup repository moved to ${report.repository_move.to}.`}
        </p>
      ) : null}

      {report && (report.deleted_workload_backups ?? []).length > 0 && mutate ? (
        <p className="btn-row">
          <button className="btn btn-sm btn-danger" type="button" disabled={busy} onClick={onPurgeDeleted}>
            Delete backups of deleted workloads
          </button>
        </p>
      ) : null}

      {report && ((report.unmanaged_restore_points ?? []).length > 0 || (report.deleted_workload_backups ?? []).length > 0) ? (
        <p className="field-hint">
          {(report.unmanaged_restore_points ?? []).length > 0
            ? `${(report.unmanaged_restore_points ?? []).length} local restore point(s) have no backup record. `
            : ""}
          {(report.deleted_workload_backups ?? []).length > 0
            ? `${(report.deleted_workload_backups ?? []).length} deleted workload(s) still have backups: ${(report.deleted_workload_backups ?? [])
                .map((w) => `${w.artifacts} backup(s), ${formatBytes(w.logical_bytes ?? 0)}`)
                .join("; ")}.`
            : ""}
        </p>
      ) : null}

      {failedEvents.length > 0 ? (
        <div className="table-wrap">
          <table aria-label="Failed cleanup operations">
            <thead>
              <tr>
                <th>When</th>
                <th>What failed</th>
              </tr>
            </thead>
            <tbody>
              {failedEvents.slice(0, 10).map((e) => (
                <tr key={`${e.at}-${e.message}`}>
                  <td>{formatWhen(e.at)}</td>
                  <td>{e.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      <ConfirmDialog
        open={wiping != null}
        title="Wipe backup target"
        confirmLabel="Wipe"
        danger
        confirmDisabled={!wiping || wipeName.trim() !== wiping.name}
        onConfirm={onWipe}
        onClose={() => setWiping(null)}
      >
        <p>
          This permanently deletes every No-dal backup in {wiping?.name}, including older whole-disk and archive backups.
          Other files in the bucket are not touched. Backups still on this host are kept and upload again with the next
          runs; backups that exist only in {wiping?.name} are gone for good.
        </p>
        <Field
          id="backup-wipe-confirm"
          label={`Type ${wiping?.name ?? "the target name"} to confirm`}
          value={wipeName}
          onChange={(e) => setWipeName(e.target.value)}
          autoComplete="off"
        />
      </ConfirmDialog>
      <ConfirmDialog
        open={relocating}
        title="Move backup repository"
        confirmLabel="Move"
        confirmDisabled={!relocate.poolId && !relocate.path.trim().startsWith("/")}
        onConfirm={onRelocate}
        onClose={() => setRelocating(false)}
      >
        <p>
          Choose where new backups are stored. A storage pool on its own disk keeps backups from filling the host root
          disk. Only an empty repository can move, so no backup is left behind. The backup key stays where it is.
        </p>
        {relocating ? (
          <BackupLocationPicker
            idPrefix="backup-repo"
            poolId={relocate.poolId}
            path={relocate.path}
            allowRoot={relocate.allowRoot}
            pathKind="repo"
            onChange={setRelocate}
          />
        ) : null}
      </ConfirmDialog>
    </article>
  );
}
