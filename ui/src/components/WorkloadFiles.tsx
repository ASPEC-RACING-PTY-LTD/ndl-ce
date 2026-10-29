import { useEffect, useMemo, useState } from "react";
import { ApiError, deleteWorkloadBackups, getWorkloadBackupRules, listWorkloadBackupFiles, putWorkloadBackupRules } from "../api/client";
import type { WorkloadFileEntry } from "../generated/openapi";
import { confirmAction } from "../confirm";
import { formatBytes, formatRelative, formatWhen } from "../format";
import { ErrorNotice } from "./ErrorNotice";

type Sort = "name" | "size" | "date";
type Rule = "" | "skip" | "keep";
type Workload = { id: string; name: string; kind: string };

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

export function sortEntries(entries: WorkloadFileEntry[], sort: Sort): WorkloadFileEntry[] {
  const byName = (a: WorkloadFileEntry, b: WorkloadFileEntry) => a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true });
  const out = [...entries];
  switch (sort) {
    case "size":
      out.sort((a, b) => b.size - a.size || byName(a, b));
      break;
    case "date":
      out.sort((a, b) => b.mtime - a.mtime || byName(a, b));
      break;
    default:
      out.sort((a, b) => Number(b.dir) - Number(a.dir) || byName(a, b));
  }
  return out;
}

/** effectiveRule is the rule the nearest listed path gives p. */
export function effectiveRule(p: string, skip: string[], keep: string[]): Rule {
  let best = -1;
  let rule: Rule = "";
  const check = (list: string[], r: Rule) => {
    for (const q of list) {
      if ((q === p || q === "/" || p.startsWith(q + "/")) && q.length > best) {
        best = q.length;
        rule = r;
      }
    }
  };
  check(keep, "keep");
  check(skip, "skip");
  return rule;
}

/**
 * WorkloadFiles browses a system container's files with sizes and short
 * labels, and edits the workload's own backup rules: paths to skip and
 * paths always kept. The rules are saved inside the workload, at
 * /.ndl-backup/rules.json, and every backup of it follows them.
 */
