import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ApiError,
  decideApproval,
  getApproval,
  getIAMAccess,
  getIAMSettings,
  listApprovals,
  saveApprovalsAccess,
  type Approval,
  type ApprovalDetail,
  type ApprovalList,
  type ApprovalStatus,
  type IAMRole,
} from "../api/client";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { formatWhen } from "../format";
import { invalidateQueries } from "../query";

const STATUS_LABEL: Record<ApprovalStatus, string> = {
  pending: "Waiting for approval",
  approved: "Approved",
  denied: "Denied",
  superseded: "Replaced",
};

const FILTERS: { value: ApprovalStatus | ""; label: string }[] = [
  { value: "pending", label: "Waiting" },
  { value: "approved", label: "Approved" },
  { value: "denied", label: "Denied" },
  { value: "", label: "All" },
];

type FlowNode = { id: string; type?: string; name?: string; config?: Record<string, unknown> };
type FlowDef = { trigger?: { type?: string; config?: Record<string, unknown> }; nodes?: FlowNode[]; edges?: unknown[] };

export type StepDiff = { id: string; label: string; change: "added" | "removed" | "changed" | "same"; before: string; after: string };

function show(v: unknown): string {
  return v === undefined ? "" : JSON.stringify(v, null, 2);
}

/** stepDiff compares two flow definitions step by step (trigger, each step, the links). */
export function stepDiff(base: unknown, next: unknown): StepDiff[] {
  const a = (base ?? {}) as FlowDef;
  const b = (next ?? {}) as FlowDef;
  const out: StepDiff[] = [];
  const row = (id: string, label: string, before: unknown, after: unknown) => {
    const x = show(before);
    const y = show(after);
    const change = !x ? "added" : !y ? "removed" : x === y ? "same" : "changed";
    out.push({ id, label, change, before: x, after: y });
  };
  row("trigger", "Trigger", a.trigger, b.trigger);
  const left = new Map((a.nodes ?? []).map((n) => [n.id, n]));
  const right = new Map((b.nodes ?? []).map((n) => [n.id, n]));
  const ids = [...new Set([...(b.nodes ?? []).map((n) => n.id), ...(a.nodes ?? []).map((n) => n.id)])];
  for (const id of ids) {
    const n = right.get(id) ?? left.get(id)!;
    row(id, `${n.name || id} (${n.type ?? "step"})`, left.get(id), right.get(id));
  }
  row("edges", "Links between steps", a.edges, b.edges);
  return out;
}

/**
 * ApprovalsPage lists flow versions that hold protected steps, shows each
 * against the version running now, and lets deciders approve or deny it.
 * SuperAdmins also choose here which roles may see and decide approvals.
 */
