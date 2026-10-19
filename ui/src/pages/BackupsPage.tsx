import { confirmAction } from "../confirm";
import { useCallback, useEffect, useMemo, useState } from "react";
import { LoadingState } from "../components/EmptyState";
import { DeleteButton } from "../components/DeleteButton";
import {
  deleteBackupArtifact,
  deleteWorkloadBackups,
  deleteBackupTargetWithBackups,
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
  protectBackupArtifact,
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
import { BackupLocationPicker, BackupStoragePanel } from "../components/BackupStorage";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { formatBytes, formatRelative, formatWhen, honestStatus } from "../format";
import { useSession } from "../session";
import { ErrorNotice } from "../components/ErrorNotice";
import { BackupGitHolds } from "../components/BackupGitHolds";
import { PlatformBackupPanel } from "../components/PlatformBackup";
import { RecoveryWorkloads } from "../components/RecoveryWorkloads";
import { KeepImages } from "../components/KeepImages";
import { BackupSizes } from "../components/BackupSizes";
import { Box, BoxDock, BoxesProvider, CreateMenu, useBoxes, type BoxInfo } from "../components/BackupBoxes";
import { WorkloadFiles } from "../components/WorkloadFiles";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin") || roles?.includes("admin"));
}

function isAdmin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin"));
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

function retentionLabel(p: BackupPolicy): string {
  return `${p.keep_daily} daily, ${p.keep_weekly} weekly, ${p.keep_monthly} monthly`;
}

