import type { ReactNode } from "react";
import type { BackupRun, PlatformWorkloadRecovery } from "../generated/openapi";
import { formatRelative, formatWhen } from "../format";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";
import { ActionMenu, type ActionItem } from "./ActionMenu";
import { protectionLabel, protectionTone, recoveryLabel, recoveryTone, runStatusLabel, runTone } from "./BackupLabels";
import { gb, small, UNITS, type Unit, type WorkloadSize } from "./BackupSizes";

/** BackupWorkloadRow is one workload of the Backups table. */
export type BackupWorkloadRow = {
  id: string;
  name: string;
  kind: string;
  /** missing workloads were deleted but still have backups. */
  missing: boolean;
  size?: WorkloadSize;
  lastRun?: BackupRun;
  /** policies names the policies that back the workload up. */
  policies: string[];
  /** dr is the workload's disaster recovery check, when it is covered. */
  dr?: PlatformWorkloadRecovery;
  /** keepImages is set for system containers only. */
  keepImages?: boolean;
};

const at = (iso: string | undefined) => Date.parse(iso ?? "") || 0;

export const BACKUP_COLUMNS: TableColumn<BackupWorkloadRow>[] = [
  { id: "name", label: "Workload", required: true, sortValue: (r) => r.name },
  { id: "before", label: "Size before", sortValue: (r) => r.size?.before ?? 0 },
  { id: "latest", label: "Latest backup", sortValue: (r) => r.size?.latestStored ?? 0 },
  { id: "total", label: "Total stored", sortValue: (r) => r.size?.total ?? 0 },
  { id: "last", label: "Last backup", sortValue: (r) => at(r.size?.latest?.created_at) },
  { id: "policy", label: "Policy", defaultHidden: true, sortValue: (r) => r.policies.join(", ") },
  { id: "run", label: "Last run", defaultHidden: true, sortValue: (r) => at(r.lastRun?.started_at) },
  { id: "points", label: "Restore points", defaultHidden: true, sortValue: (r) => r.size?.count ?? 0 },
  { id: "dr", label: "Disaster recovery", defaultHidden: true, sortValue: (r) => r.dr?.status ?? "" },
  { id: "images", label: "Image copies", defaultHidden: true, sortValue: (r) => (r.keepImages == null ? -1 : r.keepImages ? 1 : 0) },
];

const NUMERIC = new Set(["before", "latest", "total", "points"]);

/**
 * BackupWorkloadTable is the Backups page's one table: a row per workload
 * with its sizes, last backup and, when chosen with the pencil, its policy,
 * last run, disaster recovery and image copy state. Rows can be selected
 * for the bulk actions above the table; each row has its own menu.
 */
