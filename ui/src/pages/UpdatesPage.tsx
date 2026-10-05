import { useEffect, useRef, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import {
  ApiError,
  applyUpdates,
  checkUpdates,
  checkpointUpdates,
  enableUpdateRepository,
  getUpdates,
  preflightUpdates,
  rollbackUpdates,
} from "../api/client";
import type {
  UpdateCheckpoint,
  UpdateOperation,
  UpdatePreflight,
  UpdatePreview,
  UpdateStatus,
} from "../generated/openapi";
import { formatWhen, honestStatus } from "../format";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { SummaryCard } from "../ui/SummaryCard";

import { hasGrant } from "../rbac";

// While an apply runs the control plane and agent restart, so status reads
// fail for a few seconds. Poll quickly and ignore those errors.
const APPLY_POLL_MS = 3_000;

function applyRunning(op: UpdateOperation | null | undefined): boolean {
  return op?.action === "apply" && op.status === "running" && !op.dry_run;
}

function packageStatusLabel(status: string): string {
  switch (status) {
    case "current":
      return "Current";
    case "update_available":
      return "Update available";
    case "unsupported":
      return "Unsupported";
    case "not_configured":
      return "Not configured";
    case "not_reported":
      return "Not reported";
    default:
      return honestStatus(status);
  }
}

function operationStatusLabel(status: UpdateOperation["status"]): string {
  switch (status) {
    case "running":
      return "Running";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "unsupported":
      return "Unsupported";
    default:
      return honestStatus(status);
  }
}

function checkStatusLabel(status: string): string {
  switch (status) {
    case "ok":
      return "Ok";
    case "warning":
      return "Warning";
    case "failed":
      return "Failed";
    case "unsupported":
      return "Unsupported";
    default:
      return honestStatus(status);
  }
}

function checkpointStatusLabel(status: UpdateCheckpoint["status"]): string {
  switch (status) {
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "unsupported":
      return "Unsupported";
    default:
      return honestStatus(status);
  }
}

export function UpdatesPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "updates.manage");

  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [preview, setPreview] = useState<UpdatePreview | null>(null);
  const [preflight, setPreflight] = useState<UpdatePreflight | null>(null);
  const [checkpoint, setCheckpoint] = useState<UpdateCheckpoint | null>(null);
  const [lastOp, setLastOp] = useState<UpdateOperation | null>(null);
  const [loadState, setLoadState] = useState<"collecting" | "ready" | "unavailable">("collecting");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  // Set once an apply is seen running, so its outcome can be announced.
  const watchingApply = useRef(false);
  const [applyOutcome, setApplyOutcome] = useState<UpdateOperation | null>(null);

  async function reload() {
    const next = await getUpdates();
    setStatus(next);
    if (next.last_operation) {
      setLastOp(next.last_operation);
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
    const statusPoll = window.setInterval(() => {
      void getUpdates()
        .then((next) => {
          if (!cancelled) {
            setStatus(next);
          }
        })
        .catch(() => undefined);
    }, 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(statusPoll);
    };
  }, []);

  const currentOp = status?.last_operation ?? lastOp;
  const updating = applyRunning(currentOp);

  useEffect(() => {
    if (updating) {
      watchingApply.current = true;
      setApplyOutcome(null);
    } else if (watchingApply.current && currentOp?.action === "apply") {
      watchingApply.current = false;
      setApplyOutcome(currentOp);
    }
  }, [updating, currentOp]);

  useEffect(() => {
    if (!updating) {
      return;
    }
    let cancelled = false;
    const poll = window.setInterval(() => {
      void getUpdates()
        .then((next) => {
          if (!cancelled) {
            setStatus(next);
            if (next.last_operation) {
              setLastOp(next.last_operation);
            }
          }
        })
        .catch(() => undefined);
    }, APPLY_POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(poll);
    };
  }, [updating]);

  async function runAction(action: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCheck() {
    await runAction(async () => {
      const next = await checkUpdates();
      setPreview(next);
    });
  }

  async function onPreflight() {
    await runAction(async () => {
      const next = await preflightUpdates();
      setPreflight(next);
    });
  }

  async function onCheckpoint() {
    await runAction(async () => {
      const next = await checkpointUpdates();
      setCheckpoint(next);
    });
  }

  async function onApply() {
    if (
      !window.confirm(
        "Install the latest release? The control plane restarts during the update and this page reconnects on its own. Guests keep running.",
      )
    ) {
      return;
    }
    await runAction(async () => {
      const next = await applyUpdates();
      setLastOp(next);
    });
  }

  async function onEnableRepository() {
    if (
      !window.confirm(
        "Add the signed No-dal release repository (packages.no-dal.com) as a package source on this host?",
      )
    ) {
      return;
    }
    await runAction(async () => {
      const next = await enableUpdateRepository();
      setLastOp(next);
      if (next.status === "failed" && next.error) {
        throw new Error(next.error);
      }
    });
  }

  async function onRollback() {
    if (
      !window.confirm(
        "Roll back the last control-plane package update? Confirmation is sent as X-Nodal-Confirm: rollback-update.",
      )
    ) {
      return;
    }
    await runAction(async () => {
      const next = await rollbackUpdates();
      setLastOp(next);
    });
  }

  const hostSupported = status?.host_supported === true;
  const actionsEnabled = mutate && hostSupported && !busy && !applyRunning(status?.last_operation ?? lastOp);
  const lastCheckCandidates = status?.last_check?.candidates ?? [];
  const available = preview
    ? { version: preview.version, url: preview.release_url }
    : lastCheckCandidates.length > 0
      ? {
          version:
            lastCheckCandidates.find((c) => c.name === "nodal")?.candidate_version ||
            lastCheckCandidates[0].candidate_version,
          url: status?.last_check?.release_url,
        }
      : null;
  const op = currentOp;
  const repositoryMissing = hostSupported && status?.repository_configured === false;

  return (
    <section className="page page-wide" aria-labelledby="updates-heading">
      <PageHeader
        id="updates-heading"
        title="Updates"
        kicker="Control-plane package bumps must not stop guests. Split packages update the management plane while workloads keep running."
      />

      <div className="btn-row">
        <button className="btn" type="button" disabled={!actionsEnabled} onClick={() => void onCheck()}>
          Check for updates
        </button>
        <button className="btn" type="button" disabled={!actionsEnabled} onClick={() => void onPreflight()}>
          Run preflight
        </button>
        <button className="btn" type="button" disabled={!actionsEnabled} onClick={() => void onCheckpoint()}>
          Create checkpoint
        </button>
        <button className="btn btn-primary" type="button" disabled={!actionsEnabled} onClick={() => void onApply()}>
          Apply update
        </button>
        <button className="btn" type="button" disabled={!actionsEnabled} onClick={() => void onRollback()}>
          Roll back update
        </button>
      </div>

      {updating ? (
        <p className="banner" role="status">
          Updating. The control plane restarts during the update and this page reconnects on its
          own. Guests keep running. This is not an infrastructure restart.
        </p>
      ) : null}

      {applyOutcome?.status === "succeeded" ? (
        <p className="banner" role="status">
          Update installed{applyOutcome.version ? ` (${applyOutcome.version})` : ""}. Reload to use
          the new interface.{" "}
          <button className="btn" type="button" onClick={() => window.location.reload()}>
            Reload
          </button>
        </p>
      ) : null}

      {applyOutcome?.status === "failed" ? (
        <p className="banner banner-error banner-pre" role="alert">
          The update failed. {applyOutcome.error || ""}
        </p>
      ) : null}

      {repositoryMissing ? (
        <p className="banner banner-warn" role="status">
          This host is not subscribed to the signed release repository, so new releases cannot be
          found or installed here.{" "}
          {mutate ? (
            <button className="btn" type="button" disabled={busy} onClick={() => void onEnableRepository()}>
              Enable release repository
            </button>
          ) : null}
        </p>
      ) : null}

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

      {available ? (
        <p className={available.version ? "banner banner-warn" : "banner"} role="status">
          {available.version ? `Version ${available.version} is available.` : "No update is available."}
          {available.version && available.url ? (
            <>
              {" "}
              <a href={available.url} target="_blank" rel="noopener noreferrer">
                View release on GitHub
              </a>
            </>
          ) : null}
        </p>
      ) : null}

      {loadState === "ready" && status ? (
        <>
          <div className="summary-grid">
            <SummaryCard label="Channel" value={status.channel || "Not reported"} />
            <SummaryCard label="Host" value={hostSupported ? "Supported" : "Unsupported"} meta={hostSupported ? status.host_reason || undefined : undefined} />
            <SummaryCard
              label="Last operation"
              value={op ? operationStatusLabel(op.status) : "None"}
              meta={op ? "View details" : undefined}
              onClick={op ? () => setDetailsOpen(true) : undefined}
            />
          </div>
          <article className="panel">
            <h2>Host support</h2>
            {hostSupported ? (
              <dl className="definition-list">
                <div>
                  <dt>Channel</dt>
                  <dd>{status.channel}</dd>
                </div>
                <div>
                  <dt>Host</dt>
                  <dd>Supported</dd>
                </div>
                <div>
                  <dt>Detail</dt>
                  <dd>{status.host_reason || "None"}</dd>
                </div>
              </dl>
            ) : (
              <>
                <p className="banner banner-warn" role="status">
                  Unsupported
                  {status.host_reason ? `. ${status.host_reason}` : "."}
                </p>
                <p>
                  Platform updates are not available on this host. The UI will not pretend an upgrade
                  succeeded.
                </p>
              </>
            )}
            <p className="field-hint">
              On supported Debian hosts, updates use the signed Debian repository configured at
              install time. This page never reports package-manager success on its own.
            </p>
          </article>

          <article className="panel">
            <h2>Packages</h2>
            {status.packages.length === 0 ? (
              <p>Not configured</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Version</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {status.packages.map((pkg) => (
                      <tr key={pkg.name}>
                        <td>{pkg.name}</td>
                        <td>{pkg.version || "Not reported"}</td>
                        <td>{packageStatusLabel(pkg.status)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </article>

          {preflight ? (
            <article className="panel">
              <h2>Preflight</h2>
              <dl className="definition-list">
                <div>
                  <dt>Result</dt>
                  <dd>{preflight.ok ? "Ok" : "Not ready"}</dd>
                </div>
                <div>
                  <dt>Kernel</dt>
                  <dd>{preflight.kernel_ok ? "Ok" : "Not ok"}</dd>
                </div>
                <div>
                  <dt>ZFS</dt>
                  <dd>{preflight.zfs_ok ? "Ok" : "Not ok"}</dd>
                </div>
                <div>
                  <dt>NVIDIA</dt>
                  <dd>{preflight.nvidia_ok ? "Ok" : "Not ok"}</dd>
                </div>
              </dl>
              {preflight.checks.length === 0 ? (
                <p>No checks reported.</p>
              ) : (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Check</th>
                        <th>Status</th>
                        <th>Detail</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preflight.checks.map((check) => (
                        <tr key={check.name}>
                          <td>{check.name}</td>
                          <td>{checkStatusLabel(check.status)}</td>
                          <td>{check.detail || "None"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </article>
          ) : null}

          {checkpoint ? (
            <article className="panel">
              <h2>Checkpoint</h2>
              <dl className="definition-list">
                <div>
                  <dt>ID</dt>
                  <dd>{checkpoint.id}</dd>
                </div>
                <div>
                  <dt>Locator</dt>
                  <dd>{checkpoint.locator || "Not reported"}</dd>
                </div>
                <div>
                  <dt>Postgres dump</dt>
                  <dd>{checkpoint.postgres_dump ? "Yes" : "No"}</dd>
                </div>
                <div>
                  <dt>Status</dt>
                  <dd>{checkpointStatusLabel(checkpoint.status)}</dd>
                </div>
              </dl>
            </article>
          ) : null}
        </>
      ) : null}

      <Dialog open={detailsOpen && op != null} title="Last operation" onClose={() => setDetailsOpen(false)}>
        {op ? (
          <dl className="definition-list">
            <div>
              <dt>ID</dt>
              <dd>{op.id}</dd>
            </div>
            <div>
              <dt>Action</dt>
              <dd>{op.action}</dd>
            </div>
            <div>
              <dt>Status</dt>
              <dd>{operationStatusLabel(op.status)}</dd>
            </div>
            <div>
              <dt>Dry run</dt>
              <dd>{op.dry_run ? "Yes" : "No"}</dd>
            </div>
            <div>
              <dt>Packages</dt>
              <dd>{op.packages?.length ? op.packages.join(", ") : "None"}</dd>
            </div>
            <div>
              <dt>Started</dt>
              <dd>{formatWhen(op.started_at)}</dd>
            </div>
            <div>
              <dt>Finished</dt>
              <dd>{formatWhen(op.finished_at)}</dd>
            </div>
            <div>
              <dt>Error</dt>
              <dd>{op.error || "None"}</dd>
            </div>
          </dl>
        ) : null}
      </Dialog>
    </section>
  );
}
