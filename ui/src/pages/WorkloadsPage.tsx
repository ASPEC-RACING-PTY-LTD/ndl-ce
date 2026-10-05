import { useEffect, useMemo, useState, type DragEvent, type KeyboardEvent } from "react";
import { bulkDeleteWorkloads, listNodes, listWorkloads, patchMe } from "../api/client";
import type { NodeSummary } from "../api/phase2";
import type { Workload } from "../api/phase5";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { kindLabel, osLabel } from "../labels";
import { useQuery } from "../query";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { Segmented } from "../ui/Segmented";
import { moveId, orderWorkloads, savedSort, shiftId, type WorkloadSort } from "../workloadOrder";

const containerKind = "system-container";

type KindFilter = "all" | "system-container" | "vm" | "oci";

function isContainer(w: Workload): boolean {
  return w.kind === containerKind;
}

function matches(w: Workload, q: string, kind: KindFilter): boolean {
  if (kind !== "all" && w.kind !== kind) {
    return false;
  }
  if (!q) {
    return true;
  }
  return w.name.toLowerCase().includes(q) || (w.kind ?? "").toLowerCase().includes(q) || kindLabel(w.kind).toLowerCase().includes(q);
}

function hostLive(node: NodeSummary): boolean {
  const st = (node.status || "").toLowerCase();
  return st === "available" || st === "ready" || st === "running" || st === "online";
}