export function BackupWorkloadTable({
  rows,
  unit,
  onUnit,
  selected,
  onSelect,
  rowActions,
  drChecked,
  drAvailable,
  mutate,
  onKeepImages,
  toolbar,
}: {
  rows: BackupWorkloadRow[];
  unit: Unit;
  onUnit: (unit: Unit) => void;
  selected: Set<string>;
  onSelect: (next: Set<string>) => void;
  rowActions: (row: BackupWorkloadRow) => ActionItem[];
  /** drChecked is true once the platform backup checked recoverability. */
  drChecked: boolean;
  /** drAvailable is false when the platform backup status could not be read. */
  drAvailable: boolean;
  mutate: boolean;
  onKeepImages: (row: BackupWorkloadRow, on: boolean) => void;
  toolbar?: ReactNode;
}) {
  const table = useTablePrefs<BackupWorkloadRow>("backups.workloads", BACKUP_COLUMNS, { id: "name", dir: "asc" });
  const shown = table.sortRows(rows);
  const allOn = rows.length > 0 && rows.every((r) => selected.has(r.id));
  const someOn = !allOn && rows.some((r) => selected.has(r.id));

  function toggle(id: string, on: boolean) {
    const next = new Set(selected);
    if (on) next.add(id);
    else next.delete(id);
    onSelect(next);
  }

  function cell(col: string, r: BackupWorkloadRow): ReactNode {
    const s = r.size;
    switch (col) {
      case "name":
        return (
          <>
            <span className="bk-strong">{r.name}</span>
            <div className="bk-hint">{r.missing ? "Deleted workload" : r.kind === "vm" ? "Virtual machine" : "System container"}</div>
          </>
        );
      case "before":
        return s?.before ? gb(s.before) : <span className="bk-hint">None</span>;
      case "latest":
        return s?.latest ? small(s.latestStored, unit) : <span className="bk-hint">None</span>;
      case "total":
        return s ? (
          <span title={`${s.count} restore point${s.count === 1 ? "" : "s"}`}>{small(s.total, unit)}</span>
        ) : (
          <span className="bk-hint">None</span>
        );
      case "last":
        return s?.latest ? (
          <>
            <span className={"bk-pill " + protectionTone(s.latest)} title={s.latest.remote_error && s.latest.remote_state !== "protected" ? s.latest.remote_error : undefined}>
              {protectionLabel(s.latest)}
            </span>
            <div className="bk-hint" title={formatWhen(s.latest.created_at)}>
              {formatRelative(s.latest.created_at)}
            </div>
          </>
        ) : (
          <span className="bk-hint">Never</span>
        );
      case "policy":
        return r.policies.length ? r.policies.join(", ") : <span className="bk-hint">No policy</span>;
      case "run":
        return r.lastRun ? (
          <>
            <span className={"bk-pill " + runTone(r.lastRun.status)} title={r.lastRun.error || undefined}>
              {runStatusLabel(r.lastRun.status)}
            </span>
            <div className="bk-hint" title={formatWhen(r.lastRun.started_at)}>
              {formatRelative(r.lastRun.started_at)}
            </div>
          </>
        ) : (
          <span className="bk-hint">None</span>
        );
      case "points":
        return s?.count ?? 0;
      case "dr":
        if (!drAvailable) {
          return <span className="bk-hint">Unknown</span>;
        }
        if (!drChecked) {
          return <span className="bk-hint">Not checked yet</span>;
        }
        if (!r.dr) {
          return <span className="bk-hint" title="No policy backs this workload up to object storage">Not covered</span>;
        }
        return (
          <>
            <span className={"bk-pill " + recoveryTone(r.dr.status)} title={r.dr.error || undefined}>
              {recoveryLabel(r.dr.status)}
            </span>
            <div className="bk-hint" title={r.dr.latest_at ? formatWhen(r.dr.latest_at) : undefined}>
              {r.dr.latest_at ? `Offsite ${formatRelative(r.dr.latest_at)}${r.dr.deep ? ", read back" : ""}` : r.dr.error || "No offsite restore point"}
            </div>
          </>
        );
      case "images":
        if (r.keepImages == null) {
          return <span className="bk-hint">Not a container</span>;
        }
        return (
          <label className="field-check bk-check" title="Keep a copy of the Docker images the container runs, so a restore does not need the registry">
            <input type="checkbox" checked={r.keepImages} disabled={!mutate || r.missing} onChange={(e) => onKeepImages(r, e.target.checked)} />
            {r.keepImages ? "Kept" : "Digest only"}
          </label>
        );
    }
    return null;
  }

  return (
    <section className="panel bk-panel bk-main" aria-labelledby="backup-workloads-heading">
      <div className="bk-head">
        <h2 id="backup-workloads-heading">Workloads</h2>
        {toolbar}
      </div>
      {rows.length === 0 ? (
        <div className="bk-empty">
          <p className="empty-title">No workloads to back up</p>
          <p className="bk-hint">Virtual machines and system containers show here.</p>
        </div>
      ) : (
        <div className="table-wrap bk-wtable">
          <table aria-label="Workload backups">
            <thead>
              <tr>
                <th className="cell-check">
                  <input
                    type="checkbox"
                    aria-label="Select every workload"
                    checked={allOn}
                    ref={(el) => {
                      if (el) el.indeterminate = someOn;
                    }}
                    onChange={(e) => onSelect(e.target.checked ? new Set(rows.map((r) => r.id)) : new Set())}
                  />
                </th>
                {table.visible.map((col) => (
                  <SortHeader key={col.id} column={col} prefs={table} className={NUMERIC.has(col.id) ? "bk-num" : undefined} />
                ))}
                <th className="cell-actions">
                  <ColumnEditor
                    prefs={table}
                    label="Edit backup columns"
                    extra={
                      <label className="check-row">
                        <span>Size unit</span>
                        <select className="field-input bk-select" aria-label="Size unit" value={unit} onChange={(e) => onUnit(e.target.value as Unit)}>
                          {UNITS.map((u) => (
                            <option key={u} value={u}>
                              {u}
                            </option>
                          ))}
                        </select>
                      </label>
                    }
                  />
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => (
                <tr key={r.id} className={selected.has(r.id) ? "is-selected" : undefined}>
                  <td className="cell-check">
                    <input type="checkbox" aria-label={`Select ${r.name}`} checked={selected.has(r.id)} onChange={(e) => toggle(r.id, e.target.checked)} />
                  </td>
                  {table.visible.map((col) => (
                    <td key={col.id} className={NUMERIC.has(col.id) ? "bk-num" : undefined}>
                      {cell(col.id, r)}
                    </td>
                  ))}
                  <td className="cell-actions">
                    <ActionMenu label={`${r.name} backup actions`} items={rowActions(r)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
