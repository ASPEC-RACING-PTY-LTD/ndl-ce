import { useState } from "react";
import { actOnCodeholdPending, getCodeholdPending } from "../api/client";
import { formatWhen } from "../format";
import { usePref } from "../prefs";
import { invalidateQueries, useQuery } from "../query";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

/**
 * CodeholdPendingBanner warns on the dashboard while force-disabled CodeHold
 * runner containers wait out their grace period. It can be dismissed; a new
 * forced turn-off shows it again.
 */
export function CodeholdPendingBanner() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canRead = hasGrant(user, "compute.read");
  const canManage = hasGrant(user, "feature.manage");
  const q = useQuery(`codehold-pending:${canRead}`, () => (canRead ? getCodeholdPending() : Promise.resolve({ items: [] })), 60000);
  const [dismissed, setDismissed] = usePref<string>("codehold.pending.dismissed", "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const items = q.data?.items ?? [];
  if (items.length === 0) {
    return null;
  }
  const first = items
    .map((r) => r.delete_after ?? "")
    .filter(Boolean)
    .sort()[0];
  // Dismissal is remembered for this set of deletions.
  const key = `${first}|${items.length}`;
  if (dismissed === key) {
    return null;
  }
  async function act(action: "keep" | "delete") {
    if (action === "delete" && !window.confirm(`Delete ${items.length} runner container(s) now? This cannot be undone.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await actOnCodeholdPending(action);
      invalidateQueries("codehold-pending");
      await q.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="banner banner-warn" role="alert">
      <p>
        <strong>
          {items.length} CodeHold runner container{items.length === 1 ? "" : "s"} will be deleted {first ? formatWhen(first) : "soon"}
        </strong>
      </p>
      <p className="meta">
        CodeHold runners was force turned off, so {items.map((r) => r.workload_name || r.name).join(", ")} {items.length === 1 ? "was" : "were"} stopped
        and {items.length === 1 ? "is" : "are"} kept for 7 days.
      </p>
      {error ? <p className="tunnel-reason">{error}</p> : null}
      <div className="btn-row">
        {canManage ? (
          <>
            <button type="button" className="btn btn-sm btn-secondary" disabled={busy} onClick={() => void act("keep")}>
              Keep them
            </button>
            <button type="button" className="btn btn-sm btn-ghost btn-danger-text" disabled={busy} onClick={() => void act("delete")}>
              Delete now
            </button>
          </>
        ) : null}
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => setDismissed(key)}>
          Dismiss
        </button>
      </div>
    </div>
  );
}
