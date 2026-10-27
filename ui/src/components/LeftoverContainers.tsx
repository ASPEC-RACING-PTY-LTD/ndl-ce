import { useCallback, useEffect, useState } from "react";
import { listCTLeftovers, removeCTLeftovers, type CTLeftover } from "../api/client";
import { confirmAction } from "../confirm";
import { formatBytes, formatWhen } from "../format";
import { Dialog } from "../ui/Dialog";
import { ErrorNotice } from "./ErrorNotice";
import { LoadingState } from "./EmptyState";
import { ResourceTable } from "./ResourceTable";

/**
 * LeftoverContainers warns about system containers this node still has
 * state for (an enabled unit, LXC config, a root filesystem) but no
 * workload owns. They fail at every boot and hold disk space; removing
 * them is a choice, because it deletes their files.
 */
export function LeftoverContainers({ canRemove }: { canRemove: boolean }) {
  const [items, setItems] = useState<CTLeftover[] | null>(null);
  const [open, setOpen] = useState(false);
  const [sized, setSized] = useState<CTLeftover[] | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setItems((await listCTLeftovers(false)).items);
    } catch {
      // Older agents cannot list container state; say nothing.
      setItems([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!open) {
      return;
    }
    setSized(null);
    listCTLeftovers(true)
      .then((res) => {
        setSized(res.items);
        setSelected(res.items.map((i) => i.id));
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Leftover containers could not be listed"));
  }, [open]);

  async function remove() {
    if (!confirmAction(`Remove ${selected.length} leftover container(s)? Their units, settings and root filesystems are deleted. This cannot be undone.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await removeCTLeftovers(selected);
      const kept = res.results.filter((r) => r.kept);
      setNote(
        `Removed ${res.results.length} leftover container(s).` +
          (kept.length ? ` Kept the files of ${kept.length}: ${kept.map((k) => `${k.id.slice(0, 8)} (${k.kept_reason})`).join("; ")}.` : ""),
      );
      setOpen(false);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Removing failed");
    } finally {
      setBusy(false);
    }
  }

  if (!items || (items.length === 0 && !note)) {
    return null;
  }
  return (
    <>
      {note ? (
        <p className="banner banner-ok" role="status">
          {note}
        </p>
      ) : null}
      {items.length > 0 ? (
        <div className="banner banner-warn" role="status">
          <p>
            <strong>{items.length} leftover container(s) on this node.</strong> They belong to containers that were deleted, are still set to start at boot, fail every time and use
            disk space.
          </p>
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setOpen(true)}>
            Review
          </button>
        </div>
      ) : null}
      <Dialog
        open={open}
        wide
        title="Leftover containers"
        onClose={() => setOpen(false)}
        footer={
          <div className="btn-row">
            {canRemove ? (
              <button className="btn btn-danger" type="button" disabled={busy || selected.length === 0 || !sized} onClick={() => void remove()}>
                Remove {selected.length} selected
              </button>
            ) : null}
            <button className="btn btn-ghost" type="button" onClick={() => setOpen(false)}>
              Close
            </button>
          </div>
        }
      >
        <p className="muted">
          No workload owns these. Removing one stops and disables its unit and deletes its settings and root filesystem. Files another container uses are kept.
        </p>
        {error ? <ErrorNotice error={error} /> : null}
        {!sized && !error ? <LoadingState label="Measuring their files" /> : null}
        {sized ? (
          <ResourceTable
            headers={[
              <span key="sel" className="visually-hidden">
                Select
              </span>,
              "Name",
              "ID",
              "Starts at boot",
              "Root filesystem",
              "Last changed",
            ]}
            numeric={[4]}
            rows={sized.map((l) => [
              <input
                key={`sel-${l.id}`}
                type="checkbox"
                aria-label={`Select ${l.name || l.id}`}
                checked={selected.includes(l.id)}
                onChange={() => setSelected((cur) => (cur.includes(l.id) ? cur.filter((x) => x !== l.id) : [...cur, l.id]))}
              />,
              l.name || "unknown",
              <code key={`id-${l.id}`}>{l.id.slice(0, 8)}</code>,
              l.unit_enabled ? (l.unit_active ? "yes, running" : "yes, failing") : "no",
              l.rootfs ? formatBytes(l.rootfs_bytes ?? 0) : "none",
              formatWhen(l.modified),
            ])}
          />
        ) : null}
      </Dialog>
    </>
  );
}
