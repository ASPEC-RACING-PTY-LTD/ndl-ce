import { useEffect, useMemo, useRef, useState } from "react";
import { deploymentLogDownloadURL, getDeploymentLog, type Deployment } from "../api/client";
import { ansiSpans, logLines, type AnsiSpan } from "./ansi";

/** Shown at most in the page; the download has the whole log. */
const MAX_LINES = 5000;

export function formatSeconds(sec?: number): string {
  if (sec == null || sec < 0) {
    return "";
  }
  if (sec < 60) {
    return `${sec}s`;
  }
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  if (m < 60) {
    return s ? `${m}m ${s}s` : `${m}m`;
  }
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function shortCommit(c?: string): string {
  return c ? c.slice(0, 7) : "";
}

export const ACTIVE = new Set(["queued", "waiting", "starting", "running"]);

export function deployStatusLabel(d: Pick<Deployment, "status" | "cancel_requested">): string {
  switch (d.status) {
    case "queued":
      return "Queued";
    case "waiting":
      return "Waiting";
    case "starting":
      return "Starting";
    case "running":
      return d.cancel_requested ? "Stopping" : "Deploying";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    case "timed-out":
      return "Timed out";
    case "interrupted":
      return "Interrupted";
    default:
      return d.status;
  }
}

/** badgeStatus maps a deployment to a StatusBadge tone. */
export function badgeStatus(status: string): string {
  switch (status) {
    case "succeeded":
      return "succeeded";
    case "failed":
    case "timed-out":
    case "interrupted":
      return "failed";
    case "cancelled":
      return "cancelled";
    case "queued":
    case "waiting":
    case "starting":
    case "running":
      return "starting";
    default:
      return status;
  }
}

/** DeployProgress is one segment per command, with time against the estimate. */
export function DeployProgress({ d, estimate }: { d: Deployment; estimate?: number }) {
  const [, tick] = useState(0);
  useEffect(() => {
    if (d.status !== "running") {
      return;
    }
    const t = window.setInterval(() => tick((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, [d.status]);
  const count = Math.max(d.step_count || 0, d.steps.length, 1);
  const elapsed = d.started_at ? Math.max(0, Math.round((Date.now() - new Date(d.started_at).getTime()) / 1000)) : 0;
  const segs = Array.from({ length: count }, (_, i) => {
    const st = d.steps[i];
    if (d.status === "queued" || d.status === "waiting" || d.status === "starting") {
      return "is-waiting";
    }
    if (!st?.started_at) {
      return "";
    }
    if (!st.finished_at) {
      return "is-running";
    }
    if (st.exit_code && st.exit_code !== 0) {
      return st.continue_on_error ? "is-tolerated" : "is-fail";
    }
    return "is-ok";
  });
  let left = deployStatusLabel(d);
  if (d.status === "running") {
    left = `${d.cancel_requested ? "Stopping" : "Command"} ${Math.max(d.current_step, 1)} of ${count}`;
  } else if (d.status === "waiting" && d.waiting_for) {
    left = d.waiting_for;
  }
  let right = "";
  if (d.status === "running") {
    right = formatSeconds(elapsed) + (estimate ? ` of ~${formatSeconds(estimate)}` : "");
  }
  return (
    <div className="dp-progress" role="group" aria-label={`Deployment progress: ${left}`}>
      <div className="dp-segments" aria-hidden="true">
        {segs.map((cls, i) => (
          <span key={i} className={`dp-seg ${cls}`} />
        ))}
      </div>
      <div className="dp-progress-label">
        <span>{left}</span>
        <span>{right}</span>
      </div>
    </div>
  );
}

function spanClass(s: AnsiSpan): string | undefined {
  const c = [s.bold ? "a-bold" : "", s.dim ? "a-dim" : "", s.color ? `a-${s.color}` : ""].filter(Boolean).join(" ");
  return c || undefined;
}

/**
 * DeploymentLog follows a deployment's output while it runs and shows the
 * whole log when it is done. Only new bytes are fetched each second.
 */
export function DeploymentLog({ d }: { d: Deployment }) {
  const [raw, setRaw] = useState("");
  const [gone, setGone] = useState(false);
  const [follow, setFollow] = useState(true);
  const offset = useRef(0);
  const pre = useRef<HTMLPreElement>(null);
  const active = ACTIVE.has(d.status);

  useEffect(() => {
    offset.current = 0;
    setRaw("");
    setGone(false);
  }, [d.id]);

  useEffect(() => {
    let stop = false;
    let timer = 0;
    const load = async () => {
      try {
        for (let i = 0; i < 20 && !stop; i++) {
          const lg = await getDeploymentLog(d.id, offset.current);
          if (stop) {
            return;
          }
          if (lg.gone) {
            setGone(true);
          }
          if (lg.data) {
            offset.current = lg.next;
            setRaw((cur) => cur + lg.data);
          }
          if (lg.next >= lg.size || !lg.data) {
            break;
          }
        }
      } catch {
        // The next poll tries again.
      }
      if (!stop && active) {
        timer = window.setTimeout(load, 1000);
      }
    };
    void load();
    return () => {
      stop = true;
      window.clearTimeout(timer);
    };
  }, [d.id, active]);

  const { lines, hidden } = useMemo(() => {
    const all = logLines(raw);
    if (all.length > 1 && all[all.length - 1] === "") {
      all.pop();
    }
    const hiddenCount = Math.max(0, all.length - MAX_LINES);
    let style = {};
    const out = all.slice(hiddenCount).map((line) => {
      const r = ansiSpans(line, style);
      style = r.end;
      return r.spans;
    });
    return { lines: out, hidden: hiddenCount };
  }, [raw]);

  useEffect(() => {
    if (follow && pre.current) {
      pre.current.scrollTop = pre.current.scrollHeight;
    }
  }, [lines, follow]);

  return (
    <div className="bk-panel" style={{ padding: 0, gap: "var(--space-2)" }}>
      <div className="bk-head">
        <span className="bk-hint">
          {hidden ? `Showing the last ${MAX_LINES.toLocaleString()} lines; ${hidden.toLocaleString()} earlier lines are in the download.` : null}
          {gone ? "The log of this deployment was removed by log retention." : null}
        </span>
        <div className="bk-actions">
          <label className="check-row">
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
          </label>
          {!gone ? (
            <a className="btn btn-sm" href={deploymentLogDownloadURL(d.id)} download>
              Download log
            </a>
          ) : null}
        </div>
      </div>
      <pre
        ref={pre}
        className="dp-log"
        aria-label="Deployment output"
        aria-live={active ? "polite" : undefined}
        onScroll={(e) => {
          const el = e.currentTarget;
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          if (atBottom !== follow) {
            setFollow(atBottom);
          }
        }}
      >
        {lines.length === 0 ? (active ? "Waiting for output…" : "No output.") : null}
        {lines.map((spans, i) => (
          <span key={i}>
            {spans.map((s, j) => (
              <span key={j} className={spanClass(s)}>
                {s.text}
              </span>
            ))}
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}
