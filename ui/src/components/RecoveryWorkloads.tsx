import { useEffect, useRef, useState } from "react";
import { ApiError, getRecovery, restoreRecoveredWorkload } from "../api/client";
import type { RecoveryWorkloadItem, RecoveryWorkloads as RecoveryState } from "../generated/openapi";
import { formatWhen } from "../format";
import { ErrorNotice } from "./ErrorNotice";

const STATUS_LABEL: Record<string, string> = {
  pending: "Not restored yet",
  running: "Restoring",
  restored: "Restored",
  needs_attention: "Needs your input",
  failed: "Failed",
};

function errorText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : "The restore could not start";
}

function NotBackedUp({ item }: { item: RecoveryWorkloadItem }) {
  const lost = item.not_backed_up ?? [];
  if (!lost.length) {
    return null;
  }
  return (
    <div className="banner banner-warn" role="note">
      <strong>These changes were not backed up</strong> and will not come back:
      <ul>
        {lost.map((r) => (
          <li key={r.path}>
            <code>{r.path}</code>
            {r.unpushed ? ` — ${r.unpushed_commits || "some"} commit(s) were never pushed` : ""}
            {r.not_backed_up?.length ? ` — uncommitted: ${r.not_backed_up.slice(0, 10).join(", ")}${r.not_backed_up.length > 10 ? " and more" : ""}` : ""}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** WorkloadInput collects what a workload that needs attention asks for. */
function WorkloadInput({ item, onSubmit, busy }: { item: RecoveryWorkloadItem; onSubmit: (tags: string[], tokens: Record<string, string>) => void; busy: boolean }) {
  const rh = item.restore?.rehydrate;
  const [tags, setTags] = useState<Record<string, boolean>>({});
  const [tokens, setTokens] = useState<Record<string, string>>({});
  const repos = (rh?.repos ?? []).filter((r) => r.status === "needs_credentials");
  const images = (rh?.images ?? []).filter((i) => i.status === "needs_tag_approval");
  const other = [
    ...(rh?.repos ?? []).filter((r) => r.status === "failed").map((r) => `${r.path}: ${r.error}`),
    ...(rh?.images ?? []).filter((i) => i.status === "failed").map((i) => `${i.ref}: ${i.error}`),
    ...(rh?.compose ?? []).filter((c) => c.status === "failed").map((c) => `${c.dir || "Docker"}: ${c.error}`),
  ];
  return (
    <div className="form">
      {repos.map((r) => (
        <label className="field" key={r.path}>
          <span className="field-label">
            Token to clone <code>{r.path}</code> (used once, not stored)
          </span>
          <input className="field-input" type="password" autoComplete="off" value={tokens[r.path ?? ""] ?? ""} onChange={(e) => setTokens((t) => ({ ...t, [r.path ?? ""]: e.target.value }))} />
          <span className="field-hint">{r.error}</span>
        </label>
      ))}
      {images.map((i) => (
        <label className="field-check" key={i.ref}>
          <input type="checkbox" checked={!!tags[i.ref ?? ""]} onChange={(e) => setTags((t) => ({ ...t, [i.ref ?? ""]: e.target.checked }))} />
          The exact image {i.digest} is gone. Use the tag <code>{i.ref}</code> instead (it may be a different version).
        </label>
      ))}
      {other.length ? (
        <ul className="field-hint">
          {other.map((o) => (
            <li key={o}>{o}</li>
          ))}
        </ul>
      ) : null}
      <button
        className="btn btn-primary"
        type="button"
        disabled={busy}
        onClick={() =>
          onSubmit(
            Object.keys(tags).filter((k) => tags[k]),
            Object.fromEntries(Object.entries(tokens).filter(([, v]) => v.trim() !== "")),
          )
        }
      >
        Continue restoring
      </button>
    </div>
  );
}

/**
 * RecoveryWorkloads appears after NDL-CE was recovered from a platform
 * backup and brings each workload back: all at once, or one at a time.
 */
export function RecoveryWorkloads({ owner }: { owner: boolean }) {
  const [state, setState] = useState<RecoveryState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const timer = useRef<number | null>(null);

  function load() {
    getRecovery()
      .then(setState)
      .catch(() => setState(null));
  }
  useEffect(() => {
    load();
    return () => {
      if (timer.current) {
        window.clearInterval(timer.current);
      }
    };
  }, []);
  const running = (state?.workloads ?? []).some((w) => w.restore?.status === "running" || w.restore?.step === "queued");
  useEffect(() => {
    if (running && !timer.current) {
      timer.current = window.setInterval(load, 3000);
    } else if (!running && timer.current) {
      window.clearInterval(timer.current);
      timer.current = null;
    }
  }, [running]);

  async function restore(id: string, tags: string[] = [], tokens: Record<string, string> = {}) {
    setBusy(true);
    setError(null);
    try {
      setState(await restoreRecoveredWorkload(id, { use_tags: tags, repo_tokens: tokens }));
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  if (!state?.recovered) {
    return null;
  }
  const rec = state.recovery;
  const items = state.workloads ?? [];
  const done = items.filter((w) => w.restore?.status === "restored").length;
  return (
    <section className="section-block" aria-labelledby="recovery-heading">
      <div className="page-header-row">
        <h2 id="recovery-heading">Recovered from a platform backup</h2>
        {owner ? (
          <button className="btn btn-primary" type="button" disabled={busy || running || !rec?.connection_verified} onClick={() => void restore("all")}>
            Restore all workloads
          </button>
        ) : null}
      </div>
      <p className="field-hint">
        Recovered {rec?.at ? formatWhen(rec.at) : ""} from the backup of {rec?.hostname} made {rec?.snapshot_created_at ? formatWhen(rec.snapshot_created_at) : ""}.{" "}
        {done} of {items.length} workloads restored. Each comes back on its base image, then its data from its newest
        offsite restore point, then its repositories at the recorded commit, its images by digest and its compose projects.
      </p>
      {!rec?.connection_verified ? (
        <p className="banner banner-error">
          The bucket connection was not verified{rec?.connection_error ? `: ${rec.connection_error}` : ""}. Fix the backup
          target before restoring workloads.
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {items.map((w) => {
        const st = w.restore?.status ?? "pending";
        return (
          <article className="panel" key={w.workload_id} aria-label={`Recovery of ${w.name}`}>
            <div className="page-header-row">
              <h3>{w.name}</h3>
              <span>
                {STATUS_LABEL[st] ?? st}
                {w.restore?.step && st === "running" ? ` — ${w.restore.step}` : ""}
              </span>
            </div>
            <p className="field-hint">
              {w.no_offsite_point
                ? "No offsite restore point: this workload cannot be restored from the bucket."
                : `Newest offsite backup ${w.backed_up_at ? formatWhen(w.backed_up_at) : ""} (${w.capture_mode || "full"}).`}
              {w.power_before === "running" ? " It was running and will be started." : ""}
            </p>
            <NotBackedUp item={w} />
            {w.restore?.error ? <p className="banner banner-error">{w.restore.error}</p> : null}
            {(w.restore?.rehydrate?.repos ?? [])
              .filter((r) => r.warning)
              .map((r) => (
                <p className="banner banner-warn" key={r.path}>
                  <code>{r.path}</code>: {r.warning}
                </p>
              ))}
            {owner && st === "needs_attention" ? (
              <WorkloadInput item={w} busy={busy} onSubmit={(tags, tokens) => void restore(w.workload_id ?? "", tags, tokens)} />
            ) : null}
            {owner && (st === "pending" || st === "failed") && !w.no_offsite_point ? (
              <button className="btn" type="button" disabled={busy || !rec?.connection_verified} onClick={() => void restore(w.workload_id ?? "")}>
                {st === "failed" ? "Try again" : "Restore"}
              </button>
            ) : null}
          </article>
        );
      })}
    </section>
  );
}
