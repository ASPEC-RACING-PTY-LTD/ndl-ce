import { useEffect, useMemo, useState } from "react";
import {
  getWorkload,
  getWorkloadStorage,
  resizeWorkloadMount,
  setWorkloadMounts,
  workloadAction,
} from "../api/client";
import type { Workload } from "../api/phase5";
import type {
  StoragePool,
  WorkloadMount,
  WorkloadMountInput,
  WorkloadStorage,
} from "../generated/openapi";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { formatBytes } from "../format";
import { canOnItem } from "../rbac";
import { currentPath } from "../router";
import { useSession } from "../session";
import { ErrorNotice } from "../components/ErrorNotice";

const GIB = 1024 ** 3;

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

function slug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

// keepMount sends an existing mount back unchanged.
function keepMount(m: WorkloadMount): WorkloadMountInput {
  return {
    source: m.source,
    target: m.target,
    read_only: m.read_only,
    pool_id: m.pool_id,
    label: m.label,
  };
}

// defaultSizeGiB is the pool's free space in whole GiB.
function defaultSizeGiB(p: StoragePool | undefined): string {
  if (p?.usable_bytes == null || p.usable_bytes <= 0) {
    return "";
  }
  return String(Math.max(1, Math.floor(p.usable_bytes / GIB)));
}

function poolLabel(p: StoragePool): string {
  const free =
    p.usable_bytes != null
      ? `${formatBytes(p.usable_bytes)} free`
      : "free space not reported";
  return `${p.name} (${p.backend_type}, ${free})`;
}

