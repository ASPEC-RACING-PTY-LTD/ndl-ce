import { useEffect, useState } from "react";
import {
  automationRunAction,
  getAutomationRun,
  listAutomationRunLogs,
  listAutomationRunNodes,
  type AutomationRun,
} from "../../api/client";
import { PageHeader } from "../../components/PageHeader";
import { ErrorNotice } from "../../components/ErrorNotice";
import { LoadingState } from "../../components/EmptyState";
import { StatusBadge } from "../../components/StatusBadge";
import { Link } from "../../components/Link";
import { navigate, usePath } from "../../router";
import { useSession } from "../../session";
import { hasGrant } from "../../rbac";
import { ReportView, type Report } from "../../automation/ReportView";
import { STATUS_LABELS, statusTone } from "../../automation/catalogue";
import { automationErrorAction, formatWhen } from "./AutomationHome";
import "../../automation/automation.css";

type Attempt = { attempt: number; status: string; error?: string; started_at: string; finished_at?: string | null };
type NodeRun = {
  id: string;
  node_id: string;
  scope: string;
  type: string;
  status: string;
  port?: string;
  input: unknown;
  output: unknown;
  error?: string;
  attempts: Attempt[];
  started_at?: string | null;
  finished_at?: string | null;
};
type LogLine = { node_id?: string; level: string; message: string; at: string };

const FINISHED = new Set(["succeeded", "failed", "cancelled", "dropped"]);

function pretty(v: unknown): string {
  if (v == null || (typeof v === "object" && Object.keys(v as object).length === 0)) {
    return "";
  }
  return JSON.stringify(v, null, 2);
}

