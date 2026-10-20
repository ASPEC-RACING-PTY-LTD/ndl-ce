import { useEffect, useRef, useState } from "react";
import {
  getCodeholdCLI,
  getCodeholdCLIRollout,
  saveCodeholdCLISettings,
  startCodeholdCLIRollout,
  type CodeholdCLIRollout,
  type CodeholdCLISettings,
  type CodeholdCLITarget,
} from "../api/client";
import { Dialog } from "../ui/Dialog";
import { LoadingState } from "./EmptyState";
import { ErrorNotice } from "./ErrorNotice";

const outcomeLabel: Record<string, string> = {
  installed: "Installed",
  updated: "Updated",
  current: "Already current",
  failed: "Failed",
  not_running: "Skipped, not running",
  unsupported: "Skipped, unsupported",
};

const planLabel: Record<string, string> = {
  install: "Install",
  update: "Update",
  skip: "Skip, already installed",
  unknown: "Could not check",
};

function targetResult(t: CodeholdCLITarget, dry: boolean): string {
  if (t.outcome && (t.outcome === "not_running" || t.outcome === "unsupported" || !dry)) {
    return outcomeLabel[t.outcome] ?? t.outcome;
  }
  if (!t.done) {
    return dry ? "Checking" : "Working";
  }
  return dry ? (planLabel[t.plan ?? ""] ?? "") : "";
}

function summaryLine(r: CodeholdCLIRollout): string {
  const s = r.summary ?? {};
  const skipped = `${s.not_running ?? 0} not running and ${s.unsupported ?? 0} unsupported skipped`;
  if (r.dry_run) {
    const plans: Record<string, number> = {};
    r.targets.forEach((t) => {
      if (t.plan) plans[t.plan] = (plans[t.plan] ?? 0) + 1;
    });
    return `Dry run: would install ${plans.install ?? 0}, update ${plans.update ?? 0}, skip ${plans.skip ?? 0} already installed${plans.unknown ? `, ${plans.unknown} could not be checked` : ""}; ${skipped}. Nothing was changed.`;
  }
  return `Installed ${s.installed ?? 0}, updated ${s.updated ?? 0}, already current ${s.current ?? 0}, failed ${s.failed ?? 0}; ${skipped}.`;
}

/**
 * CodeholdCLIDialog manages CodeHold's "ch" client in system containers:
 * where it comes from, whether new containers get it, and a bulk install or
 * update (with a dry run first) across the existing ones.
 */
