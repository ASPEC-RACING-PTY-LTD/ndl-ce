import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  createBackupPolicy,
  createBackupTarget,
  deleteBackupPolicy,
  exportBackupDR,
  getBackupWorkspace,
  listBackupArtifacts,
  listBackupPolicies,
  listBackupRuns,
  listBackupTargets,
  listNodes,
  listWorkloads,
  patchBackupWorkspace,
  previewBackupScope,
  restoreBackupArtifact,
  restoreBackupFile,
  runBackup,
  runBackupPolicy,
  testBackupTarget,
  updateBackupPolicy,
  verifyBackupArtifact,
} from "../api/client";
import type { NodeSummary } from "../api/phase2";
import type { Workload } from "../api/phase5";
import type {
  BackupArtifact,
  BackupPolicy,
  BackupRun,
  BackupScopePreviewResponse,
  BackupTarget,
  BackupWorkspace,
  CreateBackupPolicyRequest,
  CreateBackupTargetRequest,
  RestoreBackupRequest,
} from "../generated/openapi";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
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
    case "untested":
      return "Untested";
    case "authentication_failed":
      return "Authentication failed";
    case "permission_denied":
      return "Permission denied";
    case "bucket_unavailable":
      return "Bucket unavailable";
    case "degraded":
      return "Degraded";
    default:
      return honestStatus(status);
  }
}

