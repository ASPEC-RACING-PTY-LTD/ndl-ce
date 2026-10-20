import { useEffect, useRef, useState } from "react";
import { ApiError, getLiveUpdates, rollbackLiveUpdate, uploadLiveUpdateBundle } from "../api/client";
import type { LiveUpdateStatus } from "../generated/openapi";
import { formatRelative, formatWhen } from "../format";
import { ErrorNotice } from "./ErrorNotice";

const RESULT_TONE: Record<string, string> = {
  applied: "is-ok",
  "rolled-back": "is-warn",
  failed: "is-danger",
  refused: "is-danger",
  superseded: "",
};

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

/**
 * LiveUpdatesPanel shows signed live updates: what runs now, what is
 * queued or waiting and why, the history, and SuperAdmin controls to upload a
 * bundle and roll back the newest update.
 */
export function LiveUpdatesPanel({ owner }: { owner: boolean }) {
  const [st, setSt] = useState<LiveUpdateStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  function load() {
    getLiveUpdates()
      .then((res) => {
        setSt(res);
        setError(null);
      })
      .catch((err) => setError(errorText(err, "Live updates are unavailable")));
  }

  useEffect(() => {
    if (!owner) {
      return;
    }
    load();
    const timer = window.setInterval(load, 15_000);
    return () => window.clearInterval(timer);
  }, [owner]);

  if (!owner) {
    return null;
  }

  async function onUpload(file: File) {
    setBusy(true);
    setNotice(null);
    setError(null);
    try {
      const res = await uploadLiveUpdateBundle(file);
      setNotice(
        `${res.version ?? res.id} was verified and queued (${(res.components ?? []).join(", ")}).` +
          (res.enabled ? " It applies as soon as nothing is running." : " Live updates are off on this host, so it waits."),
      );
      load();
    } catch (err) {
      setError(errorText(err, "The bundle was refused"));
    } finally {
      setBusy(false);
      if (fileRef.current) {
        fileRef.current.value = "";
      }
    }
  }

  async function onRollback() {
    setBusy(true);
    setNotice(null);
    try {
      await rollbackLiveUpdate();
      setNotice("Rollback requested. The previous version is restored and health-checked.");
      load();
    } catch (err) {
      setError(errorText(err, "Could not request a rollback"));
    } finally {
      setBusy(false);
    }
  }

  const history = [...(st?.history ?? [])].reverse();
  const busyNow = st?.busy ?? [];
  const canRollBack = (st?.history ?? []).some((e) => e.action === "apply" && e.result === "applied" && !e.rolled_back);
  return (
    <section className="panel bk-panel live-updates-panel" aria-labelledby="live-updates-heading">
      <div className="bk-head">
        <h2 id="live-updates-heading">Live updates</h2>
        <div className="bk-actions">
          <input
            ref={fileRef}
            type="file"
            accept=".ndlu"
            className="sr-only"
            id="live-update-file"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) {
                void onUpload(f);
              }
            }}
          />
          <button className="btn btn-sm" type="button" disabled={busy} onClick={() => fileRef.current?.click()}>
            Upload update
          </button>
          <button className="btn btn-sm btn-ghost btn-danger-text" type="button" disabled={busy || !canRollBack} onClick={() => void onRollback()}>
            Roll back last update
          </button>
        </div>
      </div>
      <p className="bk-hint">
        Signed updates of the web UI, control plane and agent apply without a package build. The web UI switches
        instantly; the control plane and agent restart in seconds while workloads keep running. Updates wait for
        backups and other changes to finish, and roll back on their own if the new version does not come up healthy.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {st ? (
        <>
          <div className="bk-stats" aria-label="Live update status">
            <span className={"bk-stat" + (st.enabled ? " is-ok" : " is-warn")}>
              Automatic updates <b>{st.enabled ? "On" : "Off"}</b>
            </span>
            <span className="bk-stat">
              Running <b>{st.current || st.package_version || st.version || "package"}</b>
            </span>
            {st.pending ? (
              <span className="bk-stat is-active">
                Applying <b>{st.pending.version}</b> · {st.pending.phase}
              </span>
            ) : null}
            {(st.queued ?? []).length ? (
              <span className="bk-stat is-active">
                Queued <b>{(st.queued ?? []).length}</b>
              </span>
            ) : null}
            <span className={"bk-stat" + (busyNow.length ? " is-warn" : "")} title={busyNow.join("; ") || undefined}>
              {busyNow.length ? "Would wait for" : "Nothing running"} {busyNow.length ? <b>{busyNow.join(", ")}</b> : null}
            </span>
            <span className="bk-stat" title={(st.trusted_keys ?? []).join(", ") || undefined}>
              Trusted signing keys <b>{(st.trusted_keys ?? []).length}</b>
            </span>
          </div>
          {!st.enabled ? (
            <p className="bk-hint">
              Off on this host. Updates you upload are verified and kept, but not applied until an administrator runs{" "}
              <code>ndl-live enable</code> as root.
            </p>
          ) : null}
          {st.waiting ? <p className="bk-hint">{st.waiting}</p> : null}
          {history.length ? (
            <div className="bk-scroll is-short">
              <table className="bk-table" aria-label="Live update history">
                <thead>
                  <tr>
                    <th>Update</th>
                    <th>What</th>
                    <th>Result</th>
                    <th>When</th>
                  </tr>
                </thead>
                <tbody>
                  {history.map((e, i) => (
                    <tr key={`${e.id}-${e.at}-${i}`}>
                      <td>{e.version || e.id}</td>
                      <td>
                        {e.action}
                        {e.components?.length ? ` · ${e.components.join(", ")}` : ""}
                      </td>
                      <td>
                        <span className={"bk-pill " + (RESULT_TONE[e.result] ?? "")} title={e.message || undefined}>
                          {e.rolled_back ? "rolled back later" : e.result}
                        </span>
                        {e.message ? <div className="bk-hint">{e.message}</div> : null}
                      </td>
                      <td title={formatWhen(e.at)}>{formatRelative(e.at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="bk-hint">No live updates yet.</p>
          )}
        </>
      ) : !error ? (
        <p className="bk-hint">Collecting</p>
      ) : null}
    </section>
  );
}
