import { useEffect, useState } from "react";
import {
  getDocker,
  getWorkload,
  getWorkloadGPUDiagnostics,
  getWorkloadLogs,
  reapplyWorkloadGPUs,
  workloadAction,
} from "../api/client";
import type { Workload } from "../api/phase5";
import type {
  DockerMachine,
  GPUDiagnosis,
  GPUNodeDiagnosis,
  GPUReapplyResponse,
  WorkloadGPUDiagnostics,
} from "../generated/openapi";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { canMutate } from "../rbac";
import { currentPath } from "../router";
import { useSession } from "../session";
import { metricReading, seriesBySuffix, useWorkloadMetrics } from "../workloadMetrics";

const ERROR_LINE = /\b(error|failed|failure|denied|cannot|unable|fatal)\b/i;

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

function guestLabel(guest: GPUNodeDiagnosis["guest"]): string {
  switch (guest) {
    case "present":
      return "Present";
    case "missing":
      return "Missing";
    case "mismatch":
      return "Wrong device";
    case "not_running":
      return "Not running";
    default:
      return "Unknown";
  }
}

// gpuState summarizes the saved assignment against what the guest received.
export function gpuState(diag: WorkloadGPUDiagnostics | null): { label: string; status: string } {
  if (!diag) {
    return { label: "Loading", status: "collecting" };
  }
  if (!diag.supported) {
    return { label: "Not applicable", status: "unknown" };
  }
  if (!diag.assignments?.length && !diag.diagnosis?.saved.length) {
    return { label: "Not assigned", status: "unknown" };
  }
  if (diag.assignments?.some((a) => a.error)) {
    return { label: "GPU unavailable", status: "warning" };
  }
  const d = diag.diagnosis;
  if (!d) {
    return { label: "Unavailable", status: "unavailable" };
  }
  if (d.devices_missing) {
    return { label: "GPU missing", status: "warning" };
  }
  if (d.restart_required) {
    return { label: "Restart required", status: "warning" };
  }
  const outdated = diag.assignments?.some((a) => !a.current) ?? false;
  const broken = d.nodes.some((n) => n.host_rule && (!n.mounted || !n.allowed || n.guest === "missing" || n.guest === "mismatch"));
  if (outdated || broken || !d.config_current || diag.applied_matches_saved === false) {
    return { label: "Needs reapply", status: "warning" };
  }
  return { label: d.running ? "Healthy" : "Configured", status: d.running ? "healthy" : "stopped" };
}

// verified is true only when the running guest has every host-present node.
function verified(d: GPUDiagnosis | undefined): boolean {
  if (!d || !d.running || d.restart_required || d.devices_missing || !d.config_current) {
    return false;
  }
  return d.nodes.every((n) => !n.host_rule || n.guest === "present");
}