function targetAllowsRun(t: BackupTarget): boolean {
  if (t.status === "available" || t.status === "untested" || t.status === "degraded") {
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
    case "succeeded_with_warnings":
      return "Succeeded with warnings";
    case "failed":
      return "Failed";
    case "interrupted":
      return "Interrupted";
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

function retentionLabel(p: BackupPolicy): string {
  return `${p.keep_daily} daily, ${p.keep_weekly} weekly, ${p.keep_monthly} monthly`;
}

function policyScopeLabel(policy: BackupPolicy, workloads: Workload[]): string {
  if (policy.scope === "all") {
    const n = workloads.length;
    if (n === 0) {
      return "All workloads";
    }
    return `All workloads (${n} current)`;
  }
  const ids = policy.workload_ids?.length ? policy.workload_ids : policy.workload_id ? [policy.workload_id] : [];
  if (ids.length === 0) {
    return "No workloads selected";
  }
  const names = ids.map((id) => workloads.find((w) => w.id === id)?.name ?? id);
  if (names.length <= 3) {
    return names.join(", ");
  }
  return `${names.slice(0, 2).join(", ")} +${names.length - 2}`;
}

type CaptureMode = "smart" | "custom" | "full";

type WorkloadSelection = {
  selected?: string[];
  deselected?: string[];
  includes?: string[];
  excludes?: string[];
};

type ScopeCategory = {
  id: string;
  kind: string;
  label: string;
  paths: string[];
  bytes: number;
  selected: boolean;
  default_on: boolean;
  reproducible?: boolean;
  warnings?: string[];
};

function captureModeLabel(mode: string | undefined): string {
  switch (mode) {
    case "custom":
      return "Custom";
    case "full":
      return "Full Machine / Full LXC";
    default:
      return "Smart Application Data";
  }
}

function artifactBackupType(art: BackupArtifact): string {
  if (art.capture_mode_label) {
    return art.capture_mode_label;
  }
  if (art.capture_mode) {
    return captureModeLabel(art.capture_mode);
  }
  if (art.format === "ndl-cab") {
    return "Smart Application Data";
  }
  return "Legacy";
}

function protectionLabel(art: BackupArtifact): string {
  if (art.protection) {
    return art.protection;
  }
  if (art.format !== "ndl-cab") {
    return art.verify_status === "verified" ? "Legacy verified" : "Legacy";
  }
  switch (art.remote_state) {
    case "queued":
      return "Queued";
    case "uploading":
      return "Uploading";
    case "verifying":
      return "Remote verifying";
    case "protected":
      return "Protected";
    case "failed":
      return "Failed";
    default:
      return art.local_complete ? "Local complete" : "Capturing";
  }
}

function parseSelections(raw: unknown): Record<string, WorkloadSelection> {
  if (!raw || typeof raw !== "object") {
    return {};
  }
  const wrap = raw as { workloads?: Record<string, WorkloadSelection> };
  if (wrap.workloads && typeof wrap.workloads === "object") {
    return { ...wrap.workloads };
  }
  return {};
}

function asScopeCategories(items: Record<string, unknown>[] | undefined): ScopeCategory[] {
  if (!items) {
    return [];
  }
  return items.map((it) => ({
    id: String(it.id ?? ""),
    kind: String(it.kind ?? ""),
    label: String(it.label ?? it.id ?? "Detected"),
    paths: Array.isArray(it.paths) ? it.paths.map((p) => String(p)) : [],
    bytes: typeof it.bytes === "number" ? it.bytes : 0,
    selected: Boolean(it.selected),
    default_on: Boolean(it.default_on),
    reproducible: Boolean(it.reproducible),
    warnings: Array.isArray(it.warnings) ? it.warnings.map((w) => String(w)) : undefined,
  }));
}

function planLabel(plan: BackupRun["plan"]): string {
  if (!plan) {
    return "None";
  }
  const included = plan.included?.length ?? 0;
  const skipped = plan.skipped?.length ?? 0;
  const method = plan.method ?? "copy";
  const warning = plan.warning ? `; ${plan.warning}` : "";
  if (skipped > 0) {
    return `${method}: ${included} included, ${skipped} skipped${warning}`;
  }
  return `${method}${warning}`;
}

function latestPolicyRun(runs: BackupRun[], policyId: string): BackupRun | undefined {
  return runs.find((r) => r.policy_id === policyId);
}

type Dialog =
  | { kind: "target" }
  | { kind: "policy"; policy?: BackupPolicy }
  | { kind: "adhoc" }
  | { kind: "history"; policy: BackupPolicy }
  | { kind: "artifact"; artifact: BackupArtifact }
  | { kind: "delete-policy"; policy: BackupPolicy }
  | { kind: "workspace" };

export function BackupsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);

  const [targets, setTargets] = useState<BackupTarget[] | null>(null);
  const [policies, setPolicies] = useState<BackupPolicy[] | null>(null);
  const [runs, setRuns] = useState<BackupRun[] | null>(null);
  const [artifacts, setArtifacts] = useState<BackupArtifact[] | null>(null);
  const [workloads, setWorkloads] = useState<Workload[]>([]);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [loadState, setLoadState] = useState<"collecting" | "ready" | "unavailable">("collecting");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState<Dialog | null>(null);

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
  const [policyScope, setPolicyScope] = useState<"all" | "selected">("selected");
  const [policyCaptureMode, setPolicyCaptureMode] = useState<CaptureMode>("smart");
  const [policyWorkloadIds, setPolicyWorkloadIds] = useState<string[]>([]);
  const [policySelections, setPolicySelections] = useState<Record<string, WorkloadSelection>>({});
  const [policyTargetId, setPolicyTargetId] = useState("");
  const [keepDaily, setKeepDaily] = useState("7");
  const [keepWeekly, setKeepWeekly] = useState("4");
  const [keepMonthly, setKeepMonthly] = useState("3");
  const [workloadQuery, setWorkloadQuery] = useState("");
  const [showAdvancedPaths, setShowAdvancedPaths] = useState(false);
  const [scopePreview, setScopePreview] = useState<BackupScopePreviewResponse | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [workspace, setWorkspace] = useState<BackupWorkspace | null>(null);
  const [maxLocalGiB, setMaxLocalGiB] = useState("50");
  const [minFreeGiB, setMinFreeGiB] = useState("10");
  const [uploadWorkers, setUploadWorkers] = useState("4");

  const [runWorkloadId, setRunWorkloadId] = useState("");
  const [runTargetId, setRunTargetId] = useState("");
  const [filePath, setFilePath] = useState("/etc/hostname");
  const [filePreview, setFilePreview] = useState<string | null>(null);
  const [restoreNodeId, setRestoreNodeId] = useState("");

  async function reload() {
    const [t, p, r, a, w, n] = await Promise.all([
      listBackupTargets(),
      listBackupPolicies(),
      listBackupRuns(),
      listBackupArtifacts(),
      listWorkloads(),
      listNodes(),
    ]);
    setTargets(t.items ?? []);
    setPolicies(p.items ?? []);
    setRuns(r.items ?? []);
    setArtifacts(a.items ?? []);
    setWorkloads(w.items ?? []);
    setNodes(n ?? []);
    try {
      const ws = await getBackupWorkspace();
      setWorkspace(ws);
      if (ws.max_local_bytes) {
        setMaxLocalGiB(String(Math.round(ws.max_local_bytes / 1024 / 1024 / 1024)));
      }
      if (ws.min_host_free_bytes) {
        setMinFreeGiB(String(Math.round(ws.min_host_free_bytes / 1024 / 1024 / 1024)));
      }
      if (ws.upload_workers) {
        setUploadWorkers(String(ws.upload_workers));
      }
    } catch {
      setWorkspace(null);
    }
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

  function resetTargetForm() {
    setTargetName("");
    setTargetKind("local");
    setTargetLocator("");
    setTargetUsername("");
    setTargetPassword("");
    setTargetEndpoint("");
    setTargetBucket("");
    setTargetPrefix("");
    setTargetRegion("");
    setTargetNoCheckBucket(true);
  }

  function openPolicyDialog(policy?: BackupPolicy) {
    if (policy) {
      setPolicyName(policy.name);
      setPolicyScope(policy.scope === "all" ? "all" : "selected");
      setPolicyCaptureMode(policy.capture_mode === "custom" || policy.capture_mode === "full" ? policy.capture_mode : "smart");
      setPolicyWorkloadIds(policy.workload_ids?.length ? [...policy.workload_ids] : policy.workload_id ? [policy.workload_id] : []);
      setPolicySelections(parseSelections(policy.scope_json));
      setPolicyTargetId(policy.target_id);
      setKeepDaily(String(policy.keep_daily));
      setKeepWeekly(String(policy.keep_weekly));
      setKeepMonthly(String(policy.keep_monthly));
    } else {
      setPolicyName("");
      setPolicyScope("selected");
      setPolicyCaptureMode("smart");
      setPolicyWorkloadIds([]);
      setPolicySelections({});
      setPolicyTargetId(targets?.[0]?.id ?? "");
      setKeepDaily("7");
      setKeepWeekly("4");
      setKeepMonthly("3");
    }
    setWorkloadQuery("");
    setShowAdvancedPaths(false);
    setScopePreview(null);
    setPreviewError(null);
    setDialog({ kind: "policy", policy });
  }

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
      resetTargetForm();
      setDialog(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Create target failed");
    } finally {
      setBusy(false);
    }
  }

  async function onTestTarget(id: string) {
    setBusy(true);
    setError(null);
    try {
      await testBackupTarget(id);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Test connection failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSavePolicy(existing?: BackupPolicy) {
    const name = policyName.trim();
    if (!name || !policyTargetId) {
      setError("Policy name and target are required");
      return;
    }
    if (policyScope === "selected" && policyWorkloadIds.length === 0) {
      setError("Select at least one workload, or choose All workloads if you intend to cover the entire fleet");
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
        scope: policyScope,
        capture_mode: policyCaptureMode,
        target_id: policyTargetId,
        schedule: "nightly",
        keep_daily: daily,
        keep_weekly: weekly,
        keep_monthly: monthly,
      };
      if (policyScope === "selected") {
        body.workload_ids = policyWorkloadIds;
      }
      if (Object.keys(policySelections).length > 0) {
        body.scope_json = { workloads: policySelections };
      }
      if (existing) {
        await updateBackupPolicy(existing.id, body);
      } else {
        await createBackupPolicy(body);
      }
      setDialog(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Save policy failed");
    } finally {
      setBusy(false);
    }
  }

  const previewWorkloadIds = policyScope === "all" ? workloads.map((w) => w.id) : policyWorkloadIds;

  useEffect(() => {
    if (dialog?.kind !== "policy") {
      return;
    }
    if (previewWorkloadIds.length === 0) {
      setScopePreview(null);
      setPreviewError(null);
      setPreviewBusy(false);
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(() => {
      setPreviewBusy(true);
      setPreviewError(null);
      void previewBackupScope({
        workload_ids: previewWorkloadIds,
        capture_mode: policyCaptureMode,
        scope_json: { workloads: policySelections },
      })
        .then((res) => {
          if (!cancelled) {
            setScopePreview(res);
          }
        })
        .catch((err: unknown) => {
          if (!cancelled) {
            setScopePreview(null);
            setPreviewError(err instanceof Error ? err.message : "Scope preview unavailable");
          }
        })
        .finally(() => {
          if (!cancelled) {
            setPreviewBusy(false);
          }
        });
    }, 250);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [dialog?.kind, policyCaptureMode, policySelections, previewWorkloadIds.join(",")]);

  function togglePreviewItem(workloadId: string, itemId: string, on: boolean) {
    setPolicySelections((cur) => {
      const prev = cur[workloadId] ?? {};
      const selected = new Set(prev.selected ?? []);
      const deselected = new Set(prev.deselected ?? []);
      if (on) {
        selected.add(itemId);
        deselected.delete(itemId);
      } else {
        deselected.add(itemId);
        selected.delete(itemId);
      }
      return { ...cur, [workloadId]: { ...prev, selected: [...selected], deselected: [...deselected] } };
    });
  }

  function updateAdvancedPaths(workloadId: string, field: "includes" | "excludes", text: string) {
    const paths = text
      .split("\n")
      .map((p) => p.trim())
      .filter(Boolean);
    setPolicySelections((cur) => ({
      ...cur,
      [workloadId]: { ...(cur[workloadId] ?? {}), [field]: paths },
    }));
  }

  async function onSaveWorkspace() {
    const maxLocal = Number(maxLocalGiB);
    const minFree = Number(minFreeGiB);
    const workers = Number(uploadWorkers);
    if (![maxLocal, minFree, workers].every((n) => Number.isFinite(n) && n >= 0)) {
      setError("Workspace limits must be non-negative numbers");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const ws = await patchBackupWorkspace({
        max_local_bytes: Math.round(maxLocal * 1024 * 1024 * 1024),
        min_host_free_bytes: Math.round(minFree * 1024 * 1024 * 1024),
        upload_workers: Math.max(1, Math.round(workers)),
      });
      setWorkspace(ws);
      setDialog(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Save workspace failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRunPolicy(policy: BackupPolicy) {
    setBusy(true);
    setError(null);
    try {
      await runBackupPolicy(policy.id);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Backup run failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRunAdhoc() {
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
      });
      setDialog(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Backup run failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDeletePolicy(policy: BackupPolicy) {
    setBusy(true);
    setError(null);
    try {
      await deleteBackupPolicy(policy.id);
      setDialog(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Delete policy failed");
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
      const body: RestoreBackupRequest = { mode };
      if (restoreNodeId) {
        body.target_node_id = restoreNodeId;
      }
      await restoreBackupArtifact(artifact.id, body);
      setDialog(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Restore failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDRExport() {
    setBusy(true);
    setError(null);
    try {
      const out = await exportBackupDR();
      const blob = new Blob([JSON.stringify(out, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `ndl-dr-export-${out.cluster_id}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "DR export failed");
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
  const targetById = useMemo(() => new Map((targets ?? []).map((t) => [t.id, t])), [targets]);
  const filteredWorkloads = workloads.filter((w) => {
    const q = workloadQuery.trim().toLowerCase();
    if (!q) {
      return true;
    }
    return w.name.toLowerCase().includes(q) || w.id.toLowerCase().includes(q) || w.kind.toLowerCase().includes(q);
  });
  const recentRuns = (runs ?? []).slice(0, 12);
  const recentArtifacts = artifacts ?? [];

  return (
    <section className="page page-wide" aria-labelledby="backups-heading">
      <PageHeader
        id="backups-heading"
        title="Backups"
        kicker="Backups are independent copies. Snapshots are not backups."
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => openPolicyDialog()}>
                Create policy
              </button>
              <button
                className="btn"
                type="button"
                onClick={() => {
                  resetTargetForm();
                  setDialog({ kind: "target" });
                }}
              >
                Add target
              </button>
              <button className="btn" type="button" onClick={() => setDialog({ kind: "adhoc" })}>
                Ad-hoc backup
              </button>
              <button className="btn" type="button" onClick={() => setDialog({ kind: "workspace" })}>
                Workspace
              </button>
            </div>
          ) : null
        }
      />

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
          <div className="summary-grid">
            <article className="summary-card">
              <span className="label">Policies</span>
              <span className="value">{policies?.length ?? 0}</span>
              <span className="meta">{(runs ?? []).some((r) => r.status === "running") ? "Backup running" : "Idle"}</span>
            </article>
            <article className="summary-card">
              <span className="label">Targets</span>
              <span className="value">{targets?.length ?? 0}</span>
              <span className="meta">{(targets ?? []).filter((t) => t.status === "available").length} healthy</span>
            </article>
            <article className="summary-card">
              <span className="label">Local repository</span>
              <span className="value">{workspace?.repo_bytes != null ? formatBytes(workspace.repo_bytes) : "Unknown"}</span>
              <span className="meta">
                {workspace?.pending_uploads ? `${workspace.pending_uploads} queued upload${workspace.pending_uploads === 1 ? "" : "s"}` : "No pending uploads"}
              </span>
            </article>
            <article className="summary-card">
              <span className="label">Recent failures</span>
              <span className="value">{(runs ?? []).filter((r) => r.status === "failed").length}</span>
              <span className="meta">Last {recentRuns.length} runs</span>
            </article>
          </div>
          <div className="card-grid">
          <section className="section-block" aria-labelledby="backup-policies-heading">
            <div className="page-header-row">
              <h2 id="backup-policies-heading">Policies</h2>
            </div>
            <div className="card-grid">
              {policies == null ? (
                <article className="panel dashboard-card empty-card">
                  <p>Collecting</p>
                </article>
              ) : policies.length === 0 ? (
                <article className="panel dashboard-card empty-card">
                  <p className="empty-title">No backup policies yet</p>
                  <p className="muted">A policy is the thing you schedule and run. Select the workloads it protects. Smart Application Data is the default backup scope.</p>
                  {mutate ? (
                    <div className="btn-row">
                      <button className="btn btn-primary" type="button" onClick={() => openPolicyDialog()}>
                        Create policy
                      </button>
                    </div>
                  ) : null}
                </article>
              ) : (
                policies.map((p) => {
                  const latest = latestPolicyRun(runs ?? [], p.id);
                  const target = targetById.get(p.target_id);
                  return (
                    <article className="panel dashboard-card" key={p.id}>
                      <h3>{p.name}</h3>
                      <dl className="card-meta">
                        <div>
                          <dt>Workloads</dt>
                          <dd>{policyScopeLabel(p, workloads)}</dd>
                        </div>
                        <div>
                          <dt>Backup scope</dt>
                          <dd>{captureModeLabel(p.capture_mode)}</dd>
                        </div>
                        <div>
                          <dt>Target</dt>
                          <dd>{target?.name ?? p.target_id}</dd>
                        </div>
                        <div>
                          <dt>Schedule</dt>
                          <dd>{p.schedule === "nightly" ? "Nightly" : p.schedule}</dd>
                        </div>
                        <div>
                          <dt>Retention</dt>
                          <dd>{retentionLabel(p)}</dd>
                        </div>
                        <div>
                          <dt>Latest</dt>
                          <dd>{latest ? runStatusLabel(latest.status) : "No runs yet"}</dd>
                        </div>
                        <div>
                          <dt>Last run</dt>
                          <dd>{formatWhen(p.last_run_at ?? latest?.started_at)}</dd>
                        </div>
                      </dl>
                      {p.scope === "all" ? (
                        <p className="muted">
                          All workloads covers every current and future eligible guest. Extra disks, iSCSI, and
                          distributed volumes are skipped when the root disk can still be backed up. Directory
                          system containers use Backup Engine V2 for Smart, Custom, and Full Machine scope.
                        </p>
                      ) : null}
                      {mutate ? (
                        <div className="btn-row">
                          <button className="btn btn-primary btn-sm" type="button" disabled={busy} onClick={() => void onRunPolicy(p)}>
                            Run now
                          </button>
                          <button className="btn btn-sm" type="button" onClick={() => setDialog({ kind: "history", policy: p })}>
                            History
                          </button>
                          <button className="btn btn-sm" type="button" onClick={() => openPolicyDialog(p)}>
                            Edit
                          </button>
                          <button className="btn btn-sm" type="button" onClick={() => setDialog({ kind: "delete-policy", policy: p })}>
                            Delete
                          </button>
                        </div>
                      ) : (
                        <p className="muted">Operator or admin role required to run or edit.</p>
                      )}
                    </article>
                  );
                })
              )}
            </div>
          </section>

          <section className="section-block" aria-labelledby="backup-targets-heading">
            <div className="page-header-row">
              <h2 id="backup-targets-heading">Targets</h2>
            </div>
            <div className="card-grid">
              {targets == null ? (
                <article className="panel dashboard-card empty-card">
                  <p>Collecting</p>
                </article>
              ) : targets.length === 0 ? (
                <article className="panel dashboard-card empty-card">
                  <p className="empty-title">No backup targets yet</p>
                  <p className="muted">Destinations such as local, NFS, SMB, R2, S3, B2, or MinIO.</p>
                  {mutate ? (
                    <div className="btn-row">
                      <button
                        className="btn btn-primary"
                        type="button"
                        onClick={() => {
                          resetTargetForm();
                          setDialog({ kind: "target" });
                        }}
                      >
                        Add target
                      </button>
                    </div>
                  ) : null}
                </article>
              ) : (
                targets.map((t) => (
                  <article className="panel dashboard-card" key={t.id}>
                    <div className="page-header-row">
                      <h3>{t.name}</h3>
                      <span className="kind-chip">{t.kind}</span>
                    </div>
                    <dl className="card-meta">
                      <div>
                        <dt>Status</dt>
                        <dd>
                          <span className="status-pill">{targetStatusLabel(t.status)}</span>
                        </dd>
                      </div>
                      <div>
                        <dt>{isObjectKind(t.kind) ? "Bucket" : "Locator"}</dt>
                        <dd>{isObjectKind(t.kind) ? t.bucket || t.locator : t.locator}</dd>
                      </div>
                      {t.username ? (
                        <div>
                          <dt>{isObjectKind(t.kind) ? "Access key id" : "Username"}</dt>
                          <dd>{t.username}</dd>
                        </div>
                      ) : null}
                    </dl>
                    {mutate && isObjectKind(t.kind) ? (
                      <div className="btn-row">
                        <button
                          className="btn btn-sm"
                          type="button"
                          disabled={busy}
                          onClick={() => void onTestTarget(t.id)}
                        >
                          Test connection
                        </button>
                      </div>
                    ) : null}
                  </article>
                ))
              )}
            </div>
          </section>
          </div>

          <section className="section-block" aria-labelledby="backup-runs-heading">
            <div className="page-header-row">
              <h2 id="backup-runs-heading">Recent runs</h2>
            </div>
            {runs == null || runs.length === 0 ? (
              <div className="card-grid">
                <article className="panel dashboard-card empty-card">
                  {runs == null ? (
                    <p>Collecting</p>
                  ) : (
                    <>
                      <p className="empty-title">No backup runs yet</p>
                      <p className="muted">Run now on a policy to see fleet-wide history here.</p>
                    </>
                  )}
                </article>
              </div>
            ) : (
              <div className="card-grid card-grid-table">
                <article className="panel dashboard-card table-card">
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Workload</th>
                          <th>Target</th>
                          <th>Status</th>
                          <th>Plan</th>
                          <th>Error</th>
                          <th>Transferred</th>
                          <th>Incremental</th>
                          <th>Started</th>
                        </tr>
                      </thead>
                      <tbody>
                        {recentRuns.map((r) => (
                          <tr key={r.id}>
                            <td>{workloads.find((w) => w.id === r.workload_id)?.name ?? r.workload_id}</td>
                            <td>{targetById.get(r.target_id)?.name ?? r.target_id}</td>
                            <td>{runStatusLabel(r.status)}</td>
                            <td>{planLabel(r.plan)}</td>
                            <td>{r.error || "None"}</td>
                            <td>{r.transferred_bytes != null ? formatBytes(r.transferred_bytes) : "None"}</td>
                            <td>{r.incremental ? "Yes" : "No"}</td>
                            <td>{formatWhen(r.started_at)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </article>
              </div>
            )}
          </section>

          <section className="section-block" aria-labelledby="backup-artifacts-heading">
            <div className="page-header-row">
              <h2 id="backup-artifacts-heading">Artifacts</h2>
              {mutate ? (
                <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void onDRExport()}>
                  Export DR metadata
                </button>
              ) : null}
            </div>
            {artifacts == null || artifacts.length === 0 ? (
              <div className="card-grid">
                <article className="panel dashboard-card empty-card">
                  {artifacts == null ? (
                    <p>Collecting</p>
                  ) : (
                    <>
                      <p className="empty-title">No backup artifacts yet</p>
                      <p className="muted">Completed backups appear here for restore and verification.</p>
                    </>
                  )}
                </article>
              </div>
            ) : (
              <div className="card-grid card-grid-table">
                <article className="panel dashboard-card table-card">
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Workload</th>
                          <th>Backup type</th>
                          <th>Protection</th>
                          <th>Size</th>
                          <th>Encrypted</th>
                          <th>Verify</th>
                          <th>Created</th>
                          <th>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {recentArtifacts.map((art) => (
                          <tr key={art.id}>
                            <td>{workloads.find((w) => w.id === art.workload_id)?.name ?? art.workload_id}</td>
                            <td>{artifactBackupType(art)}</td>
                            <td>
                              <span className="status-pill">{protectionLabel(art)}</span>
                            </td>
                            <td>{formatBytes(art.logical_bytes || art.size_bytes)}</td>
                            <td>{art.encrypted ? "Client-side" : "No"}</td>
                            <td>{verifyStatusLabel(art.verify_status)}</td>
                            <td>{formatWhen(art.created_at)}</td>
                            <td>
                              {mutate ? (
                                <button className="btn btn-sm" type="button" onClick={() => setDialog({ kind: "artifact", artifact: art })}>
                                  Restore or verify
                                </button>
                              ) : (
                                <span className="muted">None</span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </article>
              </div>
            )}
          </section>
        </>
      ) : null}

      <ConfirmDialog
        open={dialog?.kind === "target"}
        title="Add backup target"
        confirmLabel="Add target"
        wide
        confirmDisabled={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => void onCreateTarget()}
      >
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
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "policy"}
        title={dialog?.kind === "policy" && dialog.policy ? "Edit backup policy" : "Create backup policy"}
        confirmLabel={dialog?.kind === "policy" && dialog.policy ? "Save policy" : "Create policy"}
        wide
        confirmDisabled={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => void onSavePolicy(dialog?.kind === "policy" ? dialog.policy : undefined)}
      >
        <Field
          id="backup-policy-name"
          label="Name"
          value={policyName}
          onChange={(e) => setPolicyName(e.target.value)}
          autoComplete="off"
        />
        <fieldset className="field">
          <legend className="field-label">Workloads</legend>
          <div className="scope-options">
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-scope"
                value="selected"
                checked={policyScope === "selected"}
                onChange={() => setPolicyScope("selected")}
              />
              Selected workloads
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-scope"
                value="all"
                checked={policyScope === "all"}
                onChange={() => setPolicyScope("all")}
              />
              All workloads
            </label>
          </div>
          <p className="field-hint">
            Select the workloads this policy protects. A workload can belong to more than one policy. All workloads
            must be chosen deliberately and covers current and future eligible guests.
          </p>
        </fieldset>
        {policyScope === "selected" ? (
          <div className="stack">
            <Field
              id="backup-policy-workload-search"
              label="Search workloads"
              value={workloadQuery}
              onChange={(e) => setWorkloadQuery(e.target.value)}
              autoComplete="off"
            />
            <div className="picker-list">
              {filteredWorkloads.length === 0 ? (
                <p className="muted">No matching workloads.</p>
              ) : (
                filteredWorkloads.map((w) => {
                  const checked = policyWorkloadIds.includes(w.id);
                  return (
                    <label className="field-check" key={w.id}>
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => {
                          setPolicyWorkloadIds((cur) => (checked ? cur.filter((id) => id !== w.id) : [...cur, w.id]));
                        }}
                      />
                      {w.name} <span className="muted">({w.kind})</span>
                    </label>
                  );
                })
              )}
            </div>
          </div>
        ) : null}
        <fieldset className="field">
          <legend className="field-label">Backup scope</legend>
          <div className="scope-options">
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="smart"
                checked={policyCaptureMode === "smart"}
                onChange={() => setPolicyCaptureMode("smart")}
              />
              Smart Application Data
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="custom"
                checked={policyCaptureMode === "custom"}
                onChange={() => setPolicyCaptureMode("custom")}
              />
              Custom
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="full"
                checked={policyCaptureMode === "full"}
                onChange={() => setPolicyCaptureMode("full")}
              />
              Full Machine / Full LXC
            </label>
          </div>
          {policyCaptureMode === "smart" ? (
            <p className="field-hint">
              Recommended. Protects the data required to recover the workload, not every reproducible byte inside it.
            </p>
          ) : null}
          {policyCaptureMode === "custom" ? (
            <p className="field-hint">Choose detected categories, then add raw include or exclude paths if you need them.</p>
          ) : null}
          {policyCaptureMode === "full" ? (
            <p className="banner banner-warn" role="status">
              Full Machine / Full LXC protects the complete recoverable filesystem. It uses Backup Engine V2 (chunking,
              deduplication, compression, encryption) and consumes substantially more local storage, remote storage, and
              backup bandwidth. It is not selected by default.
            </p>
          ) : null}
        </fieldset>
        <div className="stack" aria-live="polite">
          <h3 className="field-label">Backup scope preview</h3>
          {previewWorkloadIds.length === 0 ? (
            <p className="muted">Select workloads to preview detected persistent and reproducible data.</p>
          ) : previewBusy ? (
            <p className="muted">Collecting scope preview from filesystem metadata.</p>
          ) : previewError ? (
            <p className="muted">{previewError}. You can still save the policy; capture will inventory at run time.</p>
          ) : scopePreview ? (
            <div className="scope-preview">
              <p className="muted">
                Protected data: {formatBytes(scopePreview.protected_bytes ?? 0)}. Excluded or reproducible:{" "}
                {formatBytes(scopePreview.excluded_bytes ?? 0)}. Full filesystem: {formatBytes(scopePreview.full_bytes ?? 0)}.
              </p>
              {(scopePreview.items ?? []).map((wl) => {
                const categories = asScopeCategories(wl.items);
                const persistent = categories.filter((it) => !it.reproducible && it.kind !== "technical");
                const repro = categories.filter((it) => it.reproducible || it.kind === "technical");
                const workloadId = String(wl.workload_id ?? "");
                const sel = policySelections[workloadId] ?? {};
                return (
                  <article className="scope-preview-card" key={workloadId || wl.workload_name}>
                    <h4>{wl.workload_name || workloadId}</h4>
                    <p className="muted">
                      Protected {formatBytes(wl.protected_bytes ?? 0)} · Excluded {formatBytes(wl.excluded_bytes ?? 0)}
                    </p>
                    {persistent.length > 0 ? <p className="field-label">Detected persistent data</p> : null}
                    <div className="picker-list" role="group" aria-label={`${wl.workload_name || "workload"} persistent data`}>
                      {persistent.map((it) => (
                        <label className="field-check" key={it.id}>
                          <input
                            type="checkbox"
                            checked={it.selected}
                            disabled={policyCaptureMode === "full"}
                            onChange={(e) => togglePreviewItem(workloadId, it.id, e.target.checked)}
                          />
                          <span>
                            {it.label}
                            {it.paths[0] ? <span className="muted"> {it.paths[0]}</span> : null}
                            <span className="muted"> {formatBytes(it.bytes)}</span>
                            {it.warnings?.map((w) => (
                              <span className="scope-warning" key={w}>
                                {" "}
                                {w}
                              </span>
                            ))}
                          </span>
                        </label>
                      ))}
                    </div>
                    {repro.length > 0 ? <p className="field-label">Detected reproducible or excluded data</p> : null}
                    {repro.length > 0 ? (
                      <div className="picker-list" role="group" aria-label={`${wl.workload_name || "workload"} excluded data`}>
                        {repro.map((it) => (
                          <label className="field-check" key={it.id}>
                            <input
                              type="checkbox"
                              checked={it.selected}
                              disabled={policyCaptureMode === "full"}
                              onChange={(e) => togglePreviewItem(workloadId, it.id, e.target.checked)}
                            />
                            <span>
                              {it.label}
                              {it.paths[0] ? <span className="muted"> {it.paths[0]}</span> : null}
                              <span className="muted"> {formatBytes(it.bytes)}</span>
                              {it.warnings?.map((w) => (
                                <span className="scope-warning" key={w}>
                                  {" "}
                                  {w}
                                </span>
                              ))}
                            </span>
                          </label>
                        ))}
                      </div>
                    ) : null}
                    {policyCaptureMode === "custom" || showAdvancedPaths ? (
                      <div className="stack">
                        <div className="field">
                          <label className="field-label" htmlFor={`backup-include-${workloadId}`}>
                            Additional include paths
                          </label>
                          <textarea
                            id={`backup-include-${workloadId}`}
                            className="field-input"
                            rows={3}
                            value={(sel.includes ?? []).join("\n")}
                            onChange={(e) => updateAdvancedPaths(workloadId, "includes", e.target.value)}
                          />
                          <p className="field-hint">One guest path per line. Advanced users only.</p>
                        </div>
                        <div className="field">
                          <label className="field-label" htmlFor={`backup-exclude-${workloadId}`}>
                            Additional exclude paths
                          </label>
                          <textarea
                            id={`backup-exclude-${workloadId}`}
                            className="field-input"
                            rows={3}
                            value={(sel.excludes ?? []).join("\n")}
                            onChange={(e) => updateAdvancedPaths(workloadId, "excludes", e.target.value)}
                          />
                          <p className="field-hint">One guest path per line. Overlapping a detected database is warned.</p>
                        </div>
                      </div>
                    ) : null}
                  </article>
                );
              })}
              {policyCaptureMode !== "custom" ? (
                <label className="field-check">
                  <input type="checkbox" checked={showAdvancedPaths} onChange={(e) => setShowAdvancedPaths(e.target.checked)} />
                  Show advanced path rules
                </label>
              ) : null}
            </div>
          ) : null}
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
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "adhoc"}
        title="Ad-hoc backup"
        confirmLabel="Run backup"
        confirmDisabled={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => void onRunAdhoc()}
      >
        <p className="muted">Starts a one-off copy without changing a policy. Prefer Run now on a policy when you have one.</p>
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
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "history"}
        title={dialog?.kind === "history" ? `${dialog.policy.name} history` : "History"}
        hideFooter
        wide
        onClose={() => setDialog(null)}
        onConfirm={() => setDialog(null)}
      >
        {dialog?.kind === "history" ? (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Workload</th>
                  <th>Status</th>
                  <th>Plan</th>
                  <th>Transferred</th>
                  <th>Started</th>
                  <th>Finished</th>
                  <th>Error</th>
                </tr>
              </thead>
              <tbody>
                {(runs ?? [])
                  .filter((r) => r.policy_id === dialog.policy.id)
                  .map((r) => (
                    <tr key={r.id}>
                      <td>{workloads.find((w) => w.id === r.workload_id)?.name ?? r.workload_id}</td>
                      <td>{runStatusLabel(r.status)}</td>
                      <td>{planLabel(r.plan)}</td>
                      <td>{r.transferred_bytes != null ? formatBytes(r.transferred_bytes) : "None"}</td>
                      <td>{formatWhen(r.started_at)}</td>
                      <td>{formatWhen(r.finished_at)}</td>
                      <td>{r.error || "None"}</td>
                    </tr>
                  ))}
              </tbody>
            </table>
            {(runs ?? []).every((r) => r.policy_id !== dialog.policy.id) ? <p className="muted">No runs for this policy yet.</p> : null}
          </div>
        ) : null}
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "artifact"}
        title="Restore or verify"
        hideFooter
        wide
        onClose={() => setDialog(null)}
        onConfirm={() => setDialog(null)}
      >
        {dialog?.kind === "artifact" ? (
          <>
            <dl className="card-meta">
              <div>
                <dt>Backup type</dt>
                <dd>{artifactBackupType(dialog.artifact)}</dd>
              </div>
              <div>
                <dt>Protection</dt>
                <dd>{protectionLabel(dialog.artifact)}</dd>
              </div>
            </dl>
            {dialog.artifact.capture_mode === "full" || dialog.artifact.capture_mode_label === "Full Machine" ? (
              <p className="muted">
                This Full Machine restore is the closest practical equivalent to a complete LXC filesystem restore,
                minus technical mounts. It still uses Backup Engine V2. The restored workload stays stopped until you
                start it.
              </p>
            ) : dialog.artifact.format === "ndl-cab" ? (
              <p className="muted">
                This is a {artifactBackupType(dialog.artifact)} backup, not a byte-for-byte full-machine copy. Restore
                recreates the workload from its Blueprint, then restores protected configuration and application state.
                The restored workload stays stopped until you start it.
              </p>
            ) : (
              <p className="muted">Legacy backup format. Restore uses the original archive path.</p>
            )}
            <p className="muted">
              Restore as new creates a new workload UUID. Restore replace overwrites the existing workload and requires
              confirmation. Catalog without verify stays Unverified. Throwaway restore tests must not touch the source
              workload. Dest node is the control node unless a worker is selected. Restore onto a worker records the
              catalog and does not start a second copy on this control node.
            </p>
            <div className="field">
              <label className="field-label" htmlFor="backup-restore-node">
                Restore dest node
              </label>
              <select
                id="backup-restore-node"
                className="field-input"
                value={restoreNodeId}
                onChange={(e) => setRestoreNodeId(e.target.value)}
              >
                <option value="">This control node</option>
                {nodes
                  .filter((n) => n.status !== "revoked")
                  .map((n) => (
                    <option key={n.id} value={n.id}>
                      {n.name || n.id} ({n.role || "node"})
                    </option>
                  ))}
              </select>
            </div>
            <Field
              id="backup-restore-file-path"
              label="Guest file path"
              value={filePath}
              onChange={(e) => setFilePath(e.target.value)}
              autoComplete="off"
              hint="Used by Restore file. Traversal is refused. libguestfs must be installed on the agent."
            />
            {filePreview ? (
              <pre className="mono" aria-live="polite">
                {filePreview}
              </pre>
            ) : null}
            <div className="btn-row">
              <button className="btn" type="button" disabled={busy} onClick={() => void onVerify(dialog.artifact, "open")}>
                Verify
              </button>
              <button className="btn" type="button" disabled={busy} onClick={() => void onVerify(dialog.artifact, "throwaway")}>
                Verify throwaway
              </button>
              <button className="btn" type="button" disabled={busy} onClick={() => void onRestoreFile(dialog.artifact)}>
                Restore file
              </button>
              <button className="btn" type="button" disabled={busy} onClick={() => void onRestore(dialog.artifact, "new")}>
                Restore as new
              </button>
              <button className="btn" type="button" disabled={busy} onClick={() => void onRestore(dialog.artifact, "replace")}>
                Restore replace
              </button>
            </div>
          </>
        ) : null}
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "delete-policy"}
        title="Delete backup policy"
        confirmLabel="Delete"
        danger
        confirmDisabled={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => {
          if (dialog?.kind === "delete-policy") {
            void onDeletePolicy(dialog.policy);
          }
        }}
      >
        {dialog?.kind === "delete-policy" ? (
          <p>Delete policy {dialog.policy.name}? Catalogued artifacts are kept.</p>
        ) : null}
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog?.kind === "workspace"}
        title="Backup workspace"
        confirmLabel="Save workspace"
        wide
        confirmDisabled={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => void onSaveWorkspace()}
      >
        <p className="muted">
          Local Backup Engine V2 repository bounds. Capture fails before the host disk is exhausted. Remote upload is
          asynchronous and does not block the next capture.
        </p>
        <p className="muted">
          Repository: {workspace?.root || "/var/lib/ndl/backup-repo"}. Used {workspace?.repo_bytes != null ? formatBytes(workspace.repo_bytes) : "unknown"}.
          Host free {workspace?.host_free_bytes != null ? formatBytes(workspace.host_free_bytes) : "unknown"}.
        </p>
        <Field
          id="backup-workspace-max-local"
          label="Local repository ceiling (GiB)"
          type="number"
          min={0}
          value={maxLocalGiB}
          onChange={(e) => setMaxLocalGiB(e.target.value)}
        />
        <Field
          id="backup-workspace-min-free"
          label="Host free-space reserve (GiB)"
          type="number"
          min={0}
          value={minFreeGiB}
          onChange={(e) => setMinFreeGiB(e.target.value)}
        />
        <Field
          id="backup-workspace-upload-workers"
          label="Upload workers"
          type="number"
          min={1}
          value={uploadWorkers}
          onChange={(e) => setUploadWorkers(e.target.value)}
        />
      </ConfirmDialog>
    </section>
  );
}