function StepRow({ n }: { n: NodeRun }) {
  const [open, setOpen] = useState(n.status === "failed");
  const input = pretty(n.input);
  const output = pretty(n.output);
  return (
    <li className="au-card">
      <div className="au-card-head">
        <span className="au-card-title">
          {n.node_id}
          {n.scope ? <span className="meta"> · {n.scope}</span> : null}
        </span>
        <span className="meta">{n.type}</span>
        <StatusBadge status={statusTone(n.status)} label={`${STATUS_LABELS[n.status] ?? n.status}${n.port ? ` · ${n.port}` : ""}`} />
        <button type="button" className="btn btn-ghost btn-sm" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? "Hide" : "Details"}
        </button>
      </div>
      {n.error ? <p className="tunnel-reason">{n.error}</p> : null}
      {open ? (
        <div className="au-fields">
          {input ? (
            <div className="field">
              <span className="field-label">Input</span>
              <pre className="au-json">{input}</pre>
            </div>
          ) : null}
          {output ? (
            <div className="field">
              <span className="field-label">Output</span>
              <pre className="au-json">{output}</pre>
            </div>
          ) : null}
          {n.attempts.length ? (
            <div className="field is-wide">
              <span className="field-label">Attempts</span>
              <ul className="plain-list">
                {n.attempts.map((a) => (
                  <li key={a.attempt}>
                    {a.attempt}. {STATUS_LABELS[a.status] ?? a.status} {formatWhen(a.started_at)}
                    {a.error ? <span className="meta"> {a.error}</span> : null}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

export function RunPage() {
  const path = usePath();
  const id = decodeURIComponent(path.split("/")[3] ?? "");
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const runner = hasGrant(user, "policy.run");
  const [run, setRun] = useState<AutomationRun | null>(null);
  const [nodes, setNodes] = useState<NodeRun[]>([]);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [note, setNote] = useState("");

  useEffect(() => {
    let live = true;
    let timer = 0;
    const load = async () => {
      try {
        const [r, n, l] = await Promise.all([getAutomationRun(id), listAutomationRunNodes(id), listAutomationRunLogs(id)]);
        if (!live) return;
        setRun(r);
        setNodes((n.items ?? []) as unknown as NodeRun[]);
        setLogs((l.items ?? []) as unknown as LogLine[]);
        if (!FINISHED.has(r.status)) {
          timer = window.setTimeout(() => void load(), 3000);
        }
      } catch (err) {
        if (live) setError(err);
      }
    };
    void load();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [id]);

  async function act(action: "cancel" | "retry" | "approve", body?: Record<string, unknown>) {
    try {
      const r = await automationRunAction(id, action, body);
      if (action === "retry") {
        navigate(`/automation/runs/${r.id}`);
        return;
      }
      setRun(r);
      setError(null);
    } catch (err) {
      setError(err);
    }
  }

  if (error && !run) {
    return (
      <section className="page">
        <ErrorNotice error={error} action={automationErrorAction(error)} />
      </section>
    );
  }
  if (!run) {
    return (
      <section className="page">
        <LoadingState label="Loading run" />
      </section>
    );
  }
  const finished = FINISHED.has(run.status);
  const approvals = (run.pending_approvals ?? []) as { node_id: string; message?: string }[];
  const report = (run.report ?? null) as Report | null;
  return (
    <section className="page page-wide au-page" aria-labelledby="run-heading">
      <PageHeader
        id="run-heading"
        title={`${run.mode === "dry_run" ? "Test run" : "Run"} of ${run.flow_name || "a flow"}`}
        kicker={
          <>
            <Link href="/automation?tab=runs">Runs</Link> · <Link href={`/automation/flows/${run.flow_id}`}>{run.flow_name || "flow"}</Link> ·{" "}
            <StatusBadge status={statusTone(run.status)} label={STATUS_LABELS[run.status] ?? run.status} /> · started by {run.started_by || run.trigger_kind}{" "}
            {formatWhen(run.started_at ?? run.created_at)}
            {run.version ? ` · version ${run.version}` : " · draft"}
          </>
        }
        actions={
          runner ? (
            <div className="btn-row is-flush">
              {!finished ? (
                <button className="btn btn-secondary" type="button" onClick={() => void act("cancel")}>
                  Cancel
                </button>
              ) : (
                <button className="btn btn-secondary" type="button" onClick={() => void act("retry")}>
                  Retry
                </button>
              )}
            </div>
          ) : null
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {run.error ? <p className={`banner${run.status === "failed" ? " banner-error" : ""}`}>{run.error}</p> : null}
      {run.wake_at && run.status === "waiting" ? <p className="banner">Waiting until {formatWhen(run.wake_at)}.</p> : null}
      {approvals.length ? (
        <article className="panel stack" aria-label="Approval">
          <h2>Waiting for approval</h2>
          {approvals.map((a) => (
            <div key={a.node_id} className="stack">
              <p>{a.message || `Step ${a.node_id} asks for an approval.`}</p>
              {runner ? (
                <>
                  <input className="field-input" aria-label="Note" placeholder="Note (optional)" value={note} onChange={(e) => setNote(e.target.value)} />
                  <div className="btn-row is-flush">
                    <button className="btn btn-primary" type="button" onClick={() => void act("approve", { node_id: a.node_id, approved: true, note })}>
                      Approve
                    </button>
                    <button className="btn btn-secondary" type="button" onClick={() => void act("approve", { node_id: a.node_id, approved: false, note })}>
                      Reject
                    </button>
                  </div>
                </>
              ) : null}
            </div>
          ))}
        </article>
      ) : null}
      {report ? (
        <article className="panel">
          <h2>Test results</h2>
          <ReportView report={report} />
        </article>
      ) : null}
      <article className="panel stack">
        <h2>Steps</h2>
        {nodes.length === 0 ? <p className="muted">No steps ran yet.</p> : null}
        <ul className="plain-list stack">
          {nodes.map((n) => (
            <StepRow key={n.id} n={n} />
          ))}
        </ul>
      </article>
      <article className="panel stack">
        <h2>Log</h2>
        {logs.length === 0 ? (
          <p className="muted">Nothing logged yet.</p>
        ) : (
          <ul className="au-logs">
            {logs.map((l, i) => (
              <li key={i} className={l.level === "error" ? "is-error" : l.level === "warn" ? "is-warn" : undefined}>
                {new Date(l.at).toLocaleTimeString()} {l.message}
              </li>
            ))}
          </ul>
        )}
      </article>
      {pretty(run.trigger_payload) ? (
        <article className="panel">
          <h2>Trigger data</h2>
          <pre className="au-json">{pretty(run.trigger_payload)}</pre>
        </article>
      ) : null}
    </section>
  );
}