export function WorkloadDiagnosticsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const id = workloadIDFromPath();

  const [item, setItem] = useState<Workload | null>(null);
  const [gpu, setGPU] = useState<WorkloadGPUDiagnostics | null>(null);
  const [gpuError, setGPUError] = useState<string | null>(null);
  const [docker, setDocker] = useState<DockerMachine | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ tone: "ok" | "warn" | "error"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [reapply, setReapply] = useState<GPUReapplyResponse | null>(null);
  const [confirmRestart, setConfirmRestart] = useState(false);
  const [errors, setErrors] = useState<string[] | null>(null);

  const running = item ? item.status === "running" || Boolean(item.unit_active) : false;
  const metrics = useWorkloadMetrics(id, running);

  async function loadWorkload() {
    const w = await getWorkload(id);
    setItem(w);
    return w;
  }

  async function loadGPU() {
    setGPUError(null);
    try {
      setGPU(await getWorkloadGPUDiagnostics(id));
    } catch (err) {
      setGPU(null);
      setGPUError(err instanceof Error ? err.message : "GPU diagnostics unavailable");
    }
  }

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        await loadWorkload();
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
        return;
      }
      await loadGPU();
      try {
        const inv = await getDocker();
        if (!cancelled) {
          setDocker((inv.machines ?? []).find((m) => m.id === id) ?? null);
        }
      } catch {
        if (!cancelled) {
          setDocker(null);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  function onReapply() {
    void run(async () => {
      setNotice(null);
      try {
        const res = await reapplyWorkloadGPUs(id);
        setReapply(res);
        if (res.status === "applied") {
          setNotice({ tone: "ok", text: res.message || "GPU configuration applied." });
        } else if (res.status === "restart_required") {
          setNotice({ tone: "warn", text: res.message || "Restart required to apply the GPU configuration." });
          setConfirmRestart(true);
        } else {
          setNotice({ tone: "error", text: res.message || res.error || "GPU reapply failed." });
        }
      } catch (err) {
        setNotice({ tone: "error", text: err instanceof Error ? err.message : "GPU reapply failed." });
      }
      await loadGPU();
    });
  }

  function onRestart() {
    setConfirmRestart(false);
    void run(async () => {
      await workloadAction(id, "restart");
      await loadWorkload();
      const after = await getWorkloadGPUDiagnostics(id);
      setGPU(after);
      if (verified(after.diagnosis)) {
        setNotice({ tone: "ok", text: "Restarted. The container has every GPU device." });
      } else {
        setNotice({
          tone: "error",
          text: "Restarted, but the container still does not have every GPU device. Review the device table below.",
        });
      }
      setReapply(null);
      metrics.refresh();
    });
  }

  function onRecentErrors() {
    void run(async () => {
      const logs = await getWorkloadLogs(id, 300);
      if (logs.status !== "available") {
        setErrors([]);
        setNotice({ tone: "warn", text: logs.message || "Workload logs are unavailable." });
        return;
      }
      setErrors((logs.lines ?? []).filter((line) => ERROR_LINE.test(line)).slice(-30));
    });
  }

  if (!item) {
    return (
      <section className="page">
        {error ? <ErrorState>{error}</ErrorState> : <LoadingState label="Loading workload" />}
      </section>
    );
  }

  const cpuSeries = seriesBySuffix(metrics.series, ".cpu.busy_ratio");
  const memSeries = seriesBySuffix(metrics.series, ".memory.current_bytes");
  const memPoint = memSeries?.status === "available" ? memSeries.points[memSeries.points.length - 1] : undefined;
  const gpuSummary = gpuState(gpu);
  const diag = gpu?.diagnosis;

  return (
    <section className="page page-wide" aria-labelledby="diagnostics-heading">
      <PageHeader
        id="diagnostics-heading"
        title="Diagnostics"
        kicker={
          <>
            <Link href={`/workloads/${item.id}`}>{item.name}</Link> · <StatusBadge status={item.status} />
          </>
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {notice ? (
        <p
          className={notice.tone === "ok" ? "banner" : notice.tone === "warn" ? "banner banner-warn" : "banner banner-error"}
          role={notice.tone === "error" ? "alert" : "status"}
        >
          {notice.text}
        </p>
      ) : null}

      <section className="overview-metrics" aria-label="Health overview">
        <article className="panel metric-tile">
          <h2>Runtime</h2>
          <p className="metric-value">
            <StatusBadge status={item.status} />
          </p>
          <p className="page-kicker">
            Unit {item.unit_active ? "active" : "inactive"}
            {item.pid != null ? ` · PID ${item.pid}` : ""}
            {item.reason ? ` · ${item.reason}` : ""}
          </p>
        </article>
        <article className="panel metric-tile">
          <h2>CPU</h2>
          <p className="metric-value">{metricReading(metrics.state, cpuSeries, running)}</p>
        </article>
        <article className="panel metric-tile">
          <h2>Memory</h2>
          <p className="metric-value">
            {memPoint && running ? formatBytes(memPoint.value) : metricReading(metrics.state, memSeries, running)}
          </p>
          <p className="page-kicker">Limit {formatBytes(item.memory_bytes)}</p>
        </article>
        <article className="panel metric-tile">
          <h2>GPU</h2>
          <p className="metric-value">
            <StatusBadge status={gpuSummary.status} label={gpuSummary.label} />
          </p>
        </article>
        {docker ? (
          <article className="panel metric-tile">
            <h2>Docker</h2>
            <p className="metric-value">
              <StatusBadge status={docker.health || (docker.daemon_ok ? "healthy" : "unavailable")} />
            </p>
            <p className="page-kicker">
              {docker.daemon_ok
                ? `${docker.container_count ?? 0} containers`
                : docker.daemon_error || docker.health_reason || "Engine not reachable"}
            </p>
          </article>
        ) : null}
      </section>

      <article className="panel">
        <h2>Checks</h2>
        <div className="btn-row">
          <button className="btn" type="button" disabled={busy} onClick={() => void run(async () => void (await loadWorkload()))}>
            Check runtime state
          </button>
          <button className="btn" type="button" disabled={busy} onClick={() => void run(loadGPU)}>
            Check config consistency
          </button>
          <button className="btn" type="button" disabled={busy} onClick={() => metrics.refresh()}>
            Refresh stats
          </button>
          <button className="btn" type="button" disabled={busy} onClick={onRecentErrors}>
            View recent errors
          </button>
        </div>
        {errors ? (
          errors.length ? (
            <pre className="code-block" aria-label="Recent errors">
              {errors.join("\n")}
            </pre>
          ) : (
            <p>No error lines in the recent unit log.</p>
          )
        ) : null}
      </article>

      <article className="panel">
        <h2>GPU diagnostics</h2>
        {gpuError ? <ErrorState>{gpuError}</ErrorState> : null}
        {!gpu && !gpuError ? <LoadingState label="Checking GPU configuration" /> : null}
        {gpu && !gpu.supported ? <p>{gpu.reason || "GPU diagnostics are not available for this workload."}</p> : null}
        {gpu?.supported ? (
          <>
            {gpu.assignments?.length ? (
              <div className="table-wrap">
                <table>
                  <caption>Saved assignment</caption>
                  <thead>
                    <tr>
                      <th>GPU</th>
                      <th>Mode</th>
                      <th>Saved devices</th>
                      <th>Expected devices</th>
                      <th>State</th>
                    </tr>
                  </thead>
                  <tbody>
                    {gpu.assignments.map((a) => (
                      <tr key={a.id}>
                        <td>{a.gpu_id}</td>
                        <td>{a.mode}</td>
                        <td>{a.saved_nodes.join(", ") || "None"}</td>
                        <td>{a.expected_nodes.join(", ") || "None"}</td>
                        <td>{a.error ? a.error : a.current ? "Current" : "Outdated"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p>No GPU is assigned to this workload.</p>
            )}
            {gpu.assignments?.some((a) => a.error) ? (
              <p className="banner banner-warn" role="status">
                A saved GPU is not available on this node. If the card was removed, unassign it on the{" "}
                <Link href={`/workloads/${item.id}/gpus`}>GPU tab</Link>. Reapply stays blocked until then.
              </p>
            ) : null}
            {gpu.applied_matches_saved === false ? (
              <p className="banner banner-warn" role="status">
                The devices applied to this container differ from its saved GPU assignments. Reapply to regenerate them.
              </p>
            ) : null}
            {gpu.diagnosis_error ? <ErrorState>{gpu.diagnosis_error}</ErrorState> : null}
            {diag ? (
              <>
                <p className="page-kicker">
                  LXC config {diag.config_present ? (diag.config_current ? "matches the saved assignment" : "differs from the saved assignment") : "is missing"}.
                  {diag.running ? ` Container PID ${diag.pid}.` : " Container is not running."}
                </p>
                {diag.nodes.length ? (
                  <div className="table-wrap">
                    <table>
                      <caption>Device exposure</caption>
                      <thead>
                        <tr>
                          <th>Device</th>
                          <th>Host</th>
                          <th>Mount</th>
                          <th>Permission</th>
                          <th>Container</th>
                        </tr>
                      </thead>
                      <tbody>
                        {diag.nodes.map((n) => (
                          <tr key={n.path}>
                            <td>
                              {n.path}
                              {n.canonical ? " (resolved)" : ""}
                              {n.optional ? " (optional)" : ""}
                            </td>
                            <td>{n.host_rule ? n.host_rule.replace(/ rwm$/, "") : "Unavailable"}</td>
                            <td>{n.mounted ? "Yes" : "Missing"}</td>
                            <td>{n.allowed ? "Yes" : "Missing"}</td>
                            <td>{guestLabel(n.guest)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : null}
                {diag.issues.length ? (
                  <ul aria-label="GPU issues">
                    {diag.issues.map((issue) => (
                      <li key={issue}>{issue}</li>
                    ))}
                  </ul>
                ) : (
                  <p>No GPU issues found.</p>
                )}
              </>
            ) : null}
            {mutate ? (
              <>
                <p className="field-hint">
                  Reapply regenerates this container's GPU config from its saved assignment. It never restarts the
                  container; if the running container needs a restart to receive the devices, you are asked first.
                </p>
                <div className="btn-row">
                  <button className="btn btn-primary" type="button" disabled={busy} onClick={onReapply}>
                    Reapply GPU configuration
                  </button>
                  {reapply?.status === "restart_required" ? (
                    <button className="btn" type="button" disabled={busy} onClick={() => setConfirmRestart(true)}>
                      Restart to apply
                    </button>
                  ) : null}
                </div>
              </>
            ) : null}
          </>
        ) : null}
      </article>

      <ConfirmDialog
        open={confirmRestart}
        title="Restart to apply GPU devices"
        confirmLabel="Restart"
        danger
        confirmDisabled={busy}
        onClose={() => setConfirmRestart(false)}
        onConfirm={onRestart}
      >
        <p>
          The config for {item.name} is regenerated, but the running container has not received the GPU devices. Restart{" "}
          {item.name} now? Its processes stop and start again. No other workload is affected.
        </p>
      </ConfirmDialog>
    </section>
  );
}