export function CodeholdCLIDialog({
  open,
  onClose,
  canChange,
  asPage = false,
}: {
  open: boolean;
  onClose: () => void;
  canChange: boolean;
  /** asPage draws the same controls as a page (CodeHold → CodeHold CLI). */
  asPage?: boolean;
}) {
  const [settings, setSettings] = useState<CodeholdCLISettings | null>(null);
  const [rollout, setRollout] = useState<CodeholdCLIRollout | null>(null);
  const [update, setUpdate] = useState(false);
  const [includeStopped, setIncludeStopped] = useState(false);
  const [checked, setChecked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState("");
  const timer = useRef<number | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setError(null);
    setSaved("");
    setChecked(false);
    getCodeholdCLI()
      .then((body) => {
        setSettings(body.settings);
        setRollout(body.rollout);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Could not read the settings"));
  }, [open]);

  const running = rollout?.running ?? false;
  useEffect(() => {
    if (!open || !running) {
      return;
    }
    timer.current = window.setInterval(() => {
      void getCodeholdCLIRollout()
        .then((body) => setRollout(body.rollout))
        .catch(() => undefined);
    }, 1500);
    return () => {
      if (timer.current !== null) window.clearInterval(timer.current);
    };
  }, [open, running]);

  async function save() {
    if (!settings) return;
    setBusy(true);
    setError(null);
    try {
      const body = await saveCodeholdCLISettings(settings);
      setSettings(body.settings);
      setSaved("Saved.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Saving failed");
    } finally {
      setBusy(false);
    }
  }

  async function start(dryRun: boolean) {
    setBusy(true);
    setError(null);
    try {
      const body = await startCodeholdCLIRollout({
        dry_run: dryRun,
        update,
        include_stopped: includeStopped,
      });
      setRollout(body.rollout);
      if (dryRun) setChecked(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start");
    } finally {
      setBusy(false);
    }
  }

  const rows = rollout ? [...rollout.targets, ...(rollout.skipped ?? [])].sort((a, b) => a.name.localeCompare(b.name)) : [];
  const url = settings?.url.trim().replace(/\/+$/, "") || "";

  const buttons = (
    <div className="btn-row">
      {asPage ? null : (
        <button className="btn btn-ghost" type="button" onClick={onClose}>
          Close
        </button>
      )}
      <span className="theme-spacer" />
      <button className="btn btn-secondary" type="button" disabled={busy || running} onClick={() => void start(true)}>
        Dry run
      </button>
      {canChange ? (
        <button
          className="btn btn-primary"
          type="button"
          disabled={busy || running || !checked}
          title={checked ? undefined : "Run a dry run first"}
          onClick={() => void start(false)}
        >
          {update ? "Install or update now" : "Install now"}
        </button>
      ) : null}
    </div>
  );
  const content = (
    <>
      {error ? <ErrorNotice error={error} /> : null}
      {settings === null && !error ? <LoadingState label="Reading settings" /> : null}
      {settings ? (
        <>
          <p>
            <code>ch</code> is CodeHold's command line client. It is downloaded from{" "}
            <code>{url ? `${url}/downloads` : "your CodeHold server's /downloads"}</code> into <code>/usr/local/bin/ch</code>. Nobody is signed in: each
            user runs <code>ch auth login --server {url || "<your CodeHold address>"}</code> themselves.
          </p>
          {!url ? <p className="banner banner-warn">Set your CodeHold address and save before installing.</p> : null}
          <div className="form-grid">
            <label className="field">
              <span className="field-label">CodeHold address</span>
              <input
                className="field-input"
                type="url"
                placeholder="https://codehold.example.com"
                value={settings.url}
                disabled={!canChange}
                onChange={(e) => setSettings({ ...settings, url: e.target.value })}
              />
            </label>
            <label className="field">
              <span className="field-label">Containers at a time</span>
              <input
                className="field-input"
                type="number"
                min={1}
                max={20}
                value={settings.concurrency}
                disabled={!canChange}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    concurrency: Number(e.target.value) || 0,
                  })
                }
              />
            </label>
            <label className="field">
              <span className="field-label">Time limit per container (seconds)</span>
              <input
                className="field-input"
                type="number"
                min={30}
                max={3600}
                value={settings.timeout_seconds}
                disabled={!canChange}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    timeout_seconds: Number(e.target.value) || 0,
                  })
                }
              />
            </label>
          </div>
          <label className="check-row">
            <input
              type="checkbox"
              checked={settings.auto_install}
              disabled={!canChange}
              onChange={(e) => setSettings({ ...settings, auto_install: e.target.checked })}
            />
            <span>Install on new containers (can be unticked per container under Extras when creating one)</span>
          </label>
          {canChange ? (
            <div className="btn-row">
              <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void save()}>
                Save settings
              </button>
              {saved ? <span className="meta">{saved}</span> : null}
            </div>
          ) : null}
          <hr />
          <h3>Existing containers</h3>
          <label className="check-row">
            <input type="checkbox" checked={update} onChange={(e) => (setUpdate(e.target.checked), setChecked(false))} />
            <span>Update containers that already have ch to the server's current version</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={includeStopped} onChange={(e) => (setIncludeStopped(e.target.checked), setChecked(false))} />
            <span>Include containers that are not running (each is started for the install and stopped again)</span>
          </label>
          {rollout ? (
            <>
              <p className="meta" role="status">
                {rollout.running ? `${rollout.targets.filter((t) => t.done).length} of ${rollout.targets.length} done` : summaryLine(rollout)}
              </p>
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Container</th>
                      <th>Status</th>
                      <th>ch</th>
                      <th>{rollout.dry_run ? "Plan" : "Result"}</th>
                      <th>Note</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((t) => (
                      <tr key={t.workload_id}>
                        <td>{t.name}</td>
                        <td>{t.status}</td>
                        <td>{t.version || (t.installed ? "installed" : t.outcome === "not_running" || t.outcome === "unsupported" ? "" : "not installed")}</td>
                        <td>{targetResult(t, rollout.dry_run)}</td>
                        <td className="meta">{t.reason}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          ) : (
            <p className="meta">Run a dry run to see every container and whether ch is installed. Nothing changes until you choose Install now.</p>
          )}
        </>
      ) : null}
    </>
  );
  if (asPage) {
    return (
      <section className="page page-wide" aria-labelledby="ch-cli-heading">
        <header className="page-header">
          <h1 id="ch-cli-heading">CodeHold CLI</h1>
          <p className="page-lede">The ch client in system containers.</p>
        </header>
        <div className="panel admin-box">{content}</div>
        <div className="sticky-actions">{buttons}</div>
      </section>
    );
  }
  return (
    <Dialog open={open} title="CodeHold CLI in containers" wide onClose={onClose} footer={buttons}>
      {content}
    </Dialog>
  );
}
