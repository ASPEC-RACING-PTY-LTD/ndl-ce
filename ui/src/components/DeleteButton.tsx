import { useState, type ReactNode } from "react";
import { deleteResource } from "../api/client";
import { ConfirmDialog } from "./ConfirmDialog";
import { Icon } from "./Icon";
import { ErrorNotice } from "./ErrorNotice";

/**
 * Confirms and deletes one resource. Dependency refusals from the server
 * (for example "2 volumes are still on this pool") are shown in the dialog.
 */
export function DeleteButton({
  path,
  name,
  noun,
  label = "Delete",
  description,
  onDeleted,
  small = true,
  confirmLabel,
  confirmIfName,
}: {
  path: string;
  name: string;
  noun: string;
  label?: string;
  description?: ReactNode;
  onDeleted: () => void | Promise<void>;
  small?: boolean;
  confirmLabel?: string;
  /** Asks for the interface name, sent as confirm_ifname, when the server needs it. */
  confirmIfName?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ifname, setIfname] = useState("");

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await deleteResource(path, confirmIfName && ifname.trim() ? { confirm_ifname: ifname.trim() } : undefined);
      setOpen(false);
      await onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button
        type="button"
        className={"btn btn-ghost btn-danger-text" + (small ? " btn-sm" : "")}
        aria-label={`${label} ${noun} ${name}`}
        title={`${label} ${noun}`}
        onClick={() => {
          setError(null);
          setOpen(true);
        }}
      >
        <Icon name="delete" size={14} />
        {small ? null : label}
      </button>
      <ConfirmDialog
        open={open}
        title={`${label} ${noun} ${name}?`}
        confirmLabel={busy ? "Working" : confirmLabel ?? label}
        danger
        onClose={() => setOpen(false)}
        confirmDisabled={busy}
        onConfirm={() => void confirm()}
      >
        {description ? <div className="stack">{description}</div> : <p>This cannot be undone.</p>}
        {confirmIfName ? (
          <label className="field" htmlFor={`delete-ifname-${name}`}>
            <span className="field-label">Interface confirmation (only needed on the management path)</span>
            <input
              id={`delete-ifname-${name}`}
              className="field-input"
              value={ifname}
              onChange={(e) => setIfname(e.target.value)}
              autoComplete="off"
            />
          </label>
        ) : null}
        {error ? (
          <ErrorNotice error={error} />
        ) : null}
      </ConfirmDialog>
    </>
  );
}
