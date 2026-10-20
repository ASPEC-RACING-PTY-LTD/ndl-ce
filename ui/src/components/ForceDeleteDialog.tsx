import { useState } from "react";
import type { ServicePrincipalReference } from "../api/client";
import { Dialog } from "../ui/Dialog";
import { ErrorNotice } from "./ErrorNotice";

const KIND_LABELS: Record<string, string> = {
  token: "API tokens",
  group: "Groups",
  role: "Roles",
};

/**
 * ForceDeleteDialog opens when a normal delete is refused because the item
 * is still in use. It lists every association so the risk is visible, and
 * Force delete acts at once: no typed confirmation, no second dialog.
 */
export function ForceDeleteDialog({
  name,
  references,
  canForce,
  onForce,
  onClose,
}: {
  name: string;
  references: ServicePrincipalReference[];
  /** Only SuperAdmin and root may force delete. */
  canForce: boolean;
  onForce: () => Promise<void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const groups = new Map<string, ServicePrincipalReference[]>();
  for (const ref of references) {
    groups.set(ref.kind, [...(groups.get(ref.kind) ?? []), ref]);
  }
  return (
    <Dialog
      open
      title={`${name} is still in use`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          {canForce ? (
            <button
              className="btn btn-danger"
              type="button"
              disabled={busy}
              onClick={() => {
                setBusy(true);
                setError(null);
                void onForce()
                  .catch((err) => setError(err instanceof Error ? err.message : "Force delete failed"))
                  .finally(() => setBusy(false));
              }}
            >
              {busy ? "Deleting" : "Force delete"}
            </button>
          ) : null}
        </div>
      }
    >
      <p className="field-hint">
        {canForce
          ? "Force delete removes it and every association below. Its audit history is kept."
          : "Remove these associations first, or ask a SuperAdmin to force delete it."}
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="force-refs">
        {[...groups.entries()].map(([kind, refs]) => (
          <section key={kind} className="force-refs-group">
            <h3 className="iam-subhead">
              {KIND_LABELS[kind] ?? kind} <span className="meta">{refs.length}</span>
            </h3>
            <ul>
              {refs.map((ref) => (
                <li key={`${kind}:${ref.id}`}>
                  {ref.label}
                  {ref.blocking ? null : <span className="meta"> · removed with it</span>}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