function policyScopeLabel(policy: BackupPolicy, workloads: Workload[]): string {
  if (policy.scope === "all") {
    const excluded = (policy.excluded_workload_ids ?? []).map((id) => workloads.find((w) => w.id === id)?.name ?? id);
    const n = workloads.length - excluded.length;
    const base = workloads.length === 0 ? "All workloads" : `All workloads (${n} current)`;
    return excluded.length ? `${base}, except ${excluded.join(", ")}` : base;
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
    case "disk":
      return "VM disk";
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

// shortBackupType is the one-word form of a backup type for tight tables.
function shortBackupType(label: string): string {
  if (label.startsWith("Full")) {
    return "Full";
  }
  if (label.startsWith("Smart")) {
    return "Smart";
  }
  return label;
}

// uploadProgress describes an upload still in flight, e.g. "42% uploaded".
function uploadProgress(art: BackupArtifact): string {
  const total = art.upload_total_bytes ?? 0;
  if (!total || art.remote_state === "protected" || art.remote_state === "failed") {
    return "";
  }
  const pct = Math.min(100, Math.floor(((art.uploaded_bytes ?? 0) / total) * 100));
  return `${pct}% of ${formatBytes(total)} uploaded`;
}

// compressionSaving compares the new data a backup added with what it stored.
function compressionSaving(art: BackupArtifact): string {
  const raw = art.new_raw_bytes ?? 0;
  const stored = art.physical_new_data ?? 0;
  if (!raw || !stored) {
    return "";
  }
  const saved = Math.max(0, Math.round((1 - stored / raw) * 100));
  return `${formatBytes(raw)} → ${formatBytes(stored)} (${saved}% saved)`;
}

function protectionLabel(art: BackupArtifact): string {
  if (art.protection) {
    return art.protection;
  }
  if (art.format !== "ndl-cab" && art.format !== "zfs" && art.format !== "qcow2") {
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

function protectionBucket(art: BackupArtifact): "protected" | "localPending" | "queued" | "uploading" | "failed" | "legacy" {
  const label = protectionLabel(art);
  switch (label) {
    case "Protected":
      return "protected";
    case "Queued":
      return "queued";
    case "Uploading":
    case "Remote verifying":
      return "uploading";
    case "Failed":
      return "failed";
    case "Legacy":
    case "Legacy verified":
      return "legacy";
    default:
      return "localPending";
  }
}

function formatDurationNS(ns: number | undefined): string {
  if (ns == null || ns <= 0) {
    return "None";
  }
  const ms = ns / 1e6;
  if (ms < 1000) {
    return `${Math.max(1, Math.round(ms))} ms`;
  }
  const seconds = ms / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(1)} s`;
  }
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
}

function consistencyLabel(value: string | undefined): string {
  switch (value) {
    case "application-consistent":
      return "Application-consistent";
    case "crash-consistent":
      return "Crash-consistent";
    default:
      return value ? honestStatus(value) : "Crash-consistent";
  }
}

function blueprintSummary(raw: BackupArtifact["blueprint"]): string {
  if (!raw || typeof raw !== "object") {
    return "None";
  }
  const bp = raw as {
    name?: string;
    os?: string;
    arch?: string;
    workload_type?: string;
    nesting?: boolean;
    docker?: { compose_projects?: string[]; uncertainty?: string[] };
  };
  const parts = [bp.name, bp.os, bp.arch, bp.workload_type].filter((v): v is string => Boolean(v));
  if (bp.nesting) {
    parts.push("nesting");
  }
  if (bp.docker?.compose_projects?.length) {
    parts.push(`compose ${bp.docker.compose_projects.join(", ")}`);
  }
  if (bp.docker?.uncertainty?.length) {
    parts.push("docker uncertain");
  }
  return parts.length ? parts.join(" · ") : "Recorded";
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

type CategoryGroup = {
  kind: string;
  label: string;
  bytes: number;
  selected: boolean;
  ids: string[];
  warnings: string[];
};

function kindGroupLabel(kind: string): string {
  switch (kind) {
    case "database":
      return "Databases";
    case "docker-volume":
      return "Docker volumes";
    case "docker-bind":
      return "Docker bind mounts";
    case "application":
      return "Application data";
    case "configuration":
      return "Configuration";
    case "certificate":
      return "Certificates";
    case "git":
      return "Git repositories";
    case "reproducible":
      return "Rebuildable (images, caches, toolchains, game files)";
    case "media":
      return "Media files (video and audio)";
    case "uncertain":
      return "Unclassified data";
    case "logs":
      return "Logs";
    case "os":
      return "Operating system";
    case "technical":
      return "Runtime filesystems";
    default:
      return "Other";
  }
}

function groupCategories(items: ScopeCategory[]): CategoryGroup[] {
  const order = [
    "database",
    "docker-volume",
    "docker-bind",
    "application",
    "configuration",
    "certificate",
    "uncertain",
    "git",
    "media",
    "reproducible",
    "logs",
    "os",
    "technical",
  ];
  const map = new Map<string, CategoryGroup>();
  for (const it of items) {
    const cur = map.get(it.kind);
    if (!cur) {
      map.set(it.kind, {
        kind: it.kind,
        label: kindGroupLabel(it.kind),
        bytes: it.bytes,
        selected: it.selected,
        ids: [it.id],
        warnings: [...(it.warnings ?? [])],
      });
      continue;
    }
    cur.bytes += it.bytes;
    cur.selected = cur.selected || it.selected;
    cur.ids.push(it.id);
    for (const warning of it.warnings ?? []) {
      if (!cur.warnings.includes(warning)) {
        cur.warnings.push(warning);
      }
    }
  }
  return [...map.values()].sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind));
}

function latestPolicyRun(runs: BackupRun[], policyId: string): BackupRun | undefined {
  return runs.find((r) => r.policy_id === policyId);
}

type RunRange = "latest" | "day" | "all";

// A gap this long between run starts separates one backup round from the
// next; a round's runs start one after another.
const ROUND_GAP_MS = 30 * 60 * 1000;

/**
 * filterRuns keeps, by default, the backup round that is running now or the
 * last one that ran: runs that started in an unbroken chain back from the
 * newest, plus anything still running. Older runs stay in the list behind
 * the wider ranges.
 */
export function filterRuns(runs: BackupRun[], range: RunRange, failedOnly: boolean, now = Date.now()): BackupRun[] {
  const at = (r: BackupRun) => Date.parse(r.started_at ?? "") || 0;
  const sorted = [...runs].sort((a, b) => at(b) - at(a));
  let keep = sorted;
  if (range === "day") {
    keep = sorted.filter((r) => now - at(r) <= 24 * 60 * 60 * 1000 || r.status === "running");
  } else if (range === "latest" && sorted.length > 0) {
    let start = at(sorted[0]);
    for (const r of sorted.slice(1)) {
      if (start - at(r) > ROUND_GAP_MS) {
        break;
      }
      start = at(r);
    }
    keep = sorted.filter((r) => at(r) >= start || r.status === "running");
  }
  return failedOnly ? keep.filter((r) => r.status === "failed" || r.status === "interrupted") : keep;
}

function runTone(status: BackupRun["status"]): string {
  switch (status) {
    case "succeeded":
      return "is-ok";
    case "running":
      return "is-active";
    case "failed":
    case "interrupted":
      return "is-danger";
    case "succeeded_with_warnings":
      return "is-warn";
    default:
      return "";
  }
}

function protectionTone(art: BackupArtifact): string {
  switch (protectionBucket(art)) {
    case "protected":
      return "is-ok";
    case "failed":
      return "is-danger";
    case "uploading":
    case "queued":
      return "is-active";
    case "localPending":
      return "is-warn";
    default:
      return "";
  }
}

const PLAN_METHODS: Record<string, string> = {
  "content-addressed": "Copied the files, storing only data not already backed up",
  "directory-archive": "Copied the files into one archive",
  "zfs-send": "Sent a ZFS snapshot of the disk",
  "qcow2-copy": "Copied the virtual machine disk",
};

const PLAN_CONSISTENCY: Record<string, string> = {
  "live-copy": "copied while running",
  application: "the application flushed its data first",
  "cgroup-freezer": "paused for a moment while it was copied",
  stopped: "copied while stopped",
  "zfs-snapshot": "copied from a ZFS snapshot",
  "lvm-snapshot": "copied from an LVM snapshot",
  "qcow2-overlay": "copied from a frozen disk image",
};

/** RunDetails explains one backup run, including how it chose what to copy. */
function RunDetails({ run, workload, target }: { run: BackupRun; workload: string; target: string }) {
  const plan = run.plan;
  const items = (list?: { kind?: string; id?: string; role?: string; reason?: string }[]) =>
    (list ?? []).map((it, i) => (
      <li key={`${it.id ?? it.kind ?? ""}-${i}`}>
        {[it.role, it.kind, it.id].filter(Boolean).join(" ")}
        {it.reason ? <span className="bk-hint"> ({it.reason})</span> : null}
      </li>
    ));
  return (
    <dl className="bk-dl">
      <dt>Workload</dt>
      <dd>{workload}</dd>
      <dt>Target</dt>
      <dd>{target}</dd>
      <dt>Status</dt>
      <dd>
        {runStatusLabel(run.status)}
        {run.incremental ? ", incremental (only changes were copied)" : ""}
      </dd>
      {run.error ? (
        <>
          <dt>Error</dt>
          <dd className="bk-error-text">{run.error}</dd>
        </>
      ) : null}
      <dt>Uploaded</dt>
      <dd>{run.transferred_bytes != null ? formatBytes(run.transferred_bytes) : "Nothing reported"}</dd>
      <dt>Started</dt>
      <dd>{formatWhen(run.started_at)}</dd>
      <dt>Finished</dt>
      <dd>{run.finished_at ? formatWhen(run.finished_at) : "Not finished"}</dd>
      <dt>How it was copied</dt>
      <dd>
        {plan ? (
          <>
            {PLAN_METHODS[plan.method ?? ""] ?? plan.method ?? "Copied"}
            {plan.consistency ? `, ${PLAN_CONSISTENCY[plan.consistency] ?? plan.consistency}` : ""}.
            {plan.warning ? <div className="bk-hint">{plan.warning}</div> : null}
          </>
        ) : (
          "Not recorded for this run."
        )}
      </dd>
      {plan?.included?.length ? (
        <>
          <dt>Included</dt>
          <dd>
            <ul className="bk-plain">{items(plan.included)}</ul>
          </dd>
        </>
      ) : null}
      {plan?.skipped?.length ? (
        <>
          <dt>Skipped</dt>
          <dd>
            <ul className="bk-plain">{items(plan.skipped)}</ul>
          </dd>
        </>
      ) : null}
    </dl>
  );
}

type Dialog =
  | { kind: "target" }
  | { kind: "policy"; policy?: BackupPolicy }
  | { kind: "adhoc" }
  | { kind: "history"; policy: BackupPolicy }
  | { kind: "artifact"; artifact: BackupArtifact }
  | { kind: "delete-policy"; policy: BackupPolicy }
  | { kind: "workspace" }
  | { kind: "run"; run: BackupRun };

// Boxes of the page, in page order, with the short name their minimized
// button shows.
const BOXES: BoxInfo[] = [
  { id: "images", short: "Image copies" },
  { id: "files", short: "Files & rules" },
  { id: "sizes", short: "Size" },
  { id: "policies", short: "Policies" },
  { id: "storage", short: "Storage" },
  { id: "runs", short: "Runs" },
  { id: "restore", short: "Restore points" },
  { id: "dr", short: "Recovery" },
];

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
  const [targetPoolId, setTargetPoolId] = useState("");
  const [targetAllowRoot, setTargetAllowRoot] = useState(false);
  const [offsiteDaily, setOffsiteDaily] = useState("0");
  const [offsiteWeekly, setOffsiteWeekly] = useState("0");
  const [offsiteMonthly, setOffsiteMonthly] = useState("0");

  const [policyName, setPolicyName] = useState("");
  const [policyScope, setPolicyScope] = useState<"all" | "selected">("selected");
  const [policyExcludedIds, setPolicyExcludedIds] = useState<string[]>([]);
  const [policyCaptureMode, setPolicyCaptureMode] = useState<CaptureMode>("full");
  const [policyWorkloadIds, setPolicyWorkloadIds] = useState<string[]>([]);
  const [policySelections, setPolicySelections] = useState<Record<string, WorkloadSelection>>({});
  const [policyTargetId, setPolicyTargetId] = useState("");
  const [keepDaily, setKeepDaily] = useState("7");
  const [keepWeekly, setKeepWeekly] = useState("4");
  const [keepMonthly, setKeepMonthly] = useState("3");
  const [workloadQuery, setWorkloadQuery] = useState("");
  const [showAdvancedPaths, setShowAdvancedPaths] = useState(false);
  const [customWorkloadId, setCustomWorkloadId] = useState("");
  const [customPreview, setCustomPreview] = useState<BackupScopePreviewResponse | null>(null);
  const [customPreviewBusy, setCustomPreviewBusy] = useState(false);
  const [customPreviewError, setCustomPreviewError] = useState<string | null>(null);
  const [workspace, setWorkspace] = useState<BackupWorkspace | null>(null);
  const [maxLocalGiB, setMaxLocalGiB] = useState("50");
  const [minFreeGiB, setMinFreeGiB] = useState("10");
  const [uploadWorkers, setUploadWorkers] = useState("4");
  const [captureConcurrency, setCaptureConcurrency] = useState("1");
  const [bandwidthMBps, setBandwidthMBps] = useState("0");
  const [cacheRetentionHours, setCacheRetentionHours] = useState("0");
  const [compressionLevel, setCompressionLevel] = useState("19");
  const [ignoreUncommittedGit, setIgnoreUncommittedGit] = useState(true);

  const [runWorkloadId, setRunWorkloadId] = useState("");
  const [runTargetId, setRunTargetId] = useState("");
  const [runCaptureMode, setRunCaptureMode] = useState<CaptureMode>("full");
  const [filePath, setFilePath] = useState("/etc/hostname");
  const [filePreview, setFilePreview] = useState<string | null>(null);
  const [restoreNodeId, setRestoreNodeId] = useState("");
  const [artifactQuery, setArtifactQuery] = useState("");
  const [runRange, setRunRange] = useState<RunRange>("latest");
  const [runsFailedOnly, setRunsFailedOnly] = useState(false);
  // refreshKey reloads panels that fetch their own data after a change here.
  const [refreshKey, setRefreshKey] = useState(0);
  const boxes = useBoxes(["images", "files"]);

  // Everything loads at once. Backups render as soon as the backup lists
  // arrive; workloads, nodes and workspace settings fill in when they do.
  async function reload() {
    function applyWorkspace(ws: BackupWorkspace) {
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
      if (ws.capture_concurrency) {
        setCaptureConcurrency(String(ws.capture_concurrency));
      }
      if (ws.bandwidth_limit_bps != null) {
        setBandwidthMBps(String(Math.round((ws.bandwidth_limit_bps / 1024 / 1024) * 10) / 10));
      }
      if (ws.cache_retention_hours != null) {
        setCacheRetentionHours(String(ws.cache_retention_hours));
      }
      if (ws.compression_level != null) {
        setCompressionLevel(String(ws.compression_level || 19));
      }
      setIgnoreUncommittedGit(ws.ignore_uncommitted_git !== false);
    }

    const backups = Promise.all([
      listBackupTargets(),
      listBackupPolicies(),
      listBackupRuns(),
      listBackupArtifacts(),
    ]).then(([t, p, r, a]) => {
      setTargets(t.items ?? []);
      setPolicies(p.items ?? []);
      setRuns(r.items ?? []);
      setArtifacts(a.items ?? []);
      setLoadState("ready");
    });
    const workloadsLoad = listWorkloads().then((w) => setWorkloads(w.items ?? []));
    const nodesLoad = listNodes().then((n) => setNodes(n ?? []));
    const workspaceLoad = getBackupWorkspace()
      .then(applyWorkspace)
      .catch(() => setWorkspace(null));
    await Promise.all([backups, workloadsLoad, nodesLoad, workspaceLoad]);
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
    setTargetPoolId("");
    setTargetAllowRoot(false);
  }

  function openPolicyDialog(policy?: BackupPolicy) {
    if (policy) {
      setPolicyName(policy.name);
      setPolicyScope(policy.scope === "all" ? "all" : "selected");
      setPolicyCaptureMode(policy.capture_mode === "custom" || policy.capture_mode === "smart" ? policy.capture_mode : "full");
      setPolicyWorkloadIds(policy.workload_ids?.length ? [...policy.workload_ids] : policy.workload_id ? [policy.workload_id] : []);
      setPolicyExcludedIds([...(policy.excluded_workload_ids ?? [])]);
      setPolicySelections(parseSelections(policy.scope_json));
      setPolicyTargetId(policy.target_id);
      setKeepDaily(String(policy.keep_daily));
      setKeepWeekly(String(policy.keep_weekly));
      setKeepMonthly(String(policy.keep_monthly));
      setOffsiteDaily(String(policy.offsite_keep_daily ?? 0));
      setOffsiteWeekly(String(policy.offsite_keep_weekly ?? 0));
      setOffsiteMonthly(String(policy.offsite_keep_monthly ?? 0));
    } else {
      setPolicyName("");
      setPolicyScope("selected");
      setPolicyCaptureMode("full");
      setPolicyWorkloadIds([]);
      setPolicyExcludedIds([]);
      setPolicySelections({});
      setPolicyTargetId(targets?.[0]?.id ?? "");
      setKeepDaily("7");
      setKeepWeekly("4");
      setKeepMonthly("3");
      setOffsiteDaily("0");
      setOffsiteWeekly("0");
      setOffsiteMonthly("0");
    }
    setWorkloadQuery("");
    setShowAdvancedPaths(false);
    setCustomWorkloadId("");
    setCustomPreview(null);
    setCustomPreviewBusy(false);
    setCustomPreviewError(null);
    setDialog({ kind: "policy", policy });
  }

  async function onDeleteArtifact(art: BackupArtifact) {
    if (!confirmAction(`Delete the backup from ${formatWhen(art.created_at)}? Its data is removed and it cannot be restored.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteBackupArtifact(art.id, true);
      // Drop the row at once; the reload brings back the counts and runs.
      setArtifacts((cur) => (cur ?? []).filter((a) => a.id !== art.id));
      setDialog((cur) => (cur?.kind === "artifact" && cur.artifact.id === art.id ? null : cur));
      setRefreshKey((k) => k + 1);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not delete the backup");
    } finally {
      setBusy(false);
    }
  }

  async function afterDelete() {
    setRefreshKey((k) => k + 1);
    try {
      await reload();
    } catch {
      // The next load shows the result.
    }
  }

  async function onDeleteWorkloadBackups(id: string, name: string) {
    if (!confirmAction(`Delete every backup of ${name}, on this host and in cloud storage? Protected backups are kept.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteWorkloadBackups(id);
      setArtifacts((cur) => (cur ?? []).filter((a) => a.workload_id !== id || a.protected));
      await afterDelete();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not delete the backups");
    } finally {
      setBusy(false);
    }
  }

  async function onRemoveTargetWithBackups(t: BackupTarget) {
    if (
      !confirmAction(
        `Remove ${t.name} and delete every backup No-dal recorded on it? Backups whose data cannot be reached any more are dropped from the list too. This cannot be undone.`,
      )
    ) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteBackupTargetWithBackups(t.id);
      setRefreshKey((k) => k + 1);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not remove the target");
    } finally {
      setBusy(false);
    }
  }

  async function onToggleProtect(art: BackupArtifact) {
    setBusy(true);
    setError(null);
    try {
      await protectBackupArtifact(art.id, !art.protected);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not change protection");
    } finally {
      setBusy(false);
    }
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
    } else if (targetKind === "local" ? !targetPoolId && !targetLocator.trim() : !targetLocator.trim()) {
      setError(targetKind === "local" ? "Choose a storage pool or enter a folder" : "Target name and locator are required");
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
        if (targetKind === "local" && targetPoolId) {
          body.pool_id = targetPoolId;
        } else {
          body.locator = targetLocator.trim();
        }
        if (targetKind === "local" && targetAllowRoot) {
          body.allow_root_filesystem = true;
        }
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
    const offDaily = Number(offsiteDaily);
    const offWeekly = Number(offsiteWeekly);
    const offMonthly = Number(offsiteMonthly);
    if (![daily, weekly, monthly, offDaily, offWeekly, offMonthly].every((n) => Number.isInteger(n) && n >= 0)) {
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
        offsite_keep_daily: offDaily,
        offsite_keep_weekly: offWeekly,
        offsite_keep_monthly: offMonthly,
      };
      if (policyScope === "selected") {
        body.workload_ids = policyWorkloadIds;
      } else {
        body.excluded_workload_ids = policyExcludedIds;
      }
      if (policyCaptureMode === "custom" && Object.keys(policySelections).length > 0) {
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

  function setCaptureMode(mode: CaptureMode) {
    setPolicyCaptureMode(mode);
    if (mode !== "custom") {
      setCustomWorkloadId("");
      setCustomPreview(null);
      setCustomPreviewBusy(false);
      setCustomPreviewError(null);
    }
  }

  async function loadCustomPreview(workloadId: string) {
    setCustomWorkloadId(workloadId);
    setCustomPreviewBusy(true);
    setCustomPreviewError(null);
    try {
      const res = await previewBackupScope({
        workload_ids: [workloadId],
        capture_mode: "custom",
        scope_json: { workloads: policySelections },
      });
      setCustomPreview(res);
    } catch (err) {
      setCustomPreview(null);
      setCustomPreviewError(err instanceof Error ? err.message : "Scope preview unavailable");
    } finally {
      setCustomPreviewBusy(false);
    }
  }

  function togglePreviewItems(workloadId: string, itemIds: string[], on: boolean) {
    setPolicySelections((cur) => {
      const prev = cur[workloadId] ?? {};
      const selected = new Set(prev.selected ?? []);
      const deselected = new Set(prev.deselected ?? []);
      for (const itemId of itemIds) {
        if (on) {
          selected.add(itemId);
          deselected.delete(itemId);
        } else {
          deselected.add(itemId);
          selected.delete(itemId);
        }
      }
      return { ...cur, [workloadId]: { ...prev, selected: [...selected], deselected: [...deselected] } };
    });
    setCustomPreview((cur) => {
      if (!cur?.items) {
        return cur;
      }
      const ids = new Set(itemIds);
      return {
        ...cur,
        items: cur.items.map((wl) => {
          if (String(wl.workload_id ?? "") !== workloadId || !wl.items) {
            return wl;
          }
          return {
            ...wl,
            items: wl.items.map((raw) => {
              const it = raw as { id?: string; selected?: boolean };
              if (!ids.has(String(it.id ?? ""))) {
                return raw;
              }
              return { ...raw, selected: on };
            }),
          };
        }),
      };
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
    const slots = Number(captureConcurrency);
    const bandwidth = Number(bandwidthMBps);
    const cacheHours = Number(cacheRetentionHours);
    if (![maxLocal, minFree, workers, slots, bandwidth, cacheHours].every((n) => Number.isFinite(n) && n >= 0)) {
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
        capture_concurrency: Math.max(1, Math.round(slots)),
        bandwidth_limit_bps: Math.round(bandwidth * 1024 * 1024),
        cache_retention_hours: Math.max(0, Math.round(cacheHours)),
        compression_level: Number(compressionLevel),
        // Only a SuperAdmin may change it; others never send it.
        ...(isAdmin(roles) && ignoreUncommittedGit !== (workspace?.ignore_uncommitted_git !== false)
          ? { ignore_uncommitted_git: ignoreUncommittedGit }
          : {}),
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

  async function onTogglePause(policy: BackupPolicy) {
    setBusy(true);
    setError(null);
    try {
      await updateBackupPolicy(policy.id, {
        name: policy.name,
        scope: policy.scope,
        workload_ids: policy.workload_ids,
        target_id: policy.target_id,
        schedule: policy.schedule,
        keep_daily: policy.keep_daily,
        keep_weekly: policy.keep_weekly,
        keep_monthly: policy.keep_monthly,
        capture_mode: policy.capture_mode,
        scope_json: policy.scope_json,
        offsite_keep_daily: policy.offsite_keep_daily,
        offsite_keep_weekly: policy.offsite_keep_weekly,
        offsite_keep_monthly: policy.offsite_keep_monthly,
        paused: !policy.paused,
      } as Parameters<typeof updateBackupPolicy>[1]);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not change the schedule");
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
        capture_mode: runCaptureMode,
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
        !confirmAction(
          `Restore artifact ${artifact.id} as a new workload? This creates a new workload UUID. The original workload is left unchanged.`,
        )
      ) {
        return;
      }
    } else if (
      !confirmAction(
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
  // Only VMs and system containers can be backed up; OCI containers and
  // other kinds are never offered.
  const backupWorkloads = workloads.filter((w) => w.kind === "vm" || w.kind === "system-container");
  const filteredWorkloads = backupWorkloads.filter((w) => {
    const q = workloadQuery.trim().toLowerCase();
    if (!q) {
      return true;
    }
    return w.name.toLowerCase().includes(q) || w.id.toLowerCase().includes(q) || w.kind.toLowerCase().includes(q);
  });
  const customTargets =
    policyScope === "all"
      ? backupWorkloads.filter((w) => !policyExcludedIds.includes(w.id))
      : backupWorkloads.filter((w) => policyWorkloadIds.includes(w.id));
  const workloadNames = useMemo(() => new Map(workloads.map((w) => [w.id, w.name])), [workloads]);
  const workloadName = useCallback((id: string) => workloadNames.get(id) ?? id, [workloadNames]);
  const shownRuns = useMemo(() => filterRuns(runs ?? [], runRange, runsFailedOnly), [runs, runRange, runsFailedOnly]);
  const shownArtifacts = useMemo(() => {
    const q = artifactQuery.trim().toLowerCase();
    const all = artifacts ?? [];
    return q ? all.filter((a) => workloadName(a.workload_id).toLowerCase().includes(q)) : all;
  }, [artifacts, artifactQuery, workloadName]);
  const protectionSummary = useMemo(() => {
    const counts = { protected: 0, localPending: 0, queued: 0, uploading: 0, failed: 0, legacy: 0 };
    for (const art of artifacts ?? []) {
      counts[protectionBucket(art)] += 1;
    }
    return counts;
  }, [artifacts]);
  const latestArtifactByRun = useMemo(() => {
    const map = new Map<string, BackupArtifact>();
    for (const art of artifacts ?? []) {
      const prev = map.get(art.run_id);
      if (!prev || Date.parse(art.created_at) >= Date.parse(prev.created_at)) {
        map.set(art.run_id, art);
      }
    }
    return map;
  }, [artifacts]);
  const logicalBytes = workspace?.logical_bytes ?? 0;
  const physicalBytes = workspace?.physical_bytes ?? workspace?.repo_bytes ?? 0;
  const pendingBytes = workspace?.pending_bytes ?? 0;
  const dedupeSaved = logicalBytes > physicalBytes ? logicalBytes - physicalBytes : 0;
  const hasR2Target = (targets ?? []).some((t) => t.kind === "r2");

  return (
    <BoxesProvider value={boxes.value}>
    <section className="page page-wide bk" aria-labelledby="backups-heading">
      <PageHeader
        id="backups-heading"
        title="Backups"
        kicker="Backups are independent copies. Snapshots are not backups."
      />

      <div className="bk-bar">
        <BoxDock boxes={BOXES} value={boxes.value} />
        <div className="bk-bar-actions">
          {mutate ? (
            <CreateMenu
              items={[
                { label: "Policy", onSelect: () => openPolicyDialog() },
                {
                  label: "Target",
                  onSelect: () => {
                    resetTargetForm();
                    setDialog({ kind: "target" });
                  },
                },
              ]}
            />
          ) : null}
          {mutate ? (
            <button className="btn" type="button" onClick={() => setDialog({ kind: "workspace" })}>
              Settings
            </button>
          ) : null}
        </div>
      </div>

      {error ? <ErrorNotice error={error} /> : null}

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
          <div className="bk-stats" aria-label="Backup summary">
            <span className="bk-stat is-ok" title="Backups uploaded to the cloud and checked there">
              Safe in the cloud <b>{protectionSummary.protected}</b>
            </span>
            <span className={"bk-stat" + (protectionSummary.uploading ? " is-active" : "")} title="Uploading or being checked in the cloud">
              Uploading <b>{protectionSummary.uploading}</b>
            </span>
            <span className={"bk-stat" + (protectionSummary.queued ? " is-active" : "")} title="Waiting for a free upload slot">
              Waiting to upload <b>{protectionSummary.queued}</b>
            </span>
            <span className={"bk-stat" + (protectionSummary.localPending ? " is-warn" : "")} title="Complete on this host, not in the cloud yet">
              Only on this host <b>{protectionSummary.localPending}</b>
            </span>
            <span className={"bk-stat" + (protectionSummary.failed ? " is-danger" : "")} title="The backup or its upload failed">
              Failed <b>{protectionSummary.failed}</b>
            </span>
            {protectionSummary.legacy ? (
              <span className="bk-stat" title="Older tar or pack backups">
                Old format <b>{protectionSummary.legacy}</b>
              </span>
            ) : null}
            <span className="bk-stat" title={dedupeSaved > 0 ? `${formatBytes(logicalBytes)} of data, ${formatBytes(dedupeSaved)} saved by skipping repeats` : "Space the backups use on this host"}>
              Stored on this host <b>{physicalBytes ? formatBytes(physicalBytes) : "Unknown"}</b>
            </span>
            <span
              className={"bk-stat" + (pendingBytes ? " is-active" : "")}
              title={workspace?.capture_active ? `${workspace.capture_active} backup${workspace.capture_active === 1 ? "" : "s"} running` : "No backups running"}
            >
              Left to upload <b>{workspace?.pending_bytes != null ? formatBytes(pendingBytes) : "Unknown"}</b>
            </span>
          </div>

          {workloads.some((w) => w.kind === "system-container") ? (
            <Box id="images" title="Container image copies" short="Image copies">
              <KeepImages workloads={workloads.map((w) => ({ id: w.id, name: w.name, kind: w.kind }))} mutate={mutate} />
            </Box>
          ) : null}
          <Box id="files" title="Workload files and backup rules" short="Files & rules">
            <WorkloadFiles workloads={workloads} mutate={mutate} onBackupsDeleted={() => void afterDelete()} />
          </Box>
          <Box id="sizes" title="Backup size" short="Size">
            <BackupSizes
              artifacts={artifacts ?? []}
              workloadName={workloadName}
              targets={targets ?? []}
              onDeleteWorkload={mutate ? (id, name) => void onDeleteWorkloadBackups(id, name) : undefined}
            />
          </Box>
          <BackupGitHolds owner={isAdmin(roles)} onChanged={() => void reload()} />
          <RecoveryWorkloads owner={isAdmin(roles)} />

          <Box id="policies" title="Policies" short="Policies">
          <section className="panel bk-panel" aria-labelledby="backup-policies-heading">
            <div className="bk-head">
              <h2 id="backup-policies-heading">Policies</h2>
              <span className="bk-hint">A policy backs up its workloads every night and keeps the copies its rules say.</span>
              {mutate ? (
                <button className="btn btn-sm" type="button" onClick={() => setDialog({ kind: "adhoc" })}>
                  Back up one workload
                </button>
              ) : null}
            </div>
            {policies == null ? (
              <LoadingState label="Collecting" />
            ) : policies.length === 0 ? (
              <div className="bk-empty">
                <p className="empty-title">No backup policies yet</p>
                <p className="bk-hint">Create a policy to back up your workloads every night. Full Machine is the default and restores the whole workload.</p>
              </div>
            ) : (
              <div className="bk-list">
                {policies.map((p) => {
                  const latest = latestPolicyRun(runs ?? [], p.id);
                  const latestArt = latest ? latestArtifactByRun.get(latest.id) : undefined;
                  return (
                    <div className="bk-item" key={p.id}>
                      <div className="bk-item-main">
                        <div className="bk-item-title">
                          <h3>{p.name}</h3>
                          <span className={"bk-pill " + (p.paused ? "is-warn" : "is-ok")}>{p.paused ? "Paused" : "Runs nightly"}</span>
                          {latest ? <span className={"bk-pill " + runTone(latest.status)}>Last run: {runStatusLabel(latest.status)}</span> : null}
                        </div>
                        <div className="bk-item-meta">
                          <span>{policyScopeLabel(p, backupWorkloads)}</span>
                          <span>{captureModeLabel(p.capture_mode)}</span>
                          <span>To {targetById.get(p.target_id)?.name ?? p.target_id}</span>
                          <span>Keeps {retentionLabel(p)}</span>
                          <span title={formatWhen(p.last_run_at ?? latest?.started_at)}>Last run {formatRelative(p.last_run_at ?? latest?.started_at)}</span>
                          {latestArt ? (
                            <span>
                              Latest: {protectionLabel(latestArt)}
                              {latestArt.consistency ? ` · ${consistencyLabel(latestArt.consistency)}` : ""}
                              {latestArt.logical_bytes != null ? ` · ${formatBytes(latestArt.logical_bytes)} read` : ""}
                              {latestArt.physical_new_data != null ? ` · ${formatBytes(latestArt.physical_new_data)} new` : ""}
                              {latestArt.capture_duration_ns ? ` · copied in ${formatDurationNS(latestArt.capture_duration_ns)}` : ""}
                              {latestArt.upload_duration_ns ? `, uploaded in ${formatDurationNS(latestArt.upload_duration_ns)}` : ""}
                            </span>
                          ) : null}
                        </div>
                      </div>
                      {mutate ? (
                        <div className="bk-actions">
                          <button className="btn btn-primary btn-sm" type="button" disabled={busy} onClick={() => void onRunPolicy(p)}>
                            Run now
                          </button>
                          <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void onTogglePause(p)}>
                            {p.paused ? "Resume" : "Pause"}
                          </button>
                          <button className="btn btn-sm" type="button" onClick={() => setDialog({ kind: "history", policy: p })}>
                            History
                          </button>
                          <button className="btn btn-sm" type="button" onClick={() => openPolicyDialog(p)}>
                            Edit
                          </button>
                          <button className="btn btn-sm btn-ghost btn-danger-text" type="button" onClick={() => setDialog({ kind: "delete-policy", policy: p })}>
                            Delete
                          </button>
                        </div>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            )}
          </section>
          </Box>

          <div className="bk-row">
            <Box id="storage" title="Where backups are stored" short="Storage">
            <BackupStoragePanel
              mutate={mutate}
              targets={targets ?? []}
              refreshKey={refreshKey}
              targetStatus={(t) => targetStatusLabel(t.status)}
              targetActions={
                mutate
                  ? (t) => (
                      <>
                        {isObjectKind(t.kind) ? (
                          <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void onTestTarget(t.id)}>
                            Test connection
                          </button>
                        ) : null}
                        <DeleteButton
                          path={`/backups/targets/${t.id}`}
                          name={t.name}
                          noun="backup target"
                          label="Remove"
                          description={
                            <p>
                              No-dal stops using this target. Files already on it are not deleted. A target that a policy
                              uses, or that still holds restorable backups, cannot be removed.
                            </p>
                          }
                          onDeleted={() => reload()}
                        />
                        <button
                          className="btn btn-sm btn-ghost btn-danger-text"
                          type="button"
                          disabled={busy}
                          aria-label={`Remove ${t.name} with its backups`}
                          onClick={() => void onRemoveTargetWithBackups(t)}
                        >
                          Remove with backups
                        </button>
                      </>
                    )
                  : undefined
              }
            />

            </Box>
            <Box id="runs" title="Recent runs" short="Runs">
            <section className="panel bk-panel" aria-labelledby="backup-runs-heading">
              <div className="bk-head">
                <h2 id="backup-runs-heading">Recent runs</h2>
                {runs?.length ? (
                  <div className="bk-actions">
                    <select
                      className="field-input bk-select"
                      aria-label="Which runs to show"
                      value={runRange}
                      onChange={(e) => setRunRange(e.target.value as RunRange)}
                    >
                      <option value="latest">{runs.some((r) => r.status === "running") ? "Running now" : "Last backup"}</option>
                      <option value="day">Last 24 hours</option>
                      <option value="all">All runs ({runs.length})</option>
                    </select>
                    <label className="field-check bk-check">
                      <input type="checkbox" checked={runsFailedOnly} onChange={(e) => setRunsFailedOnly(e.target.checked)} />
                      Failed only
                    </label>
                  </div>
                ) : null}
              </div>
              {runs == null ? (
                <LoadingState label="Collecting" />
              ) : runs.length === 0 ? (
                <div className="bk-empty">
                  <p className="empty-title">No backup runs yet</p>
                  <p className="bk-hint">Press Run now on a policy and its runs show here.</p>
                </div>
              ) : (
                <div className="bk-scroll" tabIndex={0} aria-label="Recent runs list">
                  <table className="bk-table">
                    <thead>
                      <tr>
                        <th>Workload</th>
                        <th>Status</th>
                        <th className="bk-num">Uploaded</th>
                        <th>Started</th>
                        <th className="bk-actions-col">
                          <span className="sr-only">Details</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {shownRuns.map((r) => (
                        <tr key={r.id}>
                          <td>{workloadName(r.workload_id)}</td>
                          <td>
                            <span className={"bk-pill " + runTone(r.status)} title={r.error || undefined}>
                              {runStatusLabel(r.status)}
                            </span>
                            {r.incremental ? <span className="bk-hint"> incremental</span> : null}
                          </td>
                          <td className="bk-num">{r.transferred_bytes != null ? formatBytes(r.transferred_bytes) : ""}</td>
                          <td className="bk-nowrap" title={formatWhen(r.started_at)}>{formatRelative(r.started_at)}</td>
                          <td className="bk-actions-col">
                            <button
                              className="btn btn-sm btn-ghost"
                              type="button"
                              aria-label={`Details of ${workloadName(r.workload_id)} run ${formatWhen(r.started_at)}`}
                              onClick={() => setDialog({ kind: "run", run: r })}
                            >
                              Details
                            </button>
                          </td>
                        </tr>
                      ))}
                      {shownRuns.length === 0 ? (
                        <tr>
                          <td colSpan={5} className="bk-hint">
                            No runs match. Choose All runs to see older ones.
                          </td>
                        </tr>
                      ) : null}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
            </Box>
          </div>

          <div className="bk-row is-even is-tall">
          <Box id="restore" title="Restore points" short="Restore points">
          <section className="panel bk-panel" aria-labelledby="backup-artifacts-heading">
            <div className="bk-head">
              <h2 id="backup-artifacts-heading">Restore points</h2>
              <div className="bk-actions">
                {(artifacts ?? []).length > 0 ? (
                  <input
                    className="field-input bk-search"
                    type="search"
                    placeholder="Filter by workload"
                    aria-label="Filter restore points by workload"
                    value={artifactQuery}
                    onChange={(e) => setArtifactQuery(e.target.value)}
                  />
                ) : null}
                {mutate ? (
                  <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void onDRExport()}>
                    Export DR metadata
                  </button>
                ) : null}
              </div>
            </div>
            {artifacts == null ? (
              <LoadingState label="Collecting" />
            ) : artifacts.length === 0 ? (
              <div className="bk-empty">
                <p className="empty-title">No backup artifacts yet</p>
                <p className="bk-hint">Finished backups show here, ready to restore or check.</p>
              </div>
            ) : (
              <div className="bk-scroll">
                <table className="bk-table">
                  <thead>
                    <tr>
                      <th>Workload</th>
                      <th>Status</th>
                      <th className="bk-num">Size</th>
                      <th>Created</th>
                      <th className="bk-actions-col">
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {shownArtifacts.map((art) => (
                      <tr key={art.id}>
                        <td>
                          {workloadName(art.workload_id)}
                          <div className="bk-hint" title={artifactBackupType(art)}>
                            {shortBackupType(artifactBackupType(art))}
                          </div>
                        </td>
                        <td>
                          <span className={"bk-pill " + protectionTone(art)} title={art.remote_error && art.remote_state !== "protected" ? art.remote_error : undefined}>
                            {protectionLabel(art)}
                          </span>
                          {art.protected ? <span className="bk-pill">Kept</span> : null}
                          {art.verify_status === "verified" ? <span className="bk-pill is-ok">Verified</span> : null}
                          {art.verify_status === "failed" ? <span className="bk-pill is-danger">Check failed</span> : null}
                          {uploadProgress(art) ? <div className="bk-hint">{uploadProgress(art)}</div> : null}
                        </td>
                        <td className="bk-num">{formatBytes(art.logical_bytes || art.size_bytes)}</td>
                        <td className="bk-nowrap" title={formatWhen(art.created_at)}>{formatRelative(art.created_at)}</td>
                        <td className="bk-actions-col">
                          {mutate ? (
                            <div className="bk-actions">
                              <button
                                className="btn btn-sm"
                                type="button"
                                aria-label="Restore or verify"
                                title="Restore or verify"
                                onClick={() => setDialog({ kind: "artifact", artifact: art })}
                              >
                                Restore
                              </button>
                              <button
                                className="btn btn-sm"
                                type="button"
                                disabled={busy}
                                aria-label={`${art.protected ? "Unprotect" : "Protect"} backup from ${formatWhen(art.created_at)}`}
                                title={art.protected ? "Let retention delete this backup again" : "Keep this backup even when retention would delete it"}
                                onClick={() => void onToggleProtect(art)}
                              >
                                {art.protected ? "Unprotect" : "Protect"}
                              </button>
                              <button
                                className="btn btn-sm btn-ghost btn-danger-text"
                                type="button"
                                disabled={busy || art.protected}
                                aria-label={`Delete backup from ${formatWhen(art.created_at)}`}
                                title={art.protected ? "Unprotect it first" : undefined}
                                onClick={() => void onDeleteArtifact(art)}
                              >
                                Delete
                              </button>
                            </div>
                          ) : null}
                        </td>
                      </tr>
                    ))}
                    {shownArtifacts.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="bk-hint">
                          No restore points match.
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
            )}
          </section>
          </Box>
          <Box id="dr" title="Disaster recovery" short="Recovery">
            <PlatformBackupPanel owner={isAdmin(roles)} targets={targets ?? []} />
          </Box>
          </div>
        </>
      ) : null}

      <ConfirmDialog
        open={dialog?.kind === "run"}
        title="Backup run details"
        hideFooter
        wide
        onClose={() => setDialog(null)}
        onConfirm={() => setDialog(null)}
      >
        {dialog?.kind === "run" ? <RunDetails run={dialog.run} workload={workloadName(dialog.run.workload_id)} target={targetById.get(dialog.run.target_id)?.name ?? dialog.run.target_id} /> : null}
      </ConfirmDialog>

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
            <optgroup label="On this host">
              <option value="local">Storage pool on this host</option>
            </optgroup>
            <optgroup label="Network share">
              <option value="nfs">NFS share</option>
              <option value="smb">SMB share</option>
            </optgroup>
            <optgroup label="Cloud object storage">
              <option value="r2">Cloudflare R2</option>
              <option value="s3">S3-compatible</option>
              <option value="aws">Amazon S3</option>
              <option value="b2">Backblaze B2</option>
              <option value="minio">MinIO</option>
            </optgroup>
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
            {targetKind === "local" ? (
              <BackupLocationPicker
                idPrefix="backup-target"
                poolId={targetPoolId}
                path={targetLocator}
                allowRoot={targetAllowRoot}
                pathKind="target"
                onChange={(next) => {
                  setTargetPoolId(next.poolId);
                  setTargetLocator(next.path);
                  setTargetAllowRoot(next.allowRoot);
                }}
              />
            ) : (
              <Field
                id="backup-target-locator"
                label="Share"
                value={targetLocator}
                onChange={(e) => setTargetLocator(e.target.value)}
                autoComplete="off"
                hint={targetKind === "nfs" ? "server:/export/path" : "//server/share"}
              />
            )}
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
        ) : (
          <div className="stack">
            <p className="field-label" id="backup-policy-exclude-heading">
              Leave out{policyExcludedIds.length ? ` (${policyExcludedIds.length})` : ""}
            </p>
            <p className="field-hint">
              Workloads ticked here are never backed up by this policy and are not counted as unprotected. Every other
              workload, including ones created later, is covered.
            </p>
            <Field
              id="backup-policy-exclude-search"
              label="Search workloads"
              value={workloadQuery}
              onChange={(e) => setWorkloadQuery(e.target.value)}
              autoComplete="off"
            />
            <div className="picker-list" aria-labelledby="backup-policy-exclude-heading">
              {filteredWorkloads.length === 0 ? (
                <p className="muted">No matching workloads.</p>
              ) : (
                filteredWorkloads.map((w) => {
                  const excluded = policyExcludedIds.includes(w.id);
                  return (
                    <label className="field-check" key={w.id}>
                      <input
                        type="checkbox"
                        aria-label={`Leave out ${w.name}`}
                        checked={excluded}
                        onChange={() => {
                          setPolicyExcludedIds((cur) => (excluded ? cur.filter((id) => id !== w.id) : [...cur, w.id]));
                        }}
                      />
                      {w.name} <span className="muted">({w.kind})</span>
                    </label>
                  );
                })
              )}
            </div>
          </div>
        )}
        <fieldset className="field">
          <legend className="field-label">Backup scope</legend>
          <div className="scope-options">
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="smart"
                checked={policyCaptureMode === "smart"}
                onChange={() => setCaptureMode("smart")}
              />
              Smart Application Data
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="custom"
                checked={policyCaptureMode === "custom"}
                onChange={() => setCaptureMode("custom")}
              />
              Custom
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-policy-capture-mode"
                value="full"
                checked={policyCaptureMode === "full"}
                onChange={() => setCaptureMode("full")}
              />
              Full Machine / Full LXC
            </label>
          </div>
          {policyCaptureMode === "smart" ? (
            <p className="field-hint">
              Protects persistent application data, databases, configuration and Docker volumes. It excludes much of
              the OS and reproducible tree, so it cannot rebuild a complete guest by itself.
            </p>
          ) : null}
          {policyCaptureMode === "full" ? (
            <p className="field-hint">
              Full Machine / Full LXC is the default. It captures the complete recoverable guest filesystem. Repeated
              OS data stays inexpensive because Backup Engine V2 reuses content-addressed chunks.
            </p>
          ) : null}
        </fieldset>
        {policyCaptureMode === "custom" ? (
          <div className="stack">
            <p className="field-hint">
              Discovery runs only when you configure a workload. Include or exclude category groups, or add raw paths.
            </p>
            {customTargets.length === 0 ? (
              <p className="muted">Select workloads above, or choose All workloads, then configure one machine.</p>
            ) : (
              <div className="picker-list" role="group" aria-label="Custom workload configuration">
                {customTargets.map((w) => (
                  <button
                    key={w.id}
                    className="btn btn-sm"
                    type="button"
                    disabled={customPreviewBusy && customWorkloadId === w.id}
                    onClick={() => void loadCustomPreview(w.id)}
                  >
                    {customWorkloadId === w.id ? `Configuring ${w.name}` : `Configure ${w.name}`}
                  </button>
                ))}
              </div>
            )}
            {customPreviewBusy ? <p className="muted">Collecting categories for this workload.</p> : null}
            {customPreviewError ? <p className="muted">{customPreviewError}</p> : null}
            {customPreview?.items?.[0] ? (
              <article className="scope-preview-card">
                <h4>{customPreview.items[0].workload_name || customWorkloadId}</h4>
                <div className="picker-list" role="group" aria-label="Detected data categories">
                  {groupCategories(asScopeCategories(customPreview.items[0].items)).map((group) => (
                    <label className="field-check" key={group.kind}>
                      <input
                        type="checkbox"
                        checked={group.selected}
                        onChange={(e) => togglePreviewItems(customWorkloadId, group.ids, e.target.checked)}
                      />
                      <span>
                        {group.label}
                        <span className="muted"> {formatBytes(group.bytes)}</span>
                        {group.warnings[0] ? <span className="scope-warning"> {group.warnings[0]}</span> : null}
                      </span>
                    </label>
                  ))}
                </div>
                <label className="field-check">
                  <input type="checkbox" checked={showAdvancedPaths} onChange={(e) => setShowAdvancedPaths(e.target.checked)} />
                  Show advanced path rules
                </label>
                {showAdvancedPaths ? (
                  <div className="stack">
                    <div className="field">
                      <label className="field-label" htmlFor={`backup-include-${customWorkloadId}`}>
                        Additional include paths
                      </label>
                      <textarea
                        id={`backup-include-${customWorkloadId}`}
                        className="field-input"
                        rows={3}
                        value={(policySelections[customWorkloadId]?.includes ?? []).join("\n")}
                        onChange={(e) => updateAdvancedPaths(customWorkloadId, "includes", e.target.value)}
                      />
                      <p className="field-hint">One guest path per line.</p>
                    </div>
                    <div className="field">
                      <label className="field-label" htmlFor={`backup-exclude-${customWorkloadId}`}>
                        Additional exclude paths
                      </label>
                      <textarea
                        id={`backup-exclude-${customWorkloadId}`}
                        className="field-input"
                        rows={3}
                        value={(policySelections[customWorkloadId]?.excludes ?? []).join("\n")}
                        onChange={(e) => updateAdvancedPaths(customWorkloadId, "excludes", e.target.value)}
                      />
                      <p className="field-hint">One guest path per line.</p>
                    </div>
                  </div>
                ) : null}
              </article>
            ) : null}
          </div>
        ) : null}
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
        {isObjectKind(targets?.find((t) => t.id === policyTargetId)?.kind ?? "") ? (
          <fieldset className="field">
            <legend className="field-label">Offsite copy</legend>
            <p className="field-hint">
              How many restore points keep a copy in cloud storage. Leave all at 0 to keep the same points offsite as
              locally. Lower numbers keep the bucket small; local copies are not affected.
            </p>
            <Field
              id="backup-offsite-daily"
              label="Offsite daily"
              type="number"
              min={0}
              value={offsiteDaily}
              onChange={(e) => setOffsiteDaily(e.target.value)}
            />
            <Field
              id="backup-offsite-weekly"
              label="Offsite weekly"
              type="number"
              min={0}
              value={offsiteWeekly}
              onChange={(e) => setOffsiteWeekly(e.target.value)}
            />
            <Field
              id="backup-offsite-monthly"
              label="Offsite monthly"
              type="number"
              min={0}
              value={offsiteMonthly}
              onChange={(e) => setOffsiteMonthly(e.target.value)}
            />
          </fieldset>
        ) : null}
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
        <fieldset className="field">
          <legend className="field-label">Backup scope</legend>
          <div className="scope-options">
            <label className="field-check">
              <input
                type="radio"
                name="backup-adhoc-capture-mode"
                value="smart"
                checked={runCaptureMode === "smart"}
                onChange={() => setRunCaptureMode("smart")}
              />
              Smart Application Data
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-adhoc-capture-mode"
                value="custom"
                checked={runCaptureMode === "custom"}
                onChange={() => setRunCaptureMode("custom")}
              />
              Custom
            </label>
            <label className="field-check">
              <input
                type="radio"
                name="backup-adhoc-capture-mode"
                value="full"
                checked={runCaptureMode === "full"}
                onChange={() => setRunCaptureMode("full")}
              />
              Full Machine / Full LXC
            </label>
          </div>
          {runCaptureMode === "full" ? (
            <p className="field-hint">
              Full Machine / Full LXC is the ad-hoc default so the copy can restore the complete recoverable guest.
            </p>
          ) : null}
        </fieldset>
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
            {backupWorkloads.map((w) => (
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
          <div className="bk-scroll is-tall">
            <table className="bk-table">
              <thead>
                <tr>
                  <th>Workload</th>
                  <th>Status</th>
                  <th>Uploaded</th>
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
                      <td>{workloadName(r.workload_id)}</td>
                      <td>{runStatusLabel(r.status)}</td>
                      <td>{r.transferred_bytes != null ? formatBytes(r.transferred_bytes) : "None"}</td>
                      <td>{formatWhen(r.started_at)}</td>
                      <td>{formatWhen(r.finished_at)}</td>
                      <td className="bk-clip" title={r.error || undefined}>
                        {r.error}
                      </td>
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
              <div>
                <dt>Local / remote</dt>
                <dd>
                  {dialog.artifact.local_complete ? "Local complete" : "Local incomplete"} · {dialog.artifact.remote_state || "local-only"}
                </dd>
              </div>
              <div>
                <dt>Consistency</dt>
                <dd>{consistencyLabel(dialog.artifact.consistency)}</dd>
              </div>
              <div>
                <dt>Checked</dt>
                <dd>{verifyStatusLabel(dialog.artifact.verify_status)}</dd>
              </div>
              <div>
                <dt>Logical size</dt>
                <dd>{formatBytes(dialog.artifact.logical_bytes || dialog.artifact.size_bytes)}</dd>
              </div>
              <div>
                <dt>Physical / stored size</dt>
                <dd>{dialog.artifact.physical_new_data != null ? formatBytes(dialog.artifact.physical_new_data) : "Unknown"}</dd>
              </div>
              {compressionSaving(dialog.artifact) ? (
                <div>
                  <dt>New data / compressed</dt>
                  <dd>{compressionSaving(dialog.artifact)}</dd>
                </div>
              ) : null}
              {dialog.artifact.upload_total_bytes ? (
                <div>
                  <dt>Uploaded</dt>
                  <dd>
                    {formatBytes(dialog.artifact.uploaded_bytes ?? 0)} of {formatBytes(dialog.artifact.upload_total_bytes)}
                  </dd>
                </div>
              ) : null}
              {dialog.artifact.remote_error && dialog.artifact.remote_state !== "protected" ? (
                <div>
                  <dt>Upload error</dt>
                  <dd>{dialog.artifact.remote_error}</dd>
                </div>
              ) : null}
              <div>
                <dt>Created</dt>
                <dd>{formatWhen(dialog.artifact.created_at)}</dd>
              </div>
              <div>
                <dt>Workload / Blueprint</dt>
                <dd>{blueprintSummary(dialog.artifact.blueprint)}</dd>
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
        title="Backup settings"
        confirmLabel="Save settings"
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
          Logical {workspace?.logical_bytes != null ? formatBytes(workspace.logical_bytes) : "unknown"}.
          Physical {workspace?.physical_bytes != null || workspace?.repo_bytes != null ? formatBytes(workspace.physical_bytes || workspace.repo_bytes || 0) : "unknown"}.
        </p>
        {hasR2Target ? (
          <p className="muted">
            Cloudflare R2 Local Uploads is a bucket-side option that can reduce upload latency. Backup Engine V2 works
            without it and does not toggle the setting. Enable it in the Cloudflare dashboard if this bucket benefits
            from it.
          </p>
        ) : null}
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
          id="backup-workspace-capture-concurrency"
          label="Capture concurrency"
          type="number"
          min={1}
          value={captureConcurrency}
          onChange={(e) => setCaptureConcurrency(e.target.value)}
          hint="How many different workloads may capture at once. The same workload cannot capture twice."
        />
        <Field
          id="backup-workspace-upload-workers"
          label="Upload workers"
          type="number"
          min={1}
          value={uploadWorkers}
          onChange={(e) => setUploadWorkers(e.target.value)}
        />
        <Field
          id="backup-workspace-bandwidth"
          label="Upload bandwidth limit (MiB/s)"
          type="number"
          min={0}
          value={bandwidthMBps}
          onChange={(e) => setBandwidthMBps(e.target.value)}
          hint="0 means unlimited. This limits remote upload, not local capture."
        />
        <Field
          id="backup-workspace-cache-retention"
          label="Local cache retention (hours)"
          type="number"
          min={0}
          value={cacheRetentionHours}
          onChange={(e) => setCacheRetentionHours(e.target.value)}
          hint="0 keeps metadata caches until the next successful prune. Loss of cache only slows the next capture."
        />
        <label className="field">
          <span className="field-label">Compression</span>
          <select className="field-input" value={compressionLevel} onChange={(e) => setCompressionLevel(e.target.value)}>
            <option value="3">zstd 3 (fastest)</option>
            <option value="9">zstd 9</option>
            <option value="19">zstd 19 (default, small)</option>
            <option value="22">zstd 22 (smallest, slowest)</option>
            <option value="-1">Off</option>
          </select>
          <span className="field-hint">
            Applies to new data. Already-compressed files (images, video, archives) are stored as they are. Existing
            backups keep their compression.
          </span>
        </label>
        <label className="field-check">
          <input
            id="backup-workspace-ignore-git"
            type="checkbox"
            checked={ignoreUncommittedGit}
            disabled={!isAdmin(roles)}
            onChange={(e) => setIgnoreUncommittedGit(e.target.checked)}
          />
          Ignore uncommitted Git repositories
        </label>
        <p className="field-hint">
            Smart backups leave Git repositories to Git and record the exact commit to re-clone. When this is off, a
            workload whose repositories have uncommitted or unpushed work is not backed up until a SuperAdmin saves it,
            lets NDL-CE commit and push it, or turns this back on. Only a SuperAdmin can change it.
        </p>
      </ConfirmDialog>
    </section>
    </BoxesProvider>
  );
}