export function WorkloadsPage() {
  const session = useSession();
  const me = session.status === "ready" ? session.user : null;
  const mutate = canMutate(me?.roles);
  const workloadsQ = useQuery("workloads", () => listWorkloads(), 10000);
  const nodesQ = useQuery("nodes", () => listNodes(), 15000);
  const items = useMemo(() => workloadsQ.data?.items ?? [], [workloadsQ.data]);
  const nodes = nodesQ.data ?? [];
  const loading = workloadsQ.loading && !workloadsQ.data;
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<KindFilter>("all");
  const [sort, setSort] = useState<WorkloadSort>(() => savedSort(me?.workload_sort));
  const [order, setOrder] = useState<string[]>(() => me?.workload_order ?? []);
  const [editing, setEditing] = useState(false);
  const [dragId, setDragId] = useState<string | null>(null);
  const [overId, setOverId] = useState<string | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [results, setResults] = useState<{ id: string; name?: string; ok: boolean; error?: string }[] | null>(null);

  useEffect(() => {
    if (workloadsQ.error && !workloadsQ.data) {
      setError(workloadsQ.error);
    }
  }, [workloadsQ.error, workloadsQ.data]);

  const ordered = useMemo(() => orderWorkloads(items, sort, order), [items, sort, order]);
  const q = query.trim().toLowerCase();
  const filtered = useMemo(
    () => (editing ? ordered : ordered.filter((w) => matches(w, q, kind))),
    [ordered, q, kind, editing],
  );
  const hosts = useMemo(
    () => (editing || (!q && kind === "all") ? nodes : nodes.filter((n) => (n.name || n.id).toLowerCase().includes(q) && kind === "all")),
    [nodes, q, kind, editing],
  );

  const selectable = useMemo(() => filtered.filter(isContainer), [filtered]);
  const selectedItems = useMemo(() => selectable.filter((w) => selected.includes(w.id)), [selectable, selected]);
  const allSelected = selectable.length > 0 && selectedItems.length === selectable.length;
  const counts = useMemo(() => {
    const running = items.filter((w) => w.status === "running").length;
    return { total: items.length, running, stopped: items.length - running };
  }, [items]);

  async function persist(nextSort: WorkloadSort, nextOrder: string[]) {
    setSaveError(null);
    try {
      const saved = await patchMe({
        workload_sort: nextSort === "custom" ? "custom" : "name",
        workload_order: nextOrder,
      });
      session.applyUser?.(saved);
    } catch (err) {
      setSaveError(err instanceof Error ? `Order not saved: ${err.message}` : "Order not saved");
    }
  }

  function changeSort(next: WorkloadSort) {
    setSort(next);
    if (next !== "name-desc") {
      void persist(next, order);
    }
  }

  function startEditing() {
    const current = ordered.map((w) => w.id);
    setOrder(current);
    setSort("custom");
    setSelected([]);
    setEditing(true);
    void persist("custom", current);
  }

  function reorder(next: string[]) {
    setOrder(next);
    void persist("custom", next);
  }

  function onDrop(event: DragEvent, target: string) {
    event.preventDefault();
    if (dragId && dragId !== target) {
      reorder(moveId(ordered.map((w) => w.id), dragId, target));
    }
    setDragId(null);
    setOverId(null);
  }

  function onHandleKey(event: KeyboardEvent, id: string) {
    const delta = event.key === "ArrowUp" ? -1 : event.key === "ArrowDown" ? 1 : 0;
    if (!delta) {
      return;
    }
    event.preventDefault();
    reorder(shiftId(ordered.map((w) => w.id), id, delta));
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLButtonElement>(`[data-drag-handle="${id}"]`)?.focus();
    });
  }

  function toggle(id: string) {
    setSelected((cur) => (cur.includes(id) ? cur.filter((n) => n !== id) : [...cur, id]));
  }

  async function onConfirmDelete() {
    setBusy(true);
    setError(null);
    try {
      const res = await bulkDeleteWorkloads(selectedItems.map((w) => w.id));
      setResults(res.results ?? []);
      setConfirmOpen(false);
      setSelected([]);
      await workloadsQ.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Bulk delete failed");
    } finally {
      setBusy(false);
    }
  }

  const showSelect = mutate && !editing;
  const leadCol = showSelect || editing;
  const nameSort = sort === "name" ? "name-desc" : "name";

  return (
    <section className="page page-wide" aria-labelledby="workloads-heading">
      <PageHeader
        id="workloads-heading"
        icon="workloads"
        title="Workloads"
        kicker={
          items.length
            ? `${counts.total} workloads · ${counts.running} running · ${counts.stopped} not running`
            : "System containers, virtual machines and OCI applications on this appliance."
        }
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <Link className="btn btn-ghost" href="/templates">
                Templates
              </Link>
              <Link className="btn btn-ghost" href="/stacks">
                Stacks
              </Link>
              <Link className="btn btn-ghost" href="/workloads/import">
                Import VM
              </Link>
              <Link className="btn btn-secondary" href="/workloads/new/oci">
                Create OCI
              </Link>
              <Link className="btn btn-secondary" href="/workloads/new/vm">
                Create VM
              </Link>
              <Link className="btn btn-primary" href="/workloads/new/system-container">
                <Icon name="create" size={14} />
                Create system container
              </Link>
            </div>
          ) : null
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {saveError ? <ErrorState>{saveError}</ErrorState> : null}
      {results ? (
        <div className="banner" role="status">
          <p>
            Deleted {results.filter((r) => r.ok).length} of {results.length} containers.
          </p>
          <ul>
            {results.map((r) => (
              <li key={r.id}>
                {r.name || r.id}: {r.ok ? "deleted" : r.error || "failed"}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div className="stack">
        <div className="toolbar">
          <div className="toolbar-group">
            <label className="search-field">
              <Icon name="search" size={14} />
              <input
                className="field-input"
                type="search"
                placeholder="Search by name or type"
                value={query}
                disabled={editing}
                onChange={(e) => setQuery(e.target.value)}
                aria-label="Search workloads"
              />
            </label>
            <Segmented<KindFilter>
              ariaLabel="Workload type"
              value={editing ? "all" : kind}
              onChange={(next) => !editing && setKind(next)}
              options={[
                { id: "all", label: "All" },
                { id: "system-container", label: "Containers" },
                { id: "vm", label: "VMs" },
                { id: "oci", label: "OCI" },
              ]}
            />
          </div>
          <div className="toolbar-group">
            {mutate && selectedItems.length > 0 && !editing ? (
              <button className="btn btn-danger" type="button" disabled={busy} onClick={() => setConfirmOpen(true)}>
                <Icon name="delete" size={14} />
                Delete selected ({selectedItems.length})
              </button>
            ) : null}
            <Segmented<"name" | "custom">
              ariaLabel="Order"
              value={sort === "custom" ? "custom" : "name"}
              onChange={(next) => changeSort(next)}
              options={[
                { id: "name", label: "A to Z" },
                { id: "custom", label: "Custom order" },
              ]}
            />
          </div>
        </div>
        {editing ? (
          <p className="banner" role="status">
            Drag rows by the handle, or focus a handle and use the arrow keys. Your order is saved to your profile. Hosts
            always stay on top.
          </p>
        ) : null}
        {loading ? (
          <LoadingState label="Loading workloads" />
        ) : filtered.length === 0 && hosts.length === 0 ? (
          <EmptyState icon="workloads" title={q || kind !== "all" ? "No matching workloads" : "No workloads yet"}>
            {q || kind !== "all"
              ? "Nothing matches that search."
              : mutate
                ? "Create a VM or system container when a usable storage pool and guest network are available."
                : "No workloads are visible yet. Creating them requires operator or admin."}
          </EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="wl-table">
              <thead>
                <tr>
                  {leadCol ? (
                    <th className="col-lead">
                      {editing ? (
                        <span className="visually-hidden">Reorder</span>
                      ) : (
                        <input
                          type="checkbox"
                          aria-label="Select all"
                          checked={allSelected}
                          disabled={selectable.length === 0}
                          onChange={(e) => setSelected(e.target.checked ? selectable.map((w) => w.id) : [])}
                        />
                      )}
                    </th>
                  ) : null}
                  <th aria-sort={sort === "name" ? "ascending" : sort === "name-desc" ? "descending" : undefined}>
                    <button
                      type="button"
                      className={"th-sort" + (sort !== "custom" ? " is-active" : "")}
                      disabled={editing}
                      onClick={() => changeSort(nameSort)}
                    >
                      Name
                      <Icon name={sort === "name" ? "sort-up" : sort === "name-desc" ? "sort-down" : "sort"} size={12} />
                    </button>
                  </th>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Image</th>
                  <th>IPv4</th>
                  <th className="num">CPU</th>
                  <th className="num">Memory</th>
                  <th className="col-tools">
                    <button
                      type="button"
                      className={"btn btn-sm btn-icon" + (editing ? " btn-primary" : " btn-ghost")}
                      aria-pressed={editing}
                      aria-label={editing ? "Done reordering" : "Edit order"}
                      title={editing ? "Done reordering" : "Edit order"}
                      onClick={() => (editing ? setEditing(false) : startEditing())}
                    >
                      <Icon name={editing ? "success" : "edit"} size={14} />
                    </button>
                  </th>
                </tr>
              </thead>
              <tbody>
                {hosts.map((node) => (
                  <tr key={`node:${node.id}`} className="is-pinned">
                    {leadCol ? (
                      <td className="col-lead">
                        <Icon name="lock" size={12} />
                      </td>
                    ) : null}
                    <td>
                      <Link href={`/nodes/${node.id}`}>{node.name || node.id}</Link>
                      <span className="host-tag">Host</span>
                    </td>
                    <td className="type-cell">Host</td>
                    <td>
                      <StatusBadge status={hostLive(node) ? "running" : node.status} label={hostLive(node) ? "Online" : undefined} />
                    </td>
                    <td>{node.host_os || "Not reported"}</td>
                    <td className="muted">Not shown</td>
                    <td className="num">{node.cpu_cores ? `${node.cpu_cores}` : ""}</td>
                    <td className="num">{node.memory_bytes ? formatBytes(node.memory_bytes) : ""}</td>
                    <td className="col-tools" />
                  </tr>
                ))}
                {filtered.map((w) => (
                  <tr
                    key={w.id}
                    className={[dragId === w.id ? "is-dragging" : "", overId === w.id && dragId !== w.id ? "is-drop-target" : ""]
                      .filter(Boolean)
                      .join(" ") || undefined}
                    draggable={editing}
                    onDragStart={(event) => {
                      if (!editing) {
                        return;
                      }
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData("text/plain", w.id);
                      setDragId(w.id);
                    }}
                    onDragOver={(event) => {
                      if (editing && dragId) {
                        event.preventDefault();
                        setOverId(w.id);
                      }
                    }}
                    onDragEnd={() => {
                      setDragId(null);
                      setOverId(null);
                    }}
                    onDrop={(event) => onDrop(event, w.id)}
                  >
                    {leadCol ? (
                      <td className="col-lead">
                        {editing ? (
                          <button
                            type="button"
                            className="drag-handle"
                            data-drag-handle={w.id}
                            aria-label={`Move ${w.name}`}
                            title="Drag to reorder"
                            onKeyDown={(event) => onHandleKey(event, w.id)}
                          >
                            <Icon name="grip" size={14} />
                          </button>
                        ) : isContainer(w) ? (
                          <input
                            type="checkbox"
                            aria-label={`Select ${w.name}`}
                            checked={selected.includes(w.id)}
                            onChange={() => toggle(w.id)}
                          />
                        ) : null}
                      </td>
                    ) : null}
                    <td>
                      <Link href={`/workloads/${w.id}`}>{w.name}</Link>
                    </td>
                    <td>
                      <span className="type-cell">
                        <Icon name="workloads" size={14} />
                        {kindLabel(w.kind)}
                      </span>
                    </td>
                    <td>
                      <StatusBadge status={w.status} />
                      {w.status === "warning" || w.status === "failed" ? (
                        <span className="cell-sub">{w.reason || ""}</span>
                      ) : null}
                    </td>
                    <td>{osLabel(w.image_pin)}</td>
                    <td className="cell-mono">{w.nics?.[0]?.ipv4 || "Not reported"}</td>
                    <td className="num">{w.cpus ? `${w.cpus}` : ""}</td>
                    <td className="num">{formatBytes(w.memory_bytes)}</td>
                    <td className="col-tools" />
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {!loading && filtered.length === 0 && hosts.length > 0 && (q || kind !== "all") ? (
          <EmptyState title="No matching workloads">Nothing matches that search.</EmptyState>
        ) : null}
      </div>
      <ConfirmDialog
        open={confirmOpen}
        title="Delete containers"
        confirmLabel="Delete"
        danger
        onClose={() => setConfirmOpen(false)}
        onConfirm={() => void onConfirmDelete()}
      >
        <p>
          Delete {selectedItems.length} container{selectedItems.length === 1 ? "" : "s"}? This cannot be undone.
        </p>
        <ul>
          {selectedItems.map((w) => (
            <li key={w.id}>{w.name}</li>
          ))}
        </ul>
      </ConfirmDialog>
    </section>
  );
}
