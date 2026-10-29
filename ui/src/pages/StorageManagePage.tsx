import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  cancelStorageScan,
  deleteScannedItems,
  getStorageScan,
  listNodes,
  listScanTargets,
  listWorkloads,
  startStorageScan,
  type ScanItem,
  type ScanStatus,
  type ScanTarget,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { HostDiskCleanups } from "../components/HostDisk";
import { DockerPrunePanel } from "../components/DockerPrune";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { navigate } from "../router";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";

type Filter = "all" | "low" | "review" | "important" | "critical";

const IMPORTANCE: Record<string, { status: string; label: string }> = {
  low: { status: "healthy", label: "Usually safe" },
  review: { status: "warning", label: "Review first" },
  important: { status: "degraded", label: "Important" },
  critical: { status: "critical", label: "Keep" },
};
const CONFIDENCE: Record<string, string> = { known: "No-dal knows", likely: "Likely", unknown: "Unknown" };

function when(unix: number): string {
  if (!unix) {
    return "-";
  }
  return new Date(unix * 1000).toLocaleDateString();
}

/** StorageManagePage finds what uses space, explains it, and deletes chosen items. */
export function StorageManagePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const ctx = useContextMenu();
  const [targets, setTargets] = useState<ScanTarget[] | null>(null);
  const [canDelete, setCanDelete] = useState(false);
  const [canOverride, setCanOverride] = useState(false);
  const [target, setTarget] = useState("/");
  const [sub, setSub] = useState("");
  const [oneFS, setOneFS] = useState(true);
  const [scan, setScan] = useState<ScanStatus | null>(null);
  const [path, setPath] = useState("");
  const [node, setNode] = useState<ScanItem | null>(null);
  const [items, setItems] = useState<ScanItem[]>([]);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Map<string, ScanItem>>(new Map());
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ deleted: number; freed: number; refused: { path: string; error?: string }[] } | null>(null);

  useEffect(() => {
    void listScanTargets()
      .then((res) => {
        setTargets(res.items);
        setCanDelete(res.can_delete);
        setCanOverride(Boolean(res.can_override));
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Storage scanning is unavailable"));
  }, []);

  async function browse(id: string, p: string) {
    try {
      const res = await getStorageScan(id, p);
      setScan(res.scan);
      if (res.node) {
        setNode(res.node);
        setItems(res.items ?? []);
        setPath(res.path);
      }
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not read the scan");
    }
  }

  useEffect(() => {
    if (!scan || scan.state !== "running") {
      return;
    }
    const timer = window.setInterval(() => void browse(scan.id, ""), 1500);
    return () => window.clearInterval(timer);
  }, [scan]);

  async function start() {
    setBusy(true);
    setError(null);
    setResult(null);
    setSelected(new Map());
    setNode(null);
    try {
      const root = sub.trim() ? `${target.replace(/\/$/, "")}/${sub.trim().replace(/^\//, "")}` : target;
      const s = await startStorageScan(root, oneFS);
      setScan(s);
      setPath("");
      if (s.state !== "running") {
        // A small folder can finish before the first poll.
        await browse(s.id, "");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the scan");
    } finally {
      setBusy(false);
    }
  }

  // A SuperAdmin may delete what No-dal protects; mounted filesystems never.
  const deletable = (x: ScanItem) => canDelete && !x.mount && (!x.insight.protected || canOverride);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return items.filter((x) => (filter === "all" || x.insight.importance === filter) && (!q || x.name.toLowerCase().includes(q) || x.insight.purpose.toLowerCase().includes(q)));
  }, [items, filter, query]);
  const parentSize = node?.size || 1;
  const crumbs = path ? path.split("/") : [];

  function toggle(x: ScanItem) {
    setSelected((cur) => {
      const next = new Map(cur);
      if (next.has(x.path)) {
        next.delete(x.path);
      } else {
        next.set(x.path, x);
      }
      return next;
    });
  }

  function rowItems(x: ScanItem): ContextItem[] {
    const out: ContextItem[] = [];
    if (x.dir && x.has_children && scan) {
      out.push({ label: "Open", onClick: () => void browse(scan.id, x.path) });
    }
    if (deletable(x)) {
      out.push({ label: selected.has(x.path) ? "Unselect" : "Select", onClick: () => toggle(x) });
    }
    out.push({ label: "Open in SM", onClick: () => void openInFiles(`${scan?.root.replace(/\/$/, "")}/${x.path}`, x.dir) });
    out.push({ label: "Copy path", onClick: () => void navigator.clipboard?.writeText(`${scan?.root.replace(/\/$/, "")}/${x.path}`) });
    if (deletable(x)) {
      out.push("sep", {
        label: "Delete",
        danger: true,
        onClick: () => {
          setSelected(new Map([[x.path, x]]));
          setConfirm(true);
        },
      });
    }
    return out;
  }

  async function doDelete() {
    if (!scan) {
      return;
    }
    const list = [...selected.keys()];
    setBusy(true);
    setError(null);
    try {
      const override = [...selected.values()].some((x) => x.insight.protected);
      const res = await deleteScannedItems(scan.id, list, override);
      setResult({ deleted: res.deleted, freed: res.freed_bytes, refused: res.results.filter((r) => !r.deleted) });
      setConfirm(false);
      setSelected(new Map());
      setItems((cur) => cur.filter((x) => !res.results.some((r) => r.deleted && r.path === x.path)));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  const chosen = [...selected.values()];
  const chosenBytes = chosen.reduce((n, x) => n + x.size, 0);
  const risky = chosen.filter((x) => x.insight.importance !== "low");

  return (
    <section className="page page-wide" aria-labelledby="manage-heading">
      <PageHeader
        id="manage-heading"
        title="Manage storage"
        kicker="Measure what uses space on the host or a storage pool, see what each item is for, and delete what you no longer need. Workload disks, backups and system files are protected."
      />
      {error ? <ErrorNotice error={error} /> : null}
      <HostDiskCleanups mutate={hasGrant(user, "storage.pool.create")} />
      <DockerPrunePanel mutate={hasGrant(user, "compute.lifecycle")} />
      <article className="panel" aria-labelledby="scan-heading">
        <h2 id="scan-heading">Find what is using space</h2>
        {!targets ? (
          <LoadingState label="Loading storage" />
        ) : (
          <div className="toolbar">
            <div className="toolbar-group">
              <select className="field-input" aria-label="Where to scan" value={target} onChange={(e) => setTarget(e.target.value)}>
                {targets.map((t) => (
                  <option key={t.path} value={t.path}>
                    {t.label} {t.total_bytes ? `· ${formatBytes(t.used_bytes)} of ${formatBytes(t.total_bytes)} used` : ""}
                  </option>
                ))}
              </select>
              <input className="field-input" aria-label="Folder inside (optional)" placeholder="folder inside (optional)" value={sub} onChange={(e) => setSub(e.target.value)} />
              <label className="check-row">
                <input type="checkbox" checked={oneFS} onChange={(e) => setOneFS(e.target.checked)} />
                <span>Stay on this filesystem</span>
              </label>
              <button className="btn btn-primary" type="button" disabled={busy || scan?.state === "running"} onClick={() => void start()}>
                Scan
              </button>
            </div>
          </div>
        )}
        {scan?.state === "running" ? (
          <div className="banner" role="status">
            Scanning {scan.root}: {scan.entries.toLocaleString()} items, {formatBytes(scan.bytes)} so far.
            {scan.current ? <div className="meta">{scan.current}</div> : null}{" "}
            <button className="btn btn-sm btn-ghost" type="button" onClick={() => void cancelStorageScan(scan.id).then(setScan)}>
              Cancel
            </button>
          </div>
        ) : null}
        {scan && (scan.state === "failed" || scan.state === "cancelled") ? (
          <p className="banner banner-warn">The scan {scan.state === "failed" ? `failed: ${scan.error}` : "was cancelled"}. Results shown are partial.</p>
        ) : null}
        {result ? (
          <div className={"banner " + (result.refused.length ? "banner-warn" : "banner-ok")} role="status">
            Deleted {result.deleted} item(s), freeing about {formatBytes(result.freed)}.
            {result.refused.length ? (
              <ul className="plain-list">
                {result.refused.map((r) => (
                  <li key={r.path}>
                    <code>{r.path}</code>: {r.error}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </article>

      {node && scan ? (
        <article className="panel" aria-label="Scan results">
          <nav className="crumbs" aria-label="Folder">
            <button className="linkish" type="button" onClick={() => void browse(scan.id, "")}>
              {scan.root}
            </button>
            {crumbs.map((c, i) => (
              <span key={i}>
                {" / "}
                <button className="linkish" type="button" onClick={() => void browse(scan.id, crumbs.slice(0, i + 1).join("/"))}>
                  {c}
                </button>
              </span>
            ))}
          </nav>
          <p className="meta">
            {formatBytes(node.size)} on disk ({formatBytes(node.apparent)} apparent) · {node.files.toLocaleString()} files · {node.dirs.toLocaleString()} folders
            {scan.fs_total ? ` · ${((node.size / scan.fs_total) * 100).toFixed(1)}% of the ${formatBytes(scan.fs_total)} filesystem, ${formatBytes(scan.fs_free)} free` : ""}
          </p>
          <p className="field-hint">
            <strong>{node.insight.purpose}</strong> ({CONFIDENCE[node.insight.confidence] ?? node.insight.confidence}). {node.insight.advice}
          </p>
          <div className="toolbar">
            <div className="toolbar-group">
              <Segmented<Filter>
                ariaLabel="Importance"
                value={filter}
                onChange={setFilter}
                options={[
                  { id: "all", label: "All" },
                  { id: "low", label: "Usually safe" },
                  { id: "review", label: "Review" },
                  { id: "important", label: "Important" },
                  { id: "critical", label: "Keep" },
                ]}
              />
              <label className="search-field">
                <input className="field-input" type="search" placeholder="Search this folder" aria-label="Search this folder" value={query} onChange={(e) => setQuery(e.target.value)} />
              </label>
            </div>
            {chosen.length > 0 ? (
              <div className="toolbar-group">
                <span className="meta">
                  {chosen.length} selected · {formatBytes(chosenBytes)}
                </span>
                <button
                  className="btn btn-sm btn-danger"
                  type="button"
                  onClick={() => {
                    setConfirm(true);
                  }}
                >
                  Delete selected
                </button>
                <button className="btn btn-sm btn-ghost" type="button" onClick={() => setSelected(new Map())}>
                  Clear
                </button>
              </div>
            ) : null}
          </div>
          {shown.length === 0 ? (
            <EmptyState title="Nothing here">{items.length ? "No item matches the filter." : "This folder is empty."}</EmptyState>
          ) : (
            <div className="table-wrap">
              <table aria-label="Folder contents">
                <thead>
                  <tr>
                    {canDelete ? <th /> : null}
                    <th>Name</th>
                    <th>Size</th>
                    <th>Files</th>
                    <th>Modified</th>
                    <th>What it is</th>
                    <th>Importance</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {shown.map((x) => {
                    const imp = IMPORTANCE[x.insight.importance] ?? { status: "unknown", label: x.insight.importance };
                    const menu = rowItems(x);
                    return (
                      <tr key={x.path} onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, x.name, menu)}>
                        {canDelete ? (
                          <td>
                            <input type="checkbox" aria-label={`Select ${x.name}`} disabled={!deletable(x)} checked={selected.has(x.path)} onChange={() => toggle(x)} />
                          </td>
                        ) : null}
                        <td>
                          {x.dir && x.has_children ? (
                            <button className="linkish" type="button" onClick={() => void browse(scan.id, x.path)}>
                              📁 {x.name}
                            </button>
                          ) : (
                            <span>
                              {x.dir ? "📁 " : x.symlink ? "↪ " : ""}
                              {x.name}
                            </span>
                          )}
                          {x.mount ? <span className="tag-inline">other filesystem</span> : null}
                          {x.error ? <span className="tag-inline" title={x.error}>unreadable</span> : null}
                        </td>
                        <td title={`${formatBytes(x.apparent)} apparent`}>
                          <div className="scan-size">
                            <span className="scan-bar" aria-hidden="true">
                              <span style={{ width: `${Math.max(1, Math.round((x.size / parentSize) * 100))}%` }} />
                            </span>
                            {formatBytes(x.size)}
                          </div>
                        </td>
                        <td>{x.dir ? x.files.toLocaleString() : ""}</td>
                        <td>{when(x.mtime)}</td>
                        <td>
                          {x.insight.purpose}
                          <div className="meta" title={x.insight.advice}>
                            {CONFIDENCE[x.insight.confidence] ?? x.insight.confidence}
                            {x.insight.advice ? ` · ${x.insight.advice}` : ""}
                          </div>
                        </td>
                        <td>
                          <StatusBadge status={imp.status} label={imp.label} />
                        </td>
                        <td>
                          <ActionMenu label={`${x.name} actions`} items={menu.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
                        </td>
                      </tr>
                    );
                  })}
                  {node.other ? (
                    <tr>
                      {canDelete ? <td /> : null}
                      <td colSpan={7} className="meta">
                        {node.other.count.toLocaleString()} smaller items, {formatBytes(node.other.size)} in total
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          )}
        </article>
      ) : null}

      <Dialog
        open={confirm}
        title={`Delete ${chosen.length} item(s)`}
        wide
        onClose={() => setConfirm(false)}
        footer={
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setConfirm(false)}>
              Cancel
            </button>
            <button className="btn btn-danger" type="button" disabled={busy} onClick={() => void doDelete()}>
              {busy ? "Deleting" : `Delete ${formatBytes(chosenBytes)}`}
            </button>
          </div>
        }
      >
        <p>These are deleted permanently from {scan?.root}. There is no undo.</p>
        {chosen.some((x) => x.insight.protected) ? (
          <p className="banner banner-warn">
            You are overriding No-dal's protection for: {chosen.filter((x) => x.insight.protected).map((x) => x.name).join(", ")}. No-dal or the
            host may depend on them.
          </p>
        ) : null}
        {risky.length ? (
          <p className="banner banner-warn">
            {risky.length} item(s) are not marked usually safe. Check what they are first: {risky.map((x) => x.name).join(", ")}.
          </p>
        ) : null}
        <ul className="plain-list">
          {chosen.map((x) => (
            <li key={x.path}>
              <code>{x.path}</code> · {formatBytes(x.size)} · {x.insight.purpose}
            </li>
          ))}
        </ul>
      </Dialog>
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}

const containerRootRe = /\/volumes\/container-root\/([0-9a-f-]{36})(\/.*)?$/;

// openInFiles opens a scanned path in the Files page. Inside a system
// container's root disk it opens that container's files at the same place;
// anywhere else it opens the host's files. Files open at their folder.
async function openInFiles(abs: string, dir: boolean) {
  const target = dir ? abs : abs.replace(/\/[^/]*$/, "") || "/";
  const m = containerRootRe.exec(target);
  if (m) {
    try {
      const { items } = await listWorkloads();
      const wl = items.find((w) => (w.disks ?? []).some((d) => d.volume_id === m[1]));
      if (wl) {
        navigate(`/workloads/${wl.id}/files?path=${encodeURIComponent(m[2] || "/")}`);
        return;
      }
    } catch {
      // Fall back to the host view below.
    }
  }
  const nodes = await listNodes().catch(() => []);
  const node = nodes[0]?.id ?? "local";
  navigate(`/nodes/${node}/files?path=${encodeURIComponent(target)}`);
}

export type StorageTab = "storage" | "manage";

/** StorageTabs switches between the Storage and Manage pages. */
export function StorageTabs({ tab, onChange }: { tab: StorageTab; onChange: (t: StorageTab) => void }) {
  return (
    <Segmented<StorageTab>
      ariaLabel="Storage sections"
      value={tab}
      onChange={onChange}
      options={[
        { id: "storage", label: "Storage" },
        { id: "manage", label: "Manage" },
      ]}
    />
  );
}
