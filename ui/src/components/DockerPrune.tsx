import { useEffect, useState } from "react";
import { getDockerPrune, putDockerPruneSettings, runDockerPrune } from "../api/client";
import { formatBytes } from "../format";
import { useQuery } from "../query";
import { ConfirmDialog } from "./ConfirmDialog";
import { ErrorNotice } from "./ErrorNotice";

/** DockerPrunePanel schedules and runs pruning of unused Docker images in every machine. */
export function DockerPrunePanel({ mutate }: { mutate: boolean }) {
  const q = useQuery("docker-prune", getDockerPrune, 15000);
  const st = q.data;
  const [enabled, setEnabled] = useState(true);
  const [runAt, setRunAt] = useState("06:00");
  const [tz, setTz] = useState("Australia/Melbourne");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  // The saved schedule shows as a sentence; Edit opens the form.
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    if (st) {
      setEnabled(st.enabled);
      setRunAt(st.run_at);
      setTz(st.timezone);
    }
  }, [st?.enabled, st?.run_at, st?.timezone]); // eslint-disable-line react-hooks/exhaustive-deps

  async function save() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await putDockerPruneSettings({ enabled, run_at: runAt, timezone: tz });
      setNotice("Schedule saved.");
      setEditing(false);
      await q.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the schedule");
    } finally {
      setBusy(false);
    }
  }

  async function runNow() {
    setConfirm(false);
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await runDockerPrune();
      setNotice("Pruning unused Docker images in the background. The result shows here when it finishes.");
      await q.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the prune");
    } finally {
      setBusy(false);
    }
  }

  const last = st?.last_result;
  return (
    <section className="panel docker-prune" aria-labelledby="docker-prune-heading">
      <div className="panel-header">
        <h2 id="docker-prune-heading">Docker cleanup</h2>
        {mutate ? (
          <div className="btn-row is-flush">
            {!editing ? (
              <button className="btn btn-ghost btn-sm" type="button" disabled={busy} onClick={() => setEditing(true)}>
                Edit schedule
              </button>
            ) : null}
            <button className="btn btn-secondary btn-sm" type="button" disabled={busy || st?.running} onClick={() => setConfirm(true)}>
              {st?.running ? "Pruning…" : "Prune now"}
            </button>
          </div>
        ) : null}
      </div>
      <p className="field-hint">
        Removes unused images and the build cache on the host and every system container. Images any container uses, containers and volumes
        are kept.
      </p>
      {q.error ? <ErrorNotice error={q.error} /> : null}
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner" role="status">
          {notice}
        </p>
      ) : null}
      {st ? (
        <p className="muted">
          {st.enabled ? `Runs daily at ${st.run_at} (${st.timezone}).` : "The daily run is off."}
          {st.enabled && st.next_run_at ? ` Next: ${new Date(st.next_run_at).toLocaleString()}.` : ""}
        </p>
      ) : null}
      {last ? (
        <p className="muted">
          Last run {new Date(last.started_at).toLocaleString()} ({last.trigger}): {last.machines.length} machine(s),{" "}
          {last.images_deleted} image(s) removed, {formatBytes(last.bytes_freed)} freed
          {last.failed ? `, ${last.failed} failed: ${last.machines.filter((m) => m.error).map((m) => `${m.name}: ${m.error}`).join("; ")}` : ""}.
        </p>
      ) : null}
      {mutate && editing ? (
        <div className="btn-row">
          <label className="check-row">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            <span>Daily</span>
          </label>
          <input className="field-input" type="time" aria-label="Run at" value={runAt} onChange={(e) => setRunAt(e.target.value)} />
          <input className="field-input" aria-label="Time zone" value={tz} onChange={(e) => setTz(e.target.value)} />
          <button className="btn btn-primary btn-sm" type="button" disabled={busy} onClick={() => void save()}>
            Save schedule
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={busy}
            onClick={() => {
              if (st) {
                setEnabled(st.enabled);
                setRunAt(st.run_at);
                setTz(st.timezone);
              }
              setEditing(false);
            }}
          >
            Cancel
          </button>
        </div>
      ) : null}
      <ConfirmDialog open={confirm} title="Prune unused Docker images?" confirmLabel="Prune" onConfirm={() => void runNow()} onClose={() => setConfirm(false)}>
        <p>Unused images are removed from the host and every system container. They are pulled again if a container needs them later.</p>
      </ConfirmDialog>
    </section>
  );
}
