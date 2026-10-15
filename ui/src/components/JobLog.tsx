import { useEffect, useState } from "react";
import { getOpsJob, type OpsJob } from "../api/client";
import { formatWhen } from "../format";
import { StatusBadge } from "./StatusBadge";

function tone(status: string): string {
  if (status === "succeeded") {
    return "healthy";
  }
  if (status === "failed") {
    return "critical";
  }
  return "running";
}

/** JobLog follows a background job until it finishes, then calls onDone once. */
export function JobLog({ job, onDone, onClose }: { job: OpsJob; onDone?: (j: OpsJob) => void; onClose?: () => void }) {
  const [cur, setCur] = useState<OpsJob>(job);

  useEffect(() => {
    setCur(job);
    if (job.status !== "running") {
      return;
    }
    let stop = false;
    let timer = 0;
    async function poll() {
      try {
        const next = await getOpsJob(job.id);
        if (stop) {
          return;
        }
        setCur(next);
        if (next.status !== "running") {
          onDone?.(next);
          return;
        }
      } catch {
        // keep polling; the job keeps running on the server
      }
      timer = window.setTimeout(() => void poll(), 1000);
    }
    timer = window.setTimeout(() => void poll(), 600);
    return () => {
      stop = true;
      window.clearTimeout(timer);
    };
    // onDone is a callback from the parent; following the job id is enough.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job.id]);

  const lines = cur.lines ?? [];
  return (
    <article className="panel job-log" aria-label={`${cur.name} progress`}>
      <header className="docker-card-head">
        <strong className="docker-head-name">{cur.name}</strong>
        <StatusBadge status={tone(cur.status)} label={cur.status === "running" ? "Running" : cur.status === "succeeded" ? "Done" : `Finished with ${cur.failed} problem(s)`} />
        {onClose && cur.status !== "running" ? (
          <button className="btn btn-ghost btn-sm" type="button" onClick={onClose}>
            Dismiss
          </button>
        ) : null}
      </header>
      <p className="meta">
        Started {formatWhen(cur.started_at)}
        {cur.started_by ? ` by ${cur.started_by}` : ""}
      </p>
      <ol className="job-lines">
        {lines.length === 0 ? <li className="meta">Starting</li> : null}
        {lines.map((l, i) => (
          <li key={i} className={`job-line is-${l.level}`}>
            <span className="job-level">{l.level}</span>
            <span className="job-msg">{l.message}</span>
          </li>
        ))}
      </ol>
    </article>
  );
}
