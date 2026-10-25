import { useState } from "react";
import { ApiError, decideBackupApproval, type AppNotification } from "../../api/client";
import { Icon } from "../Icon";

/** isApprovalAsk reports a notification that still waits for approve or deny. */
export function isApprovalAsk(n: AppNotification): boolean {
  return n.kind === "backup.approval" && n.approval === "pending" && !!n.subject_id;
}

/**
 * ApprovalButtons answers a backup approval notification in place: a green
 * tick approves the new workload for All workloads backup policies, a red
 * cross leaves it out of them. A decided one shows its outcome instead.
 */
export function ApprovalButtons({ n, onDecided }: { n: AppNotification; onDecided: (id: string, state: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (n.kind !== "backup.approval") {
    return null;
  }
  if (!isApprovalAsk(n)) {
    return n.approval ? <span className={"approval-state is-" + n.approval}>{n.approval === "approved" ? "Approved" : "Denied"}</span> : null;
  }
  async function decide(decision: "approve" | "deny") {
    setBusy(true);
    setError(null);
    try {
      const res = await decideBackupApproval(n.subject_id!, decision);
      onDecided(n.id, res.state);
    } catch (err) {
      const state = err instanceof ApiError && typeof err.body?.state === "string" ? err.body.state : null;
      if (state) {
        onDecided(n.id, state);
      } else {
        setError(err instanceof ApiError ? err.message : "Could not save");
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <span className="approval-buttons">
      <button
        type="button"
        className="approval-btn is-approve"
        aria-label="Approve backup"
        title="Approve: back it up with All workloads policies"
        disabled={busy}
        onClick={() => void decide("approve")}
      >
        <Icon name="check" size={14} />
      </button>
      <button
        type="button"
        className="approval-btn is-deny"
        aria-label="Deny backup"
        title="Deny: leave it out of All workloads policies"
        disabled={busy}
        onClick={() => void decide("deny")}
      >
        <Icon name="x" size={14} />
      </button>
      {error ? (
        <span className="approval-error" role="alert">
          {error}
        </span>
      ) : null}
    </span>
  );
}
