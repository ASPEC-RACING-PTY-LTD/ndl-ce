import { useEffect, useState } from "react";
import {
  ApiError,
  createBackupPolicy,
  createBackupTarget,
  listBackupArtifacts,
  listBackupPolicies,
  listBackupRuns,
  listBackupTargets,
  listWorkloads,
  restoreBackupArtifact,
  runBackup,
} from "../api/client";
import type { Workload } from "../api/phase5";
import type {
  BackupArtifact,
  BackupPolicy,
  BackupRun,
  BackupTarget,
  CreateBackupPolicyRequest,
  CreateBackupTargetRequest,
} from "../generated/openapi";
import { Field } from "../components/Field";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function targetStatusLabel(status: BackupTarget["status"]): string {
  switch (status) {
    case "available":
      return "Available";
    case "unavailable":
      return "Unavailable";
    case "not_configured":
      return "Not configured";
    default:
      return honestStatus(status);
  }
}

function runStatusLabel(status: BackupRun["status"]): string {
  switch (status) {
    case "running":
      return "Running";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    default:
      return honestStatus(status);
  }
}

export function BackupsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);

  const [targets, setTargets] = useState<BackupTarget[] | null>(null);
  const [policies, setPolicies] = useState<BackupPolicy[] | null>(null);
  const [runs, setRuns] = useState<BackupRun[] | null>(null);
  const [artifacts, setArtifacts] = useState<BackupArtifact[] | null>(null);
  const [workloads, setWorkloads] = useState<Workload[]>([]);
  const [loadState, setLoadState] = useState<"collecting" | "ready" | "unavailable">("collecting");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [targetName, setTargetName] = useState("");
  const [targetKind, setTargetKind] = useState<CreateBackupTargetRequest["kind"]>("local");
  const [targetLocator, setTargetLocator] = useState("");
  const [targetUsername, setTargetUsername] = useState("");
  const [targetPassword, setTargetPassword] = useState("");

  const [policyName, setPolicyName] = useState("");
  const [policyWorkloadId, setPolicyWorkloadId] = useState("");
  const [policyTargetId, setPolicyTargetId] = useState("");
  const [keepDaily, setKeepDaily] = useState("7");
  const [keepWeekly, setKeepWeekly] = useState("4");
  const [keepMonthly, setKeepMonthly] = useState("3");

  const [runWorkloadId, setRunWorkloadId] = useState("");
  const [runTargetId, setRunTargetId] = useState("");
  const [runPolicyId, setRunPolicyId] = useState("");

  async function reload() {
    const [t, p, r, a, w] = await Promise.all([
      listBackupTargets(),
      listBackupPolicies(),
      listBackupRuns(),
      listBackupArtifacts(),
      listWorkloads(),
    ]);
    setTargets(t.items ?? []);
    setPolicies(p.items ?? []);
    setRuns(r.items ?? []);
    setArtifacts(a.items ?? []);
    setWorkloads(w.items ?? []);
    setLoadState("ready");
  }

  useEffect(() => {
    let cancelled = false;
    setLoadState("collecting");
    setError(null);
    void (async () => {
      try {
        await reload();
      } catch (err) {
        if (!cancelled) {
          setLoadState("unavailable");
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function onCreateTarget() {
    const name = targetName.trim();
    const locator = targetLocator.trim();
    if (!name || !locator) {
      setError("Target name and locator are required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const body: CreateBackupTargetRequest = {
        name,
        kind: targetKind,
        locator,
      };
      const username = targetUsername.trim();
      if (username) {
        body.username = username;
      }
      if (targetPassword) {
        body.password = targetPassword;
      }
      await createBackupTarget(body);
      setTargetName("");
      setTargetLocator("");
      setTargetUsername("");
      setTargetPassword("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Create target failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreatePolicy() {
    const name = policyName.trim();
    if (!name || !policyWorkloadId || !policyTargetId) {
      setError("Policy name, workload, and target are required");
      return;
    }
    const daily = Number(keepDaily);
    const weekly = Number(keepWeekly);
    const monthly = Number(keepMonthly);
    if (![daily, weekly, monthly].every((n) => Number.isInteger(n) && n >= 0)) {
      setError("Retention counts must be non-negative integers");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const body: CreateBackupPolicyRequest = {
        name,
        workload_id: policyWorkloadId,
        target_id: policyTargetId,
        schedule: "nightly",
        keep_daily: daily,
        keep_weekly: weekly,
        keep_monthly: monthly,
      };
      await createBackupPolicy(body);
      setPolicyName("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Create policy failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRunBackup() {
    if (!runWorkloadId || !runTargetId) {
      setError("Workload and target are required to run a backup");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await runBackup({
        workload_id: runWorkloadId,
        target_id: runTargetId,
        ...(runPolicyId ? { policy_id: runPolicyId } : {}),
      });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Backup run failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRestore(artifact: BackupArtifact, mode: "new" | "replace") {
    if (mode === "new") {
      if (
        !window.confirm(
          `Restore artifact ${artifact.id} as a new workload? This creates a new workload UUID. The original workload is left unchanged.`,
        )
      ) {
        return;
      }
    } else if (
      !window.confirm(
        `Replace the existing workload with artifact ${artifact.id}? This overwrites the current workload. Type confirmation is sent as X-Nodal-Confirm: restore.`,
      )
    ) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await restoreBackupArtifact(artifact.id, { mode });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Restore failed");
    } finally {
      setBusy(false);
    }
  }

  const availableTargets = (targets ?? []).filter((t) => t.status === "available");

  return (
    <section className="page page-wide" aria-labelledby="backups-heading">
      <header className="page-header">
        <h1 id="backups-heading">Backups</h1>
        <p className="page-kicker">Backups are independent copies. Snapshots are not backups.</p>
      </header>

      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}

      {loadState === "collecting" ? (
        <p className="banner" role="status">
          Collecting
        </p>
      ) : null}

      {loadState === "unavailable" && !error ? (
        <p className="banner banner-error" role="alert">
          Unavailable
        </p>
      ) : null}

      {loadState === "ready" ? (
        <>
          <article className="panel">
            <h2>Targets</h2>
            {targets == null ? (
              <p>Collecting</p>
            ) : targets.length === 0 ? (
              <p>No backup targets yet.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Kind</th>
                      <th>Locator</th>
                      <th>Status</th>
                      <th>Username</th>
                    </tr>
                  </thead>
                  <tbody>
                    {targets.map((t) => (
                      <tr key={t.id}>
                        <td>{t.name}</td>
                        <td>{t.kind}</td>
                        <td>{t.locator}</td>
                        <td>{targetStatusLabel(t.status)}</td>
                        <td>{t.username || "None"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {mutate ? (
              <div className="stack" style={{ marginTop: "1rem" }}>
                <h3>Add target</h3>
                <Field
                  id="backup-target-name"
                  label="Name"
                  value={targetName}
                  onChange={(e) => setTargetName(e.target.value)}
                  autoComplete="off"
                />
                <div className="field">
                  <label className="field-label" htmlFor="backup-target-kind">
                    Kind
                  </label>
                  <select
                    id="backup-target-kind"
                    className="field-input"
                    value={targetKind}
                    onChange={(e) => setTargetKind(e.target.value as CreateBackupTargetRequest["kind"])}
                  >
                    <option value="local">local</option>
                    <option value="nfs">nfs</option>
                    <option value="smb">smb</option>
                  </select>
                </div>
                <Field
                  id="backup-target-locator"
                  label="Locator"
                  value={targetLocator}
                  onChange={(e) => setTargetLocator(e.target.value)}
                  autoComplete="off"
                  hint="Path or share location for the destination."
                />
                {(targetKind === "nfs" || targetKind === "smb") && (
                  <>
                    <Field
                      id="backup-target-username"
                      label="Username"
                      value={targetUsername}
                      onChange={(e) => setTargetUsername(e.target.value)}
                      autoComplete="off"
                    />
                    <Field
                      id="backup-target-password"
                      label="Password"
                      type="password"
                      value={targetPassword}
                      onChange={(e) => setTargetPassword(e.target.value)}
                      autoComplete="new-password"
                      hint="Write-only. Never shown after save."
                    />
                  </>
                )}
                <div className="btn-row">
                  <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onCreateTarget()}>
                    Add target
                  </button>
                </div>
              </div>
            ) : null}
          </article>

          <article className="panel">
            <h2>Policies</h2>
            {policies == null ? (
              <p>Collecting</p>
            ) : policies.length === 0 ? (
              <p>No backup policies yet.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Workload</th>
                      <th>Target</th>
                      <th>Schedule</th>
                      <th>Retention</th>
                      <th>Last run</th>
                    </tr>
                  </thead>
                  <tbody>
                    {policies.map((p) => (
                      <tr key={p.id}>
                        <td>{p.name}</td>
                        <td>{p.workload_id}</td>
                        <td>{p.target_id}</td>
                        <td>{p.schedule}</td>
                        <td>
                          daily {p.keep_daily}, weekly {p.keep_weekly}, monthly {p.keep_monthly}
                        </td>
                        <td>{formatWhen(p.last_run_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {mutate ? (
              <div className="stack" style={{ marginTop: "1rem" }}>
                <h3>Create policy</h3>
                <Field
                  id="backup-policy-name"
                  label="Name"
                  value={policyName}
                  onChange={(e) => setPolicyName(e.target.value)}
                  autoComplete="off"
                />
                <div className="field">
                  <label className="field-label" htmlFor="backup-policy-workload">
                    Workload
                  </label>
                  <select
                    id="backup-policy-workload"
                    className="field-input"
                    value={policyWorkloadId}
                    onChange={(e) => setPolicyWorkloadId(e.target.value)}
                  >
                    <option value="">Select workload</option>
                    {workloads.map((w) => (
                      <option key={w.id} value={w.id}>
                        {w.name} ({w.id})
                      </option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="backup-policy-target">
                    Target
                  </label>
                  <select
                    id="backup-policy-target"
                    className="field-input"
                    value={policyTargetId}
                    onChange={(e) => setPolicyTargetId(e.target.value)}
                  >
                    <option value="">Select target</option>
                    {(targets ?? []).map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.name} ({targetStatusLabel(t.status)})
                      </option>
                    ))}
                  </select>
                </div>
                <p className="muted">Schedule: nightly</p>
                <Field
                  id="backup-keep-daily"
                  label="Keep daily"
                  type="number"
                  min={0}
                  value={keepDaily}
                  onChange={(e) => setKeepDaily(e.target.value)}
                />
                <Field
                  id="backup-keep-weekly"
                  label="Keep weekly"
                  type="number"
                  min={0}
                  value={keepWeekly}
                  onChange={(e) => setKeepWeekly(e.target.value)}
                />
                <Field
                  id="backup-keep-monthly"
                  label="Keep monthly"
                  type="number"
                  min={0}
                  value={keepMonthly}
                  onChange={(e) => setKeepMonthly(e.target.value)}
                />
                <div className="btn-row">
                  <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onCreatePolicy()}>
                    Create policy
                  </button>
                </div>
              </div>
            ) : null}
          </article>

          <article className="panel">
            <h2>Run backup</h2>
            <p className="muted">Starts a backup run. Status stays Running until the API reports Succeeded or Failed.</p>
            {mutate ? (
              <>
                <div className="field">
                  <label className="field-label" htmlFor="backup-run-workload">
                    Workload
                  </label>
                  <select
                    id="backup-run-workload"
                    className="field-input"
                    value={runWorkloadId}
                    onChange={(e) => setRunWorkloadId(e.target.value)}
                  >
                    <option value="">Select workload</option>
                    {workloads.map((w) => (
                      <option key={w.id} value={w.id}>
                        {w.name} ({w.id})
                      </option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="backup-run-target">
                    Target
                  </label>
                  <select
                    id="backup-run-target"
                    className="field-input"
                    value={runTargetId}
                    onChange={(e) => setRunTargetId(e.target.value)}
                  >
                    <option value="">Select target</option>
                    {availableTargets.map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="backup-run-policy">
                    Policy (optional)
                  </label>
                  <select
                    id="backup-run-policy"
                    className="field-input"
                    value={runPolicyId}
                    onChange={(e) => setRunPolicyId(e.target.value)}
                  >
                    <option value="">None</option>
                    {(policies ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="btn-row">
                  <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onRunBackup()}>
                    Run backup
                  </button>
                </div>
              </>
            ) : (
              <p className="muted">Operator or admin role required to start a backup.</p>
            )}
          </article>

          <article className="panel">
            <h2>Runs</h2>
            {runs == null ? (
              <p>Collecting</p>
            ) : runs.length === 0 ? (
              <p>No backup runs yet.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>Workload</th>
                      <th>Target</th>
                      <th>Status</th>
                      <th>Started</th>
                      <th>Finished</th>
                      <th>Error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {runs.map((r) => (
                      <tr key={r.id}>
                        <td>{r.id}</td>
                        <td>{r.workload_id}</td>
                        <td>{r.target_id}</td>
                        <td>{runStatusLabel(r.status)}</td>
                        <td>{formatWhen(r.started_at)}</td>
                        <td>{formatWhen(r.finished_at)}</td>
                        <td>{r.error || "None"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </article>

          <article className="panel">
            <h2>Artifacts</h2>
            <p className="muted">
              Restore as new creates a new workload UUID. Restore replace overwrites the existing workload and requires
              confirmation.
            </p>
            {artifacts == null ? (
              <p>Collecting</p>
            ) : artifacts.length === 0 ? (
              <p>No backup artifacts yet.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>Workload</th>
                      <th>Size</th>
                      <th>Format</th>
                      <th>Checksum</th>
                      <th>Created</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {artifacts.map((art) => (
                      <tr key={art.id}>
                        <td>{art.id}</td>
                        <td>{art.workload_id}</td>
                        <td>{formatBytes(art.size_bytes)}</td>
                        <td>{art.format}</td>
                        <td className="mono">{art.checksum_sha256}</td>
                        <td>{formatWhen(art.created_at)}</td>
                        <td>
                          {mutate ? (
                            <div className="btn-row">
                              <button
                                className="btn"
                                type="button"
                                disabled={busy}
                                onClick={() => void onRestore(art, "new")}
                              >
                                Restore as new
                              </button>
                              <button
                                className="btn"
                                type="button"
                                disabled={busy}
                                onClick={() => void onRestore(art, "replace")}
                              >
                                Restore replace
                              </button>
                            </div>
                          ) : (
                            <span className="muted">None</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </article>
        </>
      ) : null}
    </section>
  );
}
