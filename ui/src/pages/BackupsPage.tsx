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
  restoreBackupFile,
  runBackup,
  verifyBackupArtifact,
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

const OBJECT_KINDS = ["s3", "r2", "aws", "b2", "minio"] as const;

function isObjectKind(kind: string): boolean {
  return (OBJECT_KINDS as readonly string[]).includes(kind);
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

function targetAllowsRun(t: BackupTarget): boolean {
  if (t.status === "available") {
    return true;
  }
  return isObjectKind(t.kind) && Boolean(t.no_check_bucket) && t.status === "not_configured";
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

function verifyStatusLabel(status: BackupArtifact["verify_status"] | undefined): string {
  switch (status) {
    case "verified":
      return "Verified";
    case "failed":
      return "Failed";
    default:
      return "Unverified";
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
  const [targetEndpoint, setTargetEndpoint] = useState("");
  const [targetBucket, setTargetBucket] = useState("");
  const [targetPrefix, setTargetPrefix] = useState("");
  const [targetRegion, setTargetRegion] = useState("");
  const [targetNoCheckBucket, setTargetNoCheckBucket] = useState(true);

  const [policyName, setPolicyName] = useState("");
  const [policyWorkloadId, setPolicyWorkloadId] = useState("");
  const [policyTargetId, setPolicyTargetId] = useState("");
  const [keepDaily, setKeepDaily] = useState("7");
  const [keepWeekly, setKeepWeekly] = useState("4");
  const [keepMonthly, setKeepMonthly] = useState("3");

  const [runWorkloadId, setRunWorkloadId] = useState("");
  const [runTargetId, setRunTargetId] = useState("");
  const [runPolicyId, setRunPolicyId] = useState("");
  const [filePath, setFilePath] = useState("/etc/hostname");
  const [filePreview, setFilePreview] = useState<string | null>(null);

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
    const object = isObjectKind(targetKind);
    if (!name) {
      setError("Target name is required");
      return;
    }
    if (object) {
      if (!targetEndpoint.trim() || !targetBucket.trim() || !targetUsername.trim() || !targetPassword) {
        setError("Object targets need endpoint, bucket, access key id, and secret access key");
        return;
      }
    } else if (!targetLocator.trim()) {
      setError("Target name and locator are required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const body: CreateBackupTargetRequest = {
        name,
        kind: targetKind,
      };
      if (object) {
        body.endpoint = targetEndpoint.trim();
        body.bucket = targetBucket.trim();
        const prefix = targetPrefix.trim();
        if (prefix) {
          body.prefix = prefix;
        }
        const region = targetRegion.trim();
        if (region) {
          body.region = region;
        }
        body.username = targetUsername.trim();
        body.password = targetPassword;
        body.no_check_bucket = targetNoCheckBucket;
      } else {
        body.locator = targetLocator.trim();
        const username = targetUsername.trim();
        if (username) {
          body.username = username;
        }
        if (targetPassword) {
          body.password = targetPassword;
        }
      }
      await createBackupTarget(body);
      setTargetName("");
      setTargetLocator("");
      setTargetUsername("");
      setTargetPassword("");
      setTargetEndpoint("");
      setTargetBucket("");
      setTargetPrefix("");
      setTargetRegion("");
      setTargetNoCheckBucket(true);
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

  async function onVerify(artifact: BackupArtifact, mode: "open" | "throwaway") {
    setBusy(true);
    setError(null);
    try {
      await verifyBackupArtifact(artifact.id, { mode });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Verify failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRestoreFile(artifact: BackupArtifact) {
    const path = filePath.trim();
    if (!path.startsWith("/") || path.includes("..")) {
      setError("Guest path must be absolute without traversal");
      return;
    }
    setBusy(true);
    setError(null);
    setFilePreview(null);
    try {
      const out = await restoreBackupFile(artifact.id, { path });
      try {
        setFilePreview(atob(out.content_base64));
      } catch {
        setFilePreview("(binary)");
      }
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "File restore failed");
    } finally {
      setBusy(false);
    }
  }

  const runnableTargets = (targets ?? []).filter(targetAllowsRun);
  const objectForm = isObjectKind(targetKind);

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
                      <th>Bucket</th>
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
                        <td>{t.bucket || "None"}</td>
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
                    <option value="s3">s3</option>
                    <option value="r2">r2</option>
                    <option value="aws">aws</option>
                    <option value="b2">b2</option>
                    <option value="minio">minio</option>
                  </select>
                </div>
                {objectForm ? (
                  <>
                    <Field
                      id="backup-target-endpoint"
                      label="Endpoint"
                      value={targetEndpoint}
                      onChange={(e) => setTargetEndpoint(e.target.value)}
                      autoComplete="off"
                      hint="HTTPS URL. HTTP is allowed only for MinIO fixtures."
                    />
                    <Field
                      id="backup-target-bucket"
                      label="Bucket"
                      value={targetBucket}
                      onChange={(e) => setTargetBucket(e.target.value)}
                      autoComplete="off"
                    />
                    <Field
                      id="backup-target-prefix"
                      label="Prefix"
                      value={targetPrefix}
                      onChange={(e) => setTargetPrefix(e.target.value)}
                      autoComplete="off"
                      hint="Optional object key prefix."
                    />
                    <Field
                      id="backup-target-region"
                      label="Region"
                      value={targetRegion}
                      onChange={(e) => setTargetRegion(e.target.value)}
                      autoComplete="off"
                      hint="Optional. R2 defaults to auto."
                    />
                    <Field
                      id="backup-target-username"
                      label="Access key id"
                      value={targetUsername}
                      onChange={(e) => setTargetUsername(e.target.value)}
                      autoComplete="off"
                    />
                    <Field
                      id="backup-target-password"
                      label="Secret access key"
                      type="password"
                      value={targetPassword}
                      onChange={(e) => setTargetPassword(e.target.value)}
                      autoComplete="new-password"
                      hint="Write-only. Never shown after save. Client-side encryption is generated if you omit a key. Bucket SSE is extra, not sufficient."
                    />
                    <label className="field-check">
                      <input
                        id="backup-target-no-check-bucket"
                        type="checkbox"
                        checked={targetNoCheckBucket}
                        onChange={(e) => setTargetNoCheckBucket(e.target.checked)}
                      />
                      Skip bucket probe (no_check_bucket). Status stays Not configured until a successful upload.
                    </label>
                  </>
                ) : (
                  <>
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
                    {runnableTargets.map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.name} ({targetStatusLabel(t.status)})
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
                      <th>Transferred</th>
                      <th>Incremental</th>
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
                        <td>{r.transferred_bytes != null ? formatBytes(r.transferred_bytes) : "None"}</td>
                        <td>{r.incremental ? "Yes" : "No"}</td>
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
              confirmation. Catalog without verify stays Unverified. Throwaway restore tests must not touch the source
              workload.
            </p>
            {mutate ? (
              <Field
                id="backup-restore-file-path"
                label="Guest file path"
                value={filePath}
                onChange={(e) => setFilePath(e.target.value)}
                autoComplete="off"
                hint="Used by Restore file. Traversal is refused. libguestfs must be installed on the agent."
              />
            ) : null}
            {filePreview ? (
              <pre className="mono" aria-live="polite">
                {filePreview}
              </pre>
            ) : null}
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
                      <th>Transferred</th>
                      <th>Encrypted</th>
                      <th>Format</th>
                      <th>Checksum</th>
                      <th>Verify</th>
                      <th>Last tested</th>
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
                        <td>{art.transferred_bytes != null ? formatBytes(art.transferred_bytes) : "None"}</td>
                        <td>{art.encrypted ? "Client-side" : "No"}</td>
                        <td>{art.format}</td>
                        <td className="mono">{art.checksum_sha256}</td>
                        <td>{verifyStatusLabel(art.verify_status)}</td>
                        <td>{formatWhen(art.last_tested_at)}</td>
                        <td>{formatWhen(art.created_at)}</td>
                        <td>
                          {mutate ? (
                            <div className="btn-row">
                              <button
                                className="btn"
                                type="button"
                                disabled={busy}
                                onClick={() => void onVerify(art, "open")}
                              >
                                Verify
                              </button>
                              <button
                                className="btn"
                                type="button"
                                disabled={busy}
                                onClick={() => void onVerify(art, "throwaway")}
                              >
                                Verify throwaway
                              </button>
                              <button
                                className="btn"
                                type="button"
                                disabled={busy}
                                onClick={() => void onRestoreFile(art)}
                              >
                                Restore file
                              </button>
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