export function WorkloadFiles({
  workloads,
  mutate,
  onBackupsDeleted,
  initialWorkloadId = "",
}: {
  workloads: Workload[];
  mutate: boolean;
  onBackupsDeleted?: () => void;
  /** initialWorkloadId opens on that workload's files. */
  initialWorkloadId?: string;
}) {
  const containers = useMemo(() => workloads.filter((w) => w.kind === "system-container").sort((a, b) => a.name.localeCompare(b.name)), [workloads]);
  const [workloadId, setWorkloadId] = useState(initialWorkloadId);
  const [dir, setDir] = useState("/");
  const [entries, setEntries] = useState<WorkloadFileEntry[] | null>(null);
  const [sort, setSort] = useState<Sort>("name");
  const [skip, setSkip] = useState<string[]>([]);
  const [keep, setKeep] = useState<string[]>([]);
  const [savedRules, setSavedRules] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const workload = containers.find((w) => w.id === workloadId);

  useEffect(() => {
    if (!workloadId) {
      return;
    }
    setError(null);
    setNotice(null);
    getWorkloadBackupRules(workloadId)
      .then((r) => {
        setSkip(r.skip ?? []);
        setKeep(r.keep ?? []);
        setSavedRules(JSON.stringify({ s: r.skip ?? [], k: r.keep ?? [] }));
      })
      .catch((err) => setError(errorText(err, "Could not read the backup rules")));
  }, [workloadId]);

  useEffect(() => {
    if (!workloadId) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    listWorkloadBackupFiles(workloadId, dir)
      .then((l) => {
        if (!cancelled) {
          setEntries(l.entries);
          setError(null);
        }
      })
      .catch((err) => !cancelled && setError(errorText(err, "Could not list this folder")))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [workloadId, dir]);

  const dirty = JSON.stringify({ s: skip, k: keep }) !== savedRules;
  const crumbs = dir === "/" ? [] : dir.split("/").filter(Boolean);
  const shown = useMemo(() => sortEntries(entries ?? [], sort), [entries, sort]);
  const skippedHere = (entries ?? []).filter((e) => effectiveRule(e.path, skip, keep) === "skip").reduce((n, e) => n + e.size, 0);

  function setRule(path: string, rule: Rule) {
    setSkip((cur) => (rule === "skip" ? [...cur.filter((p) => p !== path), path] : cur.filter((p) => p !== path)));
    setKeep((cur) => (rule === "keep" ? [...cur.filter((p) => p !== path), path] : cur.filter((p) => p !== path)));
  }

  async function save() {
    if (!workloadId) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const r = await putWorkloadBackupRules(workloadId, { skip, keep });
      setSkip(r.skip ?? []);
      setKeep(r.keep ?? []);
      setSavedRules(JSON.stringify({ s: r.skip ?? [], k: r.keep ?? [] }));
      setNotice(`Saved in ${workload?.name ?? "the workload"} at ${r.path ?? "/.ndl-backup/rules.json"}. The next backup follows them.`);
    } catch (err) {
      setError(errorText(err, "Could not save the rules"));
    } finally {
      setBusy(false);
    }
  }

  async function deleteAll() {
    if (!workload || !confirmAction(`Delete every backup of ${workload.name}, on this host and in cloud storage? Protected backups are kept.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await deleteWorkloadBackups(workload.id);
      setNotice(`Deleted ${res.deleted ?? 0} backup(s) of ${workload.name}${res.protected_kept ? `; ${res.protected_kept} protected kept` : ""}. Space is freed in the background.`);
      onBackupsDeleted?.();
    } catch (err) {
      setError(errorText(err, "Could not delete the backups"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel bk-panel" aria-labelledby="workload-files-heading">
      <div className="bk-head">
        <h2 id="workload-files-heading">Workload files and backup rules</h2>
        <div className="bk-actions">
          <select
            className="field-input bk-select"
            aria-label="Workload"
            value={workloadId}
            onChange={(e) => {
              setWorkloadId(e.target.value);
              setDir("/");
              setEntries(null);
            }}
          >
            <option value="">Choose a container</option>
            {containers.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </select>
          {workload && mutate ? (
            <>
              <button className="btn btn-sm btn-primary" type="button" disabled={busy || !dirty} onClick={() => void save()}>
                Save rules
              </button>
              <button className="btn btn-sm btn-ghost btn-danger-text" type="button" disabled={busy} onClick={() => void deleteAll()}>
                Delete all backups
              </button>
            </>
          ) : null}
        </div>
      </div>
      <p className="bk-hint">
        Mark folders or files to <b>Skip</b> (never backed up) or <b>Keep</b> (always backed up, even inside a skipped
        folder). Rules are saved inside the container and every backup follows them, whatever the policy says.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {workload ? (
        <>
          <div className="bk-head">
            <nav className="bk-crumbs" aria-label="Folder">
              <button type="button" className="bk-link" onClick={() => setDir("/")}>
                {workload.name}
              </button>
              {crumbs.map((c, i) => (
                <span key={`${c}-${i}`}>
                  {" / "}
                  <button type="button" className="bk-link" onClick={() => setDir("/" + crumbs.slice(0, i + 1).join("/"))}>
                    {c}
                  </button>
                </span>
              ))}
            </nav>
            <div className="bk-actions">
              {skippedHere ? <span className="bk-hint">{formatBytes(skippedHere)} here is skipped</span> : null}
              <div className="bk-toggle" role="group" aria-label="Sort by">
                {(["name", "size", "date"] as Sort[]).map((s) => (
                  <button key={s} type="button" className={"btn btn-sm" + (sort === s ? " is-selected" : "")} aria-pressed={sort === s} onClick={() => setSort(s)}>
                    {s === "name" ? "A–Z" : s === "size" ? "Size" : "Date"}
                  </button>
                ))}
              </div>
            </div>
          </div>
          <div className="bk-scroll is-tall">
            <table className="bk-table" aria-label="Workload files">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>What it is</th>
                  <th className="bk-num">Size</th>
                  <th>Changed</th>
                  <th className="bk-actions-col">Backup</th>
                </tr>
              </thead>
              <tbody>
                {dir !== "/" ? (
                  <tr>
                    <td colSpan={5}>
                      <button type="button" className="bk-link" onClick={() => setDir(dir.split("/").slice(0, -1).join("/") || "/")}>
                        .. up one folder
                      </button>
                    </td>
                  </tr>
                ) : null}
                {loading && !entries ? (
                  <tr>
                    <td colSpan={5} className="bk-hint">
                      Measuring folders
                    </td>
                  </tr>
                ) : null}
                {shown.map((e) => {
                  const own: Rule = skip.includes(e.path) ? "skip" : keep.includes(e.path) ? "keep" : "";
                  const eff = effectiveRule(e.path, skip, keep);
                  return (
                    <tr key={e.path} className={eff === "skip" ? "bk-row-skipped" : undefined}>
                      <td>
                        {e.dir && !e.symlink ? (
                          <button type="button" className="bk-link" onClick={() => setDir(e.path)}>
                            {e.name}/
                          </button>
                        ) : (
                          <span>
                            {e.name}
                            {e.symlink ? " →" : ""}
                          </span>
                        )}
                      </td>
                      <td>
                        <span className="bk-pill">{e.label}</span>
                      </td>
                      <td className="bk-num">
                        {e.partial ? "≥ " : ""}
                        {formatBytes(e.size)}
                      </td>
                      <td title={formatWhen(new Date(e.mtime * 1000).toISOString())}>{formatRelative(new Date(e.mtime * 1000).toISOString())}</td>
                      <td className="bk-actions-col">
                        <div className="bk-toggle" role="group" aria-label={`Backup rule for ${e.name}`}>
                          {(["", "skip", "keep"] as Rule[]).map((r) => (
                            <button
                              key={r || "default"}
                              type="button"
                              disabled={!mutate}
                              className={"btn btn-sm" + (own === r ? " is-selected" : "")}
                              aria-pressed={own === r}
                              title={r === "" ? (eff ? `Follows the ${eff} rule above` : "Follows the policy") : undefined}
                              onClick={() => setRule(e.path, r)}
                            >
                              {r === "" ? "Policy" : r === "skip" ? "Skip" : "Keep"}
                            </button>
                          ))}
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {entries && entries.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="bk-hint">
                      This folder is empty.
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
          {skip.length + keep.length > 0 ? (
            <p className="bk-hint">
              {skip.length ? `Skipped: ${skip.join(", ")}. ` : ""}
              {keep.length ? `Always kept: ${keep.join(", ")}.` : ""}
              {dirty ? " Not saved yet." : ""}
            </p>
          ) : null}
        </>
      ) : (
        <p className="bk-hint">Choose a container to see what is inside it and what its backups keep.</p>
      )}
    </section>
  );
}