export function WorkloadStoragePage() {
  const session = useSession();
  const id = workloadIDFromPath();
  const mutate = canOnItem(session.status === "ready" ? session.user : null, "workload", id, "compute.modify");

  const [item, setItem] = useState<Workload | null>(null);
  const [data, setData] = useState<WorkloadStorage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [restartNeeded, setRestartNeeded] = useState(false);
  const [confirmRestart, setConfirmRestart] = useState(false);
  const [removing, setRemoving] = useState<WorkloadMount | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<WorkloadMount | null>(null);
  const [editLabel, setEditLabel] = useState("");
  const [editTarget, setEditTarget] = useState("");
  const [editReadOnly, setEditReadOnly] = useState(false);
  const [editSize, setEditSize] = useState("");

  const [sourceKind, setSourceKind] = useState<"pool" | "host">("pool");
  const [poolID, setPoolID] = useState("");
  const [sizeGiB, setSizeGiB] = useState("");
  const [hostPath, setHostPath] = useState("");
  const [label, setLabel] = useState("");
  const [target, setTarget] = useState("");
  const [readOnly, setReadOnly] = useState(false);

  const pools = useMemo(() => data?.pools ?? [], [data]);
  const pool = pools.find((p) => p.id === poolID);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [w, s] = await Promise.all([
          getWorkload(id),
          getWorkloadStorage(id),
        ]);
        if (cancelled) {
          return;
        }
        setItem(w);
        setData(s);
        if (s.pools.length > 0) {
          setPoolID(s.pools[0].id);
          setSizeGiB(defaultSizeGiB(s.pools[0]));
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  async function save(next: WorkloadMountInput[], done: string) {
    setBusy(true);
    setFormError(null);
    setNotice(null);
    try {
      const res = await setWorkloadMounts(id, next);
      setData(res);
      setNotice(done);
      if (res.restart_required) {
        setRestartNeeded(true);
      }
      return true;
    } catch (err) {
      setFormError(
        err instanceof Error ? err.message : "Saving storage failed",
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function onAdd(event: React.FormEvent) {
    event.preventDefault();
    if (!data) {
      return;
    }
    const name = label.trim();
    const mountAt = target.trim() || `/mnt/${slug(name) || "data"}`;
    const input: WorkloadMountInput = {
      target: mountAt,
      read_only: readOnly,
      label: name,
    };
    if (sourceKind === "pool") {
      const gib = Number(sizeGiB);
      if (!poolID) {
        setFormError("Pick a storage pool.");
        return;
      }
      if (!Number.isFinite(gib) || gib < 1) {
        setFormError("Enter a size of at least 1 GiB.");
        return;
      }
      input.pool_id = poolID;
      input.size_bytes = Math.floor(gib) * GIB;
    } else {
      if (!hostPath.trim().startsWith("/")) {
        setFormError(
          "Enter the absolute host folder, for example /mnt/hdd1/media.",
        );
        return;
      }
      input.source = hostPath.trim();
    }
    const ok = await save(
      [...data.mounts.map(keepMount), input],
      `Added ${mountAt}.`,
    );
    if (ok) {
      setLabel("");
      setTarget("");
      setHostPath("");
      setReadOnly(false);
    }
  }

  function onRemove() {
    const m = removing;
    setRemoving(null);
    if (!m || !data) {
      return;
    }
    void save(
      data.mounts.filter((x) => x.target !== m.target).map(keepMount),
      `Removed the mount at ${m.target}. The folder and its files are still on the host.`,
    );
  }

  function openEdit(m: WorkloadMount) {
    setEditing(m);
    setEditLabel(m.label ?? "");
    setEditTarget(m.target);
    setEditReadOnly(m.read_only);
    setEditSize(
      m.size_bytes != null ? String(Math.round(m.size_bytes / GIB)) : "",
    );
  }

  async function onEdit() {
    const m = editing;
    if (!m || !data) {
      return;
    }
    const mountAt = editTarget.trim();
    if (!mountAt.startsWith("/")) {
      setFormError(
        "Enter the absolute path inside the container, for example /mnt/data.",
      );
      return;
    }
    let sizeBytes: number | null = null;
    if (m.volume_id && m.size_bytes != null) {
      const gib = Number(editSize);
      if (!Number.isFinite(gib) || gib < 1) {
        setFormError("Enter a size of at least 1 GiB.");
        return;
      }
      const next = Math.floor(gib) * GIB;
      if (next !== Math.round(m.size_bytes / GIB) * GIB) {
        sizeBytes = next;
      }
    }
    const listChanged =
      mountAt !== m.target ||
      editReadOnly !== m.read_only ||
      editLabel.trim() !== (m.label ?? "");
    setEditing(null);
    if (sizeBytes != null) {
      setBusy(true);
      setFormError(null);
      setNotice(null);
      try {
        setData(await resizeWorkloadMount(id, m.target, sizeBytes));
        setNotice(`Resized ${m.target} to ${formatBytes(sizeBytes)}.`);
      } catch (err) {
        setFormError(err instanceof Error ? err.message : "Resizing failed");
        setBusy(false);
        return;
      }
      setBusy(false);
    }
    if (listChanged) {
      await save(
        data.mounts.map((x) =>
          x.target === m.target
            ? {
                ...keepMount(x),
                target: mountAt,
                read_only: editReadOnly,
                label: editLabel.trim(),
              }
            : keepMount(x),
        ),
        `Saved ${mountAt}.`,
      );
    }
  }

  function onRestart() {
    setConfirmRestart(false);
    setBusy(true);
    void workloadAction(id, "restart")
      .then(async () => {
        setRestartNeeded(false);
        setNotice("Restarted. The container now has its storage mounts.");
        setItem(await getWorkload(id));
      })
      .catch((err) =>
        setFormError(err instanceof Error ? err.message : "Restart failed"),
      )
      .finally(() => setBusy(false));
  }

  if (error) {
    return (
      <section className="page">
        <ErrorState>Storage unavailable: {error}</ErrorState>
      </section>
    );
  }
  if (!data || !item) {
    return (
      <section className="page">
        <LoadingState label="Loading storage" />
      </section>
    );
  }

  const running = item.status === "running" || Boolean(item.unit_active);

  return (
    <section className="page page-wide" aria-labelledby="storage-heading">
      <PageHeader
        id="storage-heading"
        title="Storage"
        kicker={`Folders mounted into ${item.name}. Changes edit the container's mount list only: nothing is deleted, formatted or restarted.`}
      />

      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {formError ? <ErrorNotice error={formError} /> : null}
      {restartNeeded && running ? (
        <div className="banner banner-warn" role="status">
          <p>
            The change loads the next time {item.name} restarts. It keeps
            running until then.
          </p>
          {mutate ? (
            <div className="btn-row is-flush">
              <button
                className="btn"
                type="button"
                disabled={busy}
                onClick={() => setConfirmRestart(true)}
              >
                Restart {item.name}
              </button>
            </div>
          ) : null}
        </div>
      ) : null}

      <article className="panel">
        <h2>Mounted storage</h2>
        {data.mounts.length === 0 ? (
          <p>
            No extra storage is mounted. The container only has its root disk.
          </p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Container path</th>
                  <th>Host folder</th>
                  <th>Pool</th>
                  <th>Size</th>
                  <th>Access</th>
                  {mutate ? <th aria-label="Actions" /> : null}
                </tr>
              </thead>
              <tbody>
                {data.mounts.map((m) => (
                  <tr key={m.target}>
                    <td>{m.label || "Unnamed"}</td>
                    <td>
                      <code>{m.target}</code>
                    </td>
                    <td>
                      <code>{m.source}</code>
                    </td>
                    <td>
                      {m.pool_name || (m.pool_id ? "Pool" : "Host folder")}
                    </td>
                    <td>
                      {m.size_bytes != null ? formatBytes(m.size_bytes) : "Host disk"}
                    </td>
                    <td>{m.read_only ? "Read-only" : "Read and write"}</td>
                    {mutate ? (
                      <td className="btn-row is-flush">
                        <button
                          className="btn"
                          type="button"
                          disabled={busy}
                          aria-label={`Edit ${m.target}`}
                          onClick={() => openEdit(m)}
                        >
                          Edit
                        </button>
                        <button
                          className="btn"
                          type="button"
                          disabled={busy}
                          aria-label={`Unmount ${m.target}`}
                          onClick={() => setRemoving(m)}
                        >
                          Unmount
                        </button>
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>

      {mutate ? (
        <article className="panel">
          <h2>Add storage</h2>
          <form className="form" onSubmit={(e) => void onAdd(e)}>
            <fieldset>
              <legend>Source</legend>
              <label>
                <input
                  type="radio"
                  name="storage-source"
                  checked={sourceKind === "pool"}
                  onChange={() => setSourceKind("pool")}
                />{" "}
                New folder on a storage pool
              </label>
              <label>
                <input
                  type="radio"
                  name="storage-source"
                  checked={sourceKind === "host"}
                  onChange={() => setSourceKind("host")}
                />{" "}
                Existing folder on the host
              </label>
            </fieldset>

            {sourceKind === "pool" ? (
              pools.length === 0 ? (
                <p className="banner banner-warn" role="status">
                  No directory, ZFS, NFS or SMB pool is available. Add a pool
                  for the disk under Storage, then come back.
                </p>
              ) : (
                <>
                  <label htmlFor="storage-pool">Storage pool</label>
                  <select
                    id="storage-pool"
                    value={poolID}
                    onChange={(e) => {
                      setPoolID(e.target.value);
                      setSizeGiB(
                        defaultSizeGiB(
                          pools.find((p) => p.id === e.target.value),
                        ),
                      );
                    }}
                  >
                    {pools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {poolLabel(p)}
                      </option>
                    ))}
                  </select>
                  {pool?.warning_text?.length ? (
                    <p className="field-hint">{pool.warning_text.join(" ")}</p>
                  ) : null}
                  <label htmlFor="storage-size">Size (GiB)</label>
                  <input
                    id="storage-size"
                    inputMode="numeric"
                    value={sizeGiB}
                    onChange={(e) => setSizeGiB(e.target.value)}
                  />
                  <p className="field-hint">
                    A new, empty folder is created on the pool and given to the
                    container's root user.
                    {pool?.backend_type === "zfs"
                      ? " On ZFS it is its own dataset and the size is its quota."
                      : ""}
                  </p>
                </>
              )
            ) : (
              <>
                <label htmlFor="storage-host-path">Host folder</label>
                <input
                  id="storage-host-path"
                  placeholder="/mnt/hdd1/media"
                  value={hostPath}
                  onChange={(e) => setHostPath(e.target.value)}
                />
                <p className="field-hint">
                  The folder must already exist. Its files and ownership are
                  left exactly as they are. For the container to write to it,
                  files need to be owned by host UID {data.mapped_root_uid},
                  which is root inside the container.
                </p>
              </>
            )}

            <label htmlFor="storage-label">Name</label>
            <input
              id="storage-label"
              placeholder="Media"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
            <label htmlFor="storage-target">Path inside the container</label>
            <input
              id="storage-target"
              placeholder={`/mnt/${slug(label) || "data"}`}
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            />
            <label>
              <input
                type="checkbox"
                checked={readOnly}
                onChange={(e) => setReadOnly(e.target.checked)}
              />{" "}
              Read-only
            </label>
            <div className="btn-row is-flush">
              <button
                className="btn btn-primary"
                type="submit"
                disabled={busy || (sourceKind === "pool" && pools.length === 0)}
              >
                Add storage
              </button>
            </div>
          </form>
        </article>
      ) : null}

      <ConfirmDialog
        open={editing != null}
        title="Edit storage"
        confirmLabel="Save"
        confirmDisabled={busy}
        onConfirm={() => void onEdit()}
        onClose={() => setEditing(null)}
      >
        <div className="form">
          <label htmlFor="edit-storage-label">Name</label>
          <input
            id="edit-storage-label"
            value={editLabel}
            onChange={(e) => setEditLabel(e.target.value)}
          />
          <label htmlFor="edit-storage-target">Path inside the container</label>
          <input
            id="edit-storage-target"
            value={editTarget}
            onChange={(e) => setEditTarget(e.target.value)}
          />
          <label>
            <input
              type="checkbox"
              checked={editReadOnly}
              onChange={(e) => setEditReadOnly(e.target.checked)}
            />{" "}
            Read-only
          </label>
          {editing?.volume_id ? (
            <>
              <label htmlFor="edit-storage-size">Size (GiB)</label>
              <input
                id="edit-storage-size"
                inputMode="numeric"
                value={editSize}
                onChange={(e) => setEditSize(e.target.value)}
              />
              <p className="field-hint">
                Growing takes effect straight away. Shrinking is refused if the
                files would not fit; on a directory pool the container must be
                stopped to shrink.
              </p>
            </>
          ) : (
            <p className="field-hint">
              This is a host folder, so its size is whatever the host disk has.
            </p>
          )}
          <p className="field-hint">
            Changing the path or access loads on the next restart. Files are
            never moved or deleted.
          </p>
        </div>
      </ConfirmDialog>

      <ConfirmDialog
        open={removing != null}
        title="Unmount storage"
        confirmLabel="Unmount"
        onConfirm={onRemove}
        onClose={() => setRemoving(null)}
      >
        <p>
          Unmount <code>{removing?.target}</code> from {item.name}? The host
          folder <code>{removing?.source}</code> and every file in it stay on
          the host. Nothing is deleted.
        </p>
      </ConfirmDialog>

      <ConfirmDialog
        open={confirmRestart}
        title={`Restart ${item.name}`}
        confirmLabel="Restart"
        onConfirm={onRestart}
        onClose={() => setConfirmRestart(false)}
      >
        <p>
          Restart {item.name} now to load its storage mounts? Services inside it
          stop briefly while it restarts. Its data is not affected.
        </p>
      </ConfirmDialog>
    </section>
  );
}