export function ApprovalsPage() {
  const [filter, setFilter] = useState<ApprovalStatus | "">("pending");
  const [list, setList] = useState<ApprovalList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<{ flow: string; version: number } | null>(() => {
    const q = new URLSearchParams(window.location.search);
    const flow = q.get("flow");
    const version = Number(q.get("version"));
    return flow && version > 0 ? { flow, version } : null;
  });

  const load = useCallback(async () => {
    try {
      setList(await listApprovals(filter || undefined));
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Approvals are unavailable");
    }
  }, [filter]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="iam-section approvals-page">
      <PageHeader
        id="approvals-heading"
        title="Approvals"
        icon="check"
        kicker="Automation flow versions with steps that are hard to undo wait here until someone allowed to decide approves them. The version running now keeps running meanwhile."
      />
      {error ? <ErrorNotice error={error} /> : null}
      <div className="btn-row" role="tablist" aria-label="Filter approvals">
        {FILTERS.map((f) => (
          <button
            key={f.label}
            type="button"
            role="tab"
            aria-selected={filter === f.value}
            className={"btn btn-sm " + (filter === f.value ? "btn-primary" : "btn-ghost")}
            onClick={() => setFilter(f.value)}
          >
            {f.label}
            {f.value === "pending" && list && list.pending > 0 ? ` (${list.pending})` : ""}
          </button>
        ))}
      </div>
      {!list && !error ? <LoadingState label="Loading approvals" /> : null}
      {list && list.items.length === 0 ? <p className="muted">{filter === "pending" ? "Nothing is waiting for approval." : "No requests."}</p> : null}
      {list && list.items.length > 0 ? (
        <table className="bk-table approvals-table" aria-label="Approval requests">
          <thead>
            <tr>
              <th>Flow</th>
              <th>Version</th>
              <th>Requested by</th>
              <th>Protected steps</th>
              <th>Status</th>
              <th>When</th>
            </tr>
          </thead>
          <tbody>
            {list.items.map((a) => (
              <tr key={`${a.flow_id}#${a.version}`}>
                <td>
                  <button className="linkish" type="button" onClick={() => setOpen({ flow: a.flow_id, version: a.version })}>
                    {a.flow_name || a.flow_id}
                  </button>
                </td>
                <td>v{a.version}</td>
                <td>{a.requested_by}</td>
                <td>{a.protected_steps.map((p) => p.name).join(", ")}</td>
                <td>
                  <span className={`approval-status is-${a.status}`}>{STATUS_LABEL[a.status]}</span>
                </td>
                <td className="muted">{formatWhen(a.decided_at || a.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {open ? (
        <ApprovalDetailView
          key={`${open.flow}#${open.version}`}
          flow={open.flow}
          version={open.version}
          onClose={() => setOpen(null)}
          onDecided={async () => {
            invalidateQueries("approvals-pending");
            await load();
          }}
        />
      ) : null}
      {list?.can_manage_access ? <ApprovalsAccess /> : null}
    </div>
  );
}

function ApprovalDetailView({ flow, version, onClose, onDecided }: { flow: string; version: number; onClose: () => void; onDecided: () => Promise<void> }) {
  const [detail, setDetail] = useState<ApprovalDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [showSame, setShowSame] = useState(false);

  useEffect(() => {
    getApproval(flow, version)
      .then(setDetail)
      .catch((err) => setError(err instanceof ApiError ? err.message : "That request is unavailable"));
  }, [flow, version]);

  const rows = useMemo(() => (detail ? stepDiff(detail.base_definition, detail.definition) : []), [detail]);
  const protectedIDs = new Set(detail?.protected_steps.map((p) => p.node_id));

  async function decide(approve: boolean) {
    setBusy(true);
    setError(null);
    try {
      const updated: Approval = await decideApproval(flow, version, approve, note.trim());
      setDetail((cur) => (cur ? { ...cur, ...updated, can_decide: false } : cur));
      await onDecided();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "The decision was not saved");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="approval-detail" aria-label="Approval request">
      <div className="approval-detail-head">
        <h3>
          {detail?.flow_name || flow} v{version}
        </h3>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onClose}>
          Close
        </button>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {!detail && !error ? <LoadingState label="Loading the request" /> : null}
      {detail ? (
        <>
          <p className="field-hint">
            {STATUS_LABEL[detail.status]}. Requested by {detail.requested_by} {formatWhen(detail.created_at)}.
            {detail.decided_by ? ` Decided by ${detail.decided_by} ${formatWhen(detail.decided_at ?? undefined)}.` : ""}
            {detail.decision_note ? ` Note: ${detail.decision_note}` : ""} Version hash <code>{detail.hash.slice(0, 12)}</code>.{" "}
            <Link href={`/automation/flows/${flow}`}>Open the flow</Link>
          </p>
          <div className="banner banner-warn">
            <strong>Protected steps</strong>
            <ul>
              {detail.protected_steps.map((p) => (
                <li key={p.node_id}>
                  {p.name}: {p.reason}
                </li>
              ))}
            </ul>
          </div>
          <div className="approval-diff-head">
            <span>{detail.base_version > 0 ? `Approved v${detail.base_version}` : "No approved version yet"}</span>
            <span>Requested v{detail.version}</span>
            <label className="check-row">
              <input type="checkbox" checked={showSame} onChange={(e) => setShowSame(e.target.checked)} />
              <span>Show unchanged steps</span>
            </label>
          </div>
          <div className="approval-diff">
            {rows
              .filter((r) => showSame || r.change !== "same" || protectedIDs.has(r.id))
              .map((r) => (
                <div key={r.id} className={`approval-diff-row is-${r.change}` + (protectedIDs.has(r.id) ? " is-protected" : "")}>
                  <div className="approval-diff-label">
                    {r.label}
                    <span className="meta"> {r.change === "same" ? "unchanged" : r.change}</span>
                    {protectedIDs.has(r.id) ? <span className="approval-tag">protected</span> : null}
                  </div>
                  <pre className="approval-diff-side">{r.before || "(none)"}</pre>
                  <pre className="approval-diff-side">{r.after || "(none)"}</pre>
                </div>
              ))}
          </div>
          {detail.status === "pending" && detail.can_decide ? (
            <div className="approval-decide">
              <label className="field">
                <span className="field-label">Note (optional, sent to the requester)</span>
                <input className="field-input" value={note} onChange={(e) => setNote(e.target.value)} />
              </label>
              <div className="btn-row">
                <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void decide(true)}>
                  Approve v{detail.version}
                </button>
                <button className="btn btn-danger" type="button" disabled={busy} onClick={() => void decide(false)}>
                  Deny
                </button>
              </div>
              <p className="field-hint">
                Approve runs exactly this version from now on. Deny keeps {detail.running_version > 0 ? `v${detail.running_version}` : "the flow as a draft that cannot run"}.
              </p>
            </div>
          ) : null}
          {detail.status === "pending" && !detail.can_decide ? <p className="muted">You can view this request but not decide it.</p> : null}
        </>
      ) : null}
    </section>
  );
}

const ACCESS_PERMS = [
  { perm: "approvals.view", label: "See approvals" },
  { perm: "approvals.decide", label: "Approve and deny" },
];

/** ApprovalsAccess lets SuperAdmins give other roles approvals.view / approvals.decide. */
function ApprovalsAccess() {
  const [roles, setRoles] = useState<IAMRole[] | null>(null);
  const [access, setAccess] = useState<Record<string, string[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    Promise.all([getIAMAccess(), getIAMSettings()])
      .then(([a, s]) => {
        setRoles(a.roles.filter((r) => r.name !== "superadmin" && r.name !== "automation"));
        setAccess(s.approvals_access ?? {});
      })
      .catch(() => setError("Approval access settings are unavailable"));
  }, []);

  function toggle(role: string, perm: string, on: boolean) {
    setSaved(false);
    setAccess((cur) => {
      let next = new Set(cur[role] ?? []);
      if (on) {
        next.add(perm);
        if (perm === "approvals.decide") next.add("approvals.view");
      } else {
        next.delete(perm);
        if (perm === "approvals.view") next = new Set();
      }
      return { ...cur, [role]: [...next] };
    });
  }

  async function save() {
    setError(null);
    try {
      const s = await saveApprovalsAccess(access);
      setAccess(s.approvals_access ?? {});
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
    }
  }

  return (
    <section className="approvals-access" aria-labelledby="approvals-access-heading">
      <h3 id="approvals-access-heading">Who else can see and decide approvals</h3>
      <p className="field-hint">SuperAdmins always can. Only SuperAdmins see and change this.</p>
      {error ? <ErrorNotice error={error} /> : null}
      {roles ? (
        <table className="bk-table" aria-label="Approval access">
          <thead>
            <tr>
              <th>Role</th>
              {ACCESS_PERMS.map((p) => (
                <th key={p.perm}>{p.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {roles.map((r) => (
              <tr key={r.name}>
                <td>{r.title}</td>
                {ACCESS_PERMS.map((p) => (
                  <td key={p.perm}>
                    <input
                      type="checkbox"
                      aria-label={`${r.title}: ${p.label}`}
                      checked={(access[r.name] ?? []).includes(p.perm)}
                      onChange={(e) => toggle(r.name, p.perm, e.target.checked)}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      <div className="btn-row">
        <button className="btn btn-sm btn-primary" type="button" onClick={() => void save()}>
          Save access
        </button>
        {saved ? <span className="muted">Saved.</span> : null}
      </div>
    </section>
  );
}
