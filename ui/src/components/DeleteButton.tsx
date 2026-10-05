import { useState, type ReactNode } from "react";
import { deleteResource } from "../api/client";
import { ConfirmDialog } from "./ConfirmDialog";
import { Icon } from "./Icon";

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
}: {
  path: string;
  name: string;
  noun: string;
  label?: string;
  description?: ReactNode;
  onDeleted: () => void | Promise<void>;
  small?: boolean;
  confirmLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await deleteResource(path);
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
        {error ? (
          <p className="banner banner-error" role="alert">
            {error}
          </p>
        ) : null}
      </ConfirmDialog>
    </>
  );
}
