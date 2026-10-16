import { useEffect, useState } from "react";
import { disableCodehold, getCodehold, type CodeholdRunner } from "../api/client";
import { Dialog } from "../ui/Dialog";
import { LoadingState } from "./EmptyState";
import { ErrorNotice } from "./ErrorNotice";

type Choice = "stop" | "delete";

/**
 * CodeholdDisableDialog turns CodeHold runners off. Every runner with a
 * container needs a decision first: stop and keep it, or delete it. Force
 * stops them all and deletes them after seven days instead.
 */
export function CodeholdDisableDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: (msg: string) => void }) {
  const [runners, setRunners] = useState<CodeholdRunner[] | null>(null);
  const [choice, setChoice] = useState<Record<string, Choice | "">>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [force, setForce] = useState(false);

  useEffect(() => {
    if (!open) {
      return;
    }
    setRunners(null);
    setChoice({});
    setError(null);
    setForce(false);
    getCodehold()
      .then((s) => setRunners(s.runners.filter((r) => !r.detached && r.workload_id)))
      .catch(() => setRunners([]));
  }, [open]);

  const list = runners ?? [];
  const decided = list.every((r) => choice[r.id] === "stop" || choice[r.id] === "delete");

  async function run(body: Parameters<typeof disableCodehold>[0]) {
    setBusy(true);
    setError(null);
    try {
      await disableCodehold(body);
      onDone(
        body.force
          ? "CodeHold runners turned off. Their containers are stopped and will be deleted in 7 days unless you keep them."
          : "CodeHold runners turned off.",
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Turning it off failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      title="Turn off CodeHold runners"
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <span className="theme-spacer" />
          {list.length > 0 ? (
            <button className="btn btn-ghost btn-danger-text" type="button" disabled={busy} onClick={() => setForce(true)}>
              Force turn off…
            </button>
          ) : null}
          <button className="btn btn-primary" type="button" disabled={busy || runners === null || !decided} onClick={() => void run({ decisions: choice as Record<string, Choice> })}>
            {busy ? "Working" : list.length ? "Apply and turn off" : "Turn off"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {runners === null ? <LoadingState label="Reading runners" /> : null}
      {runners !== null && list.length === 0 ? <p>No runner has a container here, so nothing else changes. CodeHold will show this node as offline.</p> : null}
      {list.length > 0 ? (
        <>
          <p>
            Choose what happens to each runner's container before the integration turns off. Stopped containers are kept as ordinary workloads; turning
            CodeHold runners on again hands them back to CodeHold.
          </p>
          <div className="btn-row">
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setChoice(Object.fromEntries(list.map((r) => [r.id, "stop"])))}>
              Stop all
            </button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setChoice(Object.fromEntries(list.map((r) => [r.id, "delete"])))}>
              Delete all
            </button>
          </div>
          <div className="ch-decisions" role="list" aria-label="Runner decisions">
            {list.map((r) => (
              <div key={r.id} className="ch-decision" role="listitem">
                <span>
                  <strong>{r.name}</strong>
                  <span className="meta"> {r.workload_name}</span>
                  {r.busy > 0 ? <span className="tunnel-reason"> running {r.busy} job(s) now</span> : null}
                </span>
                <select
                  className="field-input"
                  aria-label={`What happens to ${r.name}`}
                  value={choice[r.id] ?? ""}
                  onChange={(e) => setChoice((c) => ({ ...c, [r.id]: e.target.value as Choice | "" }))}
                >
                  <option value="">Choose…</option>
                  <option value="stop">Shut down and keep</option>
                  <option value="delete">Shut down and delete</option>
                </select>
              </div>
            ))}
          </div>
        </>
      ) : null}
      {force ? (
        <div className="banner banner-warn" role="alert">
          <p>
            <strong>Force turn off</strong> stops every runner's container now and deletes them all in 7 days. The dashboard shows a warning until then,
            where you can keep them or delete them sooner.
          </p>
          <div className="btn-row">
            <button className="btn btn-sm btn-danger" type="button" disabled={busy} onClick={() => void run({ force: true })}>
              Stop all and delete in 7 days
            </button>
            <button className="btn btn-sm btn-ghost" type="button" onClick={() => setForce(false)}>
              Back
            </button>
          </div>
        </div>
      ) : null}
    </Dialog>
  );
}
