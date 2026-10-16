import { useEffect, useState } from "react";
import {
  ApiError,
  getPlatformBackup,
  rotatePlatformBackupKey,
  runPlatformBackup,
  updatePlatformBackup,
  verifyPlatformBackup,
} from "../api/client";
import type { BackupTarget, PlatformBackupStatus } from "../generated/openapi";
import { formatBytes, formatWhen } from "../format";
import { ConfirmDialog } from "./ConfirmDialog";
import { ErrorNotice } from "./ErrorNotice";
import { Field } from "./Field";

const OBJECT_KINDS = new Set(["s3", "r2", "aws", "b2", "minio"]);

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

/** recoverySummary is "N of M workloads recoverable" for a status. */
export function recoverySummary(st: PlatformBackupStatus | null | undefined): string {
  const h = st?.health;
  if (!h?.checked_at) {
    return "Not checked yet";
  }
  return `${h.recoverable ?? 0} of ${h.total ?? 0} workloads recoverable`;
}

/**
 * PlatformBackupPanel shows the encrypted backup of NDL-CE itself and how
 * many protected workloads can be recovered from offsite backups. An Owner
 * can back up now, verify deeply, choose the target and rotate the key.
 */
export function PlatformBackupPanel({ owner, targets }: { owner: boolean; targets: BackupTarget[] }) {
  const [st, setSt] = useState<PlatformBackupStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [rotating, setRotating] = useState(false);
  const [password, setPassword] = useState("");
  const [showItems, setShowItems] = useState(false);

  useEffect(() => {
    getPlatformBackup()
      .then(setSt)
      .catch(() => setSt(null));
  }, []);

  async function act(name: string, run: () => Promise<PlatformBackupStatus>, done: string) {
    setBusy(name);
    setError(null);
    setNotice(null);
    try {
      setSt(await run());
      setNotice(done);
    } catch (err) {
      setError(errorText(err, "The platform backup failed"));
      getPlatformBackup()
        .then(setSt)
        .catch(() => undefined);
    } finally {
      setBusy(null);
    }
  }

  if (!st) {
    return null;
  }
  const objectTargets = targets.filter((t) => OBJECT_KINDS.has(t.kind ?? ""));
  const health = st.health;
  const last = st.snapshot?.last;
  const waiting = st.owners_waiting ?? [];
  const problem = st.snapshot?.error || st.envelope?.error;
  const degraded = (health?.missing ?? 0) + (health?.stale ?? 0) > 0;
  return (
    <section className="section-block" aria-labelledby="platform-backup-heading">
      <div className="page-header-row">
        <h2 id="platform-backup-heading">Disaster recovery</h2>
        {owner ? (
          <div className="btn-row">
            <button className="btn" type="button" disabled={busy !== null} onClick={() => void act("verify", () => verifyPlatformBackup(true), "Every protected workload's newest offsite restore point was read back.")}>
              {busy === "verify" ? "Verifying" : "Verify deeply"}
            </button>
            <button className="btn btn-primary" type="button" disabled={busy !== null || !st.enabled} onClick={() => void act("run", runPlatformBackup, "The platform was backed up and the copy was read back and decrypted.")}>
              {busy === "run" ? "Backing up" : "Back up platform now"}
            </button>
          </div>
        ) : null}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner" role="status">
          {notice}
        </p>
      ) : null}
      <div className="summary-grid" aria-label="Disaster recovery">
        <article className={"summary-card" + (degraded ? " is-warn" : "")}>
          <span className="label">Recoverable workloads</span>
          <span className="value">{health?.checked_at ? `${health.recoverable ?? 0} of ${health.total ?? 0}` : "Not checked"}</span>
          <span className="meta">
            {health?.checked_at
              ? `${health.stale ?? 0} stale · ${health.missing ?? 0} missing · checked ${formatWhen(health.checked_at)}`
              : "Checked after each platform backup"}
          </span>
        </article>
        <article className={"summary-card" + (problem ? " is-danger" : "")}>
          <span className="label">Platform backup</span>
          <span className="value">{!st.enabled ? "Off" : last ? formatWhen(last.created_at ?? "") : "None yet"}</span>
          <span className="meta">
            {problem ? problem : last ? `${formatBytes(last.size ?? 0)} · verified · ${st.snapshot?.published?.length ?? 0} kept` : "Runs daily"}
          </span>
        </article>
        <article className={"summary-card" + (waiting.length ? " is-warn" : "")}>
          <span className="label">Owners who can unlock it</span>
          <span className="value">{(st.owners_ready ?? []).length}</span>
          <span className="meta">
            {waiting.length ? `Waiting for ${waiting.join(", ")} to sign in` : "Every Owner's password unlocks it"}
          </span>
        </article>
      </div>
      <p className="field-hint">
        The database, configuration, backup keys and the index of every restore point are encrypted on this host and
        kept in {st.target ? `${st.target.name} (${st.target.bucket})` : "an object storage target"} under ndl-platform/.
        Only an Owner's password unlocks them; the bucket's credentials alone cannot. To recover on a new host, choose
        "Recover an existing NDL-CE" on its setup screen.
      </p>
      {health?.items?.length ? (
        <>
          <button className="btn btn-ghost" type="button" onClick={() => setShowItems((v) => !v)}>
            {showItems ? "Hide workloads" : "Show workloads"}
          </button>
          {showItems ? (
            <table className="table" aria-label="Workload recoverability">
              <thead>
                <tr>
                  <th>Workload</th>
                  <th>Status</th>
                  <th>Newest offsite restore point</th>
                </tr>
              </thead>
              <tbody>
                {health.items.map((it) => (
                  <tr key={it.workload_id}>
                    <td>{it.name}</td>
                    <td>{it.status}</td>
                    <td>{it.latest_at ? `${formatWhen(it.latest_at)}${it.deep ? " · read back" : ""}` : it.error || "None"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
        </>
      ) : null}
      {owner ? (
        <div className="btn-row">
          <label className="field">
            <span className="field-label">Target</span>
            <select
              className="field-input"
              value={st.target_id ?? ""}
              disabled={busy !== null}
              onChange={(e) => void act("target", () => updatePlatformBackup({ target_id: e.target.value }), "Target saved. The key envelope is published there on the next backup.")}
            >
              <option value="">First object storage target</option>
              {objectTargets.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          </label>
          <button className="btn" type="button" disabled={busy !== null} onClick={() => void act("toggle", () => updatePlatformBackup({ enabled: !st.enabled }), st.enabled ? "The platform backup is off." : "The platform backup is on.")}>
            {st.enabled ? "Turn off" : "Turn on"}
          </button>
          <button className="btn" type="button" disabled={busy !== null} onClick={() => setRotating(true)}>
            Rotate key
          </button>
        </div>
      ) : null}
      <ConfirmDialog
        open={rotating}
        title="Rotate the platform backup key"
        confirmLabel="Rotate"
        danger
        confirmDisabled={!password}
        onClose={() => {
          setRotating(false);
          setPassword("");
        }}
        onConfirm={() => {
          const pw = password;
          setRotating(false);
          setPassword("");
          void act("rotate", () => rotatePlatformBackupKey(pw), "The key was rotated and a new platform backup was verified.");
        }}
      >
        <p>
          A new key is made and wrapped with your password. Other Owners can unlock it after their next sign-in. Platform
          backups made with the old key are deleted once one with the new key is verified.
        </p>
        <p>
          Copies of old envelopes or backups that anyone already downloaded stay readable with the passwords of that time.
          Rotation cannot reach them.
        </p>
        <Field id="platform-rotate-password" label="Your password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
      </ConfirmDialog>
    </section>
  );
}
