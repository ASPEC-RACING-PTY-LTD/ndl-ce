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
    <section className={"panel bk-panel" + (degraded || problem ? " is-warn" : "")} aria-labelledby="platform-backup-heading">
      <div className="bk-head">
        <h2 id="platform-backup-heading">Disaster recovery</h2>
        <span className="bk-summary-meta">{recoverySummary(st)}</span>
      </div>
      <div className="bk-fill">
        {error ? <ErrorNotice error={error} /> : null}
        {notice ? (
          <p className="banner" role="status">
            {notice}
          </p>
        ) : null}
        <div className="bk-stats" aria-label="Disaster recovery">
          <span className={"bk-stat" + (degraded ? " is-warn" : "")} title={health?.checked_at ? `Checked ${formatWhen(health.checked_at)}` : "Checked after each platform backup"}>
            Recoverable <b>{health?.checked_at ? `${health.recoverable ?? 0} of ${health.total ?? 0}` : "Not checked"}</b>
            {health?.checked_at ? ` · ${health.stale ?? 0} stale · ${health.missing ?? 0} missing` : ""}
          </span>
          <span className={"bk-stat" + (problem ? " is-danger" : "")} title={problem || undefined}>
            Platform backup <b>{!st.enabled ? "Off" : last ? formatWhen(last.created_at ?? "") : "None yet"}</b>
            {problem ? ` · ${problem}` : last ? ` · ${formatBytes(last.size ?? 0)} · verified · ${st.snapshot?.published?.length ?? 0} kept` : " · runs daily"}
          </span>
          <span className={"bk-stat" + (waiting.length ? " is-warn" : "")}>
            Owners who can unlock it <b>{(st.owners_ready ?? []).length}</b>
            {waiting.length ? ` · Waiting for ${waiting.join(", ")} to sign in` : ""}
          </span>
        </div>
        <p className="bk-hint">
          The database, configuration, backup keys and the index of every restore point are encrypted on this host and
          kept in {st.target ? `${st.target.name} (${st.target.bucket})` : "an object storage target"} under ndl-platform/.
          Only an Owner's password unlocks them; the bucket's credentials alone cannot. To recover on a new host, choose
          "Recover an existing NDL-CE" on its setup screen.
        </p>
        {health?.items?.length ? (
          <div className="bk-scroll">
            <table className="bk-table" aria-label="Workload recoverability">
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
                    <td>
                      <span className={"bk-pill " + (it.status === "recoverable" ? "is-ok" : it.status === "stale" ? "is-warn" : "is-danger")}>{it.status}</span>
                    </td>
                    <td>{it.latest_at ? `${formatWhen(it.latest_at)}${it.deep ? " · read back" : ""}` : it.error || "None"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {owner ? (
          <div className="bk-actions is-footer">
            <button className="btn btn-sm btn-primary" type="button" disabled={busy !== null || !st.enabled} onClick={() => void act("run", runPlatformBackup, "The platform was backed up and the copy was read back and decrypted.")}>
              {busy === "run" ? "Backing up" : "Back up platform now"}
            </button>
            <button className="btn btn-sm" type="button" disabled={busy !== null} onClick={() => void act("verify", () => verifyPlatformBackup(true), "Every protected workload's newest offsite restore point was read back.")}>
              {busy === "verify" ? "Verifying" : "Verify deeply"}
            </button>
            <label className="bk-inline-field">
              <span>Target</span>
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
            <button className="btn btn-sm" type="button" disabled={busy !== null} onClick={() => void act("toggle", () => updatePlatformBackup({ enabled: !st.enabled }), st.enabled ? "The platform backup is off." : "The platform backup is on.")}>
              {st.enabled ? "Turn off" : "Turn on"}
            </button>
            <button className="btn btn-sm" type="button" disabled={busy !== null} onClick={() => setRotating(true)}>
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
      </div>
    </section>
  );
}
