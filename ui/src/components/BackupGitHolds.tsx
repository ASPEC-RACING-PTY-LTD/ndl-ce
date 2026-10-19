import { useEffect, useState } from "react";
import { ApiError, listBackupGitHolds, patchBackupWorkspace, resolveBackupGitHold } from "../api/client";
import type { BackupGitHold, BackupGitHoldResult } from "../generated/openapi";
import { formatWhen } from "../format";
import { ConfirmDialog } from "./ConfirmDialog";
import { ErrorNotice } from "./ErrorNotice";

function errorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const saved = (err.body as BackupGitHoldResult | undefined)?.saved;
    const failed = saved?.filter((s) => s.error).map((s) => `${s.path}: ${s.error}`);
    return failed?.length ? failed.join("\n") : err.message;
  }
  return err instanceof Error ? err.message : fallback;
}

/**
 * BackupGitHolds lists workloads whose backup is held because a Git
 * repository has work that is not committed and pushed, and lets a SuperAdmin
 * retry after saving it by hand, have NDL-CE commit and push it, or go back
 * to ignoring uncommitted Git work.
 */
export function BackupGitHolds({ owner, onChanged }: { owner: boolean; onChanged: () => void }) {
  const [holds, setHolds] = useState<BackupGitHold[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [saving, setSaving] = useState<BackupGitHold | null>(null);
  const [ignoring, setIgnoring] = useState(false);

  function load() {
    listBackupGitHolds()
      .then((res) => setHolds(res.items ?? []))
      .catch(() => setHolds([]));
  }
  useEffect(load, []);

  async function act(hold: BackupGitHold, action: "retry" | "save") {
    setBusy(hold.workload_id ?? "");
    setError(null);
    setNotice(null);
    try {
      const res = await resolveBackupGitHold(hold.workload_id ?? "", action);
      setNotice(
        res.held
          ? `${hold.workload_name} is still held: its Git work is not committed and pushed yet.`
          : `${hold.workload_name} was backed up.`,
      );
      onChanged();
    } catch (err) {
      setError(errorText(err, "Could not release the backup"));
    } finally {
      setBusy(null);
      load();
    }
  }

  async function ignore() {
    setIgnoring(false);
    setError(null);
    try {
      await patchBackupWorkspace({ ignore_uncommitted_git: true });
      setNotice("Uncommitted Git work is ignored again. Held workloads are backed up on their next run.");
      onChanged();
    } catch (err) {
      setError(errorText(err, "Could not change the setting"));
    } finally {
      load();
    }
  }

  if (holds.length === 0 && !notice && !error) {
    return null;
  }
  return (
    <section className="section-block" aria-labelledby="backup-git-holds-heading">
      <div className="page-header-row">
        <h2 id="backup-git-holds-heading">Held for uncommitted Git work</h2>
        {owner && holds.length > 0 ? (
          <button className="btn" type="button" onClick={() => setIgnoring(true)}>
            Ignore uncommitted Git
          </button>
        ) : null}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner" role="status">
          {notice}
        </p>
      ) : null}
      {holds.map((h) => (
        <article className="panel" key={h.workload_id} aria-label={`Held backup ${h.workload_name}`}>
          <p>
            <strong>{h.workload_name}</strong> is not being backed up since {formatWhen(h.held_since ?? "")}. Smart backups
            leave Git repositories to Git, so this work would be lost:
          </p>
          <ul>
            {(h.repos ?? []).map((r) => (
              <li key={r.path}>
                <code>{r.path}</code>
                {r.branch ? ` on ${r.branch}` : ""}
                {r.unpushed ? ": has commits that are on no remote" : ""}
                {r.unsaved?.length ? (
                  <>
                    {": not committed: "}
                    {r.unsaved.slice(0, 8).join(", ")}
                    {r.unsaved.length > 8 || r.more_changes ? " and more" : ""}
                  </>
                ) : null}
              </li>
            ))}
          </ul>
          {h.last_error ? <p className="banner banner-error">{h.last_error}</p> : null}
          {owner ? (
            <div className="btn-row">
              <button className="btn" type="button" disabled={busy !== null} onClick={() => void act(h, "retry")}>
                {busy === h.workload_id ? "Working" : "I saved it, back up now"}
              </button>
              <button className="btn btn-primary" type="button" disabled={busy !== null} onClick={() => setSaving(h)}>
                Commit and push for me
              </button>
            </div>
          ) : (
            <p className="field-hint">A SuperAdmin can release this backup.</p>
          )}
        </article>
      ))}
      <ConfirmDialog
        open={saving !== null}
        title="Commit and push for me"
        confirmLabel="Commit and push"
        onClose={() => setSaving(null)}
        onConfirm={() => {
          const h = saving;
          setSaving(null);
          if (h) {
            void act(h, "save");
          }
        }}
      >
        <p>
          NDL-CE commits the changes in {saving?.workload_name}'s repositories from inside the workload, with its own Git
          credentials, and pushes them to their remote. Then it backs the workload up.
        </p>
        <p>
          .env files, keys, certificates, databases, files named like secrets and files over 10 MB are never added or
          read. They stay as they are and are reported.
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={ignoring}
        title="Ignore uncommitted Git work"
        confirmLabel="Ignore"
        onClose={() => setIgnoring(false)}
        onConfirm={() => void ignore()}
      >
        <p>
          Workloads are backed up even when their Git repositories have work that is not committed and pushed. That work
          is not in the backup. You can change this again in Backups → Workspace.
        </p>
      </ConfirmDialog>
    </section>
  );
}
