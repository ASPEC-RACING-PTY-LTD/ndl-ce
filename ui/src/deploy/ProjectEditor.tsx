import { useState, type ReactNode } from "react";
import {
  ApiError,
  createDeployProject,
  updateDeployProject,
  type DeployProject,
  type DeployProjectConfig,
  type DeploySettings,
} from "../api/client";
import type { DeployCommand } from "../generated/openapi";
import { ErrorNotice } from "../components/ErrorNotice";

type Container = { id: string; name: string };

export type EditorSeed = { workload_id?: string; working_dir?: string; name?: string };

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="field">
      <label className="field">
        <span className="field-label">{label}</span>
        {children}
      </label>
      {hint ? <span className="bk-hint">{hint}</span> : null}
    </div>
  );
}

/**
 * ProjectEditor is the SuperAdmin's form for a project: where it is, the
 * commands that update it, how updates are found and when it deploys on
 * its own. Commands run exactly as written, with bash, in the folder.
 */
export function ProjectEditor({
  project,
  seed,
  containers,
  settings,
  extra,
  onSaved,
  onCancel,
}: {
  project?: DeployProject;
  seed?: EditorSeed;
  containers: Container[];
  settings: DeploySettings;
  extra?: (cfg: DeployProjectConfig, set: (c: DeployProjectConfig) => void) => ReactNode;
  onSaved: (p: DeployProject) => void;
  onCancel: () => void;
}) {
  const [workloadId, setWorkloadId] = useState(project?.workload_id ?? seed?.workload_id ?? "");
  const [dir, setDir] = useState(project?.working_dir ?? seed?.working_dir ?? "");
  const [name, setName] = useState(project?.name ?? seed?.name ?? "");
  const [cfg, setCfg] = useState<DeployProjectConfig>(
    () =>
      project?.config ?? {
        commands: settings.default_commands?.length ? settings.default_commands : [{ run: "" }],
        detect: true,
        provider: "auto",
        remote: "origin",
        notify_events: settings.notify_events ?? ["deploy.failed", "deploy.succeeded", "bulk.finished"],
      },
  );
  const [envAdd, setEnvAdd] = useState<{ key: string; value: string }[]>([]);
  const [envRemove, setEnvRemove] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (patch: Partial<DeployProjectConfig>) => setCfg((c) => ({ ...c, ...patch }));
  const setCmd = (i: number, patch: Partial<DeployCommand>) =>
    set({ commands: cfg.commands.map((c, j) => (j === i ? { ...c, ...patch } : c)) });
  const move = (i: number, by: number) => {
    const next = [...cfg.commands];
    const [c] = next.splice(i, 1);
    next.splice(i + by, 0, c);
    set({ commands: next });
  };

  async function save() {
    setBusy(true);
    setError(null);
    const env: Record<string, string> = {};
    for (const e of envAdd) {
      if (e.key.trim()) {
        env[e.key.trim()] = e.value;
      }
    }
    const body = {
      workload_id: workloadId,
      working_dir: dir,
      name,
      config: { ...cfg, commands: cfg.commands.filter((c) => c.run.trim()) },
      env,
      env_remove: envRemove,
    };
    try {
      onSaved(project ? await updateDeployProject(project.id, body) : await createDeployProject(body));
    } catch (err) {
      setError(errorText(err, "Could not save the project"));
    } finally {
      setBusy(false);
    }
  }

  const envKeys = (cfg.env_keys ?? []).filter((k) => !envRemove.includes(k));

  return (
    <form
      className="dp-form"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      {error ? <ErrorNotice error={error} /> : null}
      <fieldset>
        <legend>Project</legend>
        <div className="form-grid">
          <Field label="Container">
            <select className="field-input" value={workloadId} disabled={!!project} required onChange={(e) => setWorkloadId(e.target.value)}>
              <option value="">Choose a system container</option>
              {containers.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Working folder" hint="Commands run here, inside the container.">
            <input className="field-input" value={dir} required placeholder="/srv/app" onChange={(e) => setDir(e.target.value)} />
          </Field>
          <Field label="Name">
            <input className="field-input" value={name} placeholder="Folder name" onChange={(e) => setName(e.target.value)} />
          </Field>
        </div>
      </fieldset>

      <fieldset>
        <legend>Update commands</legend>
        <p className="bk-hint">
          Each command runs with bash in the working folder, in order. The deployment succeeds when every command exits
          with 0; the first failing command stops it, unless it is marked to continue. Nothing is undone on failure.
        </p>
        <div className="dp-commands">
          {cfg.commands.map((c, i) => (
            <div className="dp-command" key={i}>
              <span className="dp-command-n">{i + 1}</span>
              <textarea
                className="field-input"
                aria-label={`Command ${i + 1}`}
                value={c.run}
                rows={Math.min(6, Math.max(1, c.run.split("\n").length))}
                spellCheck={false}
                onChange={(e) => setCmd(i, { run: e.target.value })}
              />
              <div className="dp-command-tools">
                <label className="check-row" title="Keep going if this command fails">
                  <input type="checkbox" checked={!!c.continue_on_error} onChange={(e) => setCmd(i, { continue_on_error: e.target.checked })} />{" "}
                  Continue on error
                </label>
                <div className="bk-actions">
                  <button type="button" className="btn btn-sm btn-ghost" disabled={i === 0} aria-label={`Move command ${i + 1} up`} onClick={() => move(i, -1)}>
                    ↑
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    disabled={i === cfg.commands.length - 1}
                    aria-label={`Move command ${i + 1} down`}
                    onClick={() => move(i, 1)}
                  >
                    ↓
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost btn-danger-text"
                    disabled={cfg.commands.length === 1}
                    aria-label={`Remove command ${i + 1}`}
                    onClick={() => set({ commands: cfg.commands.filter((_, j) => j !== i) })}
                  >
                    Remove
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
        <div>
          <button type="button" className="btn btn-sm" onClick={() => set({ commands: [...cfg.commands, { run: "" }] })}>
            Add command
          </button>
        </div>
        <div className="form-grid">
          <Field label="Run as" hint="Empty: the folder's owner, so Git uses their credentials.">
            <input className="field-input" value={cfg.run_as ?? ""} placeholder="Folder owner" onChange={(e) => set({ run_as: e.target.value })} />
          </Field>
          <Field label="Time limit (minutes)" hint={`Empty: ${Math.round((settings.default_timeout_sec ?? 1800) / 60)} minutes.`}>
            <input
              className="field-input"
              type="number"
              min={1}
              max={1440}
              value={cfg.timeout_sec ? Math.round(cfg.timeout_sec / 60) : ""}
              onChange={(e) => set({ timeout_sec: e.target.value ? Number(e.target.value) * 60 : 0 })}
            />
          </Field>
        </div>
        <label className="check-row">
          <input type="checkbox" checked={!!cfg.deploy_during_backup} onChange={(e) => set({ deploy_during_backup: e.target.checked })} /> Deploy
          during a backup of this container (by default a deployment waits for it)
        </label>
      </fieldset>

      <fieldset>
        <legend>Update detection</legend>
        <label className="check-row">
          <input type="checkbox" checked={!!cfg.detect} onChange={(e) => set({ detect: e.target.checked })} /> Check the Git remote for
          new commits
        </label>
        <div className="form-grid">
          <Field label="Provider" hint="Auto prefers CodeHold (ch), then GitHub (gh), then plain Git.">
            <select className="field-input" value={cfg.provider ?? "auto"} onChange={(e) => set({ provider: e.target.value as DeployProjectConfig["provider"] })}>
              <option value="auto">Auto</option>
              <option value="codehold">CodeHold (ch)</option>
              <option value="github">GitHub (gh)</option>
              <option value="git">Git only</option>
            </select>
          </Field>
          <Field label="Remote">
            <input className="field-input" value={cfg.remote ?? "origin"} onChange={(e) => set({ remote: e.target.value })} />
          </Field>
          <Field label="Branch" hint="Empty: the branch the folder is on.">
            <input className="field-input" value={cfg.branch ?? ""} placeholder="Current branch" onChange={(e) => set({ branch: e.target.value })} />
          </Field>
          <Field label="Fallback remote" hint="Used when the remote cannot be reached, such as a GitHub mirror.">
            <input className="field-input" value={cfg.fallback_remote ?? ""} placeholder="None" onChange={(e) => set({ fallback_remote: e.target.value })} />
          </Field>
          <Field label="Check every (minutes)" hint={`Empty: ${Math.round((settings.detect_interval_sec ?? 600) / 60)} minutes.`}>
            <input
              className="field-input"
              type="number"
              min={1}
              max={10080}
              value={cfg.detect_interval_sec ? Math.round(cfg.detect_interval_sec / 60) : ""}
              onChange={(e) => set({ detect_interval_sec: e.target.value ? Number(e.target.value) * 60 : 0 })}
            />
          </Field>
        </div>
        <label className="check-row">
          <input type="checkbox" checked={!!cfg.auto_deploy} onChange={(e) => set({ auto_deploy: e.target.checked })} /> Deploy new commits
          automatically
        </label>
        {cfg.auto_deploy ? (
          <Field label="Only between" hint="Like 02:00-05:00, in the host's time. Empty: any time.">
            <input className="field-input" value={cfg.auto_window ?? ""} placeholder="Any time" onChange={(e) => set({ auto_window: e.target.value })} />
          </Field>
        ) : null}
      </fieldset>

      {extra ? extra(cfg, setCfg) : null}

      <fieldset>
        <legend>Environment</legend>
        <p className="bk-hint">Set for every command. Values are stored as secrets and never shown again.</p>
        {envKeys.length ? (
          <div className="dp-env">
            {envKeys.map((k) => (
              <span key={k} className="bk-pill">
                {k}{" "}
                <button type="button" className="btn btn-sm btn-ghost" aria-label={`Remove ${k}`} onClick={() => setEnvRemove((r) => [...r, k])}>
                  ×
                </button>
              </span>
            ))}
          </div>
        ) : null}
        {envAdd.map((e, i) => (
          <div className="form-grid" key={i}>
            <input
              className="field-input"
              aria-label="Variable name"
              placeholder="NAME"
              value={e.key}
              onChange={(ev) => setEnvAdd((cur) => cur.map((x, j) => (j === i ? { ...x, key: ev.target.value } : x)))}
            />
            <input
              className="field-input"
              aria-label="Variable value"
              type="password"
              autoComplete="off"
              placeholder="Value"
              value={e.value}
              onChange={(ev) => setEnvAdd((cur) => cur.map((x, j) => (j === i ? { ...x, value: ev.target.value } : x)))}
            />
          </div>
        ))}
        <div>
          <button type="button" className="btn btn-sm" onClick={() => setEnvAdd((cur) => [...cur, { key: "", value: "" }])}>
            Add variable
          </button>
        </div>
      </fieldset>

      <div className="bk-actions is-footer">
        <button type="button" className="btn" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {project ? "Save project" : "Add project"}
        </button>
      </div>
    </form>
  );
}
