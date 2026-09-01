import { useEffect, useState } from "react";
import {
  ApiError,
  applyUpdates,
  checkUpdates,
  checkpointUpdates,
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

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
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

function previewActionLabel(action: string): string {
  switch (action) {
    case "hold":
      return "Hold";
    case "upgrade":
      return "Upgrade";
    case "unsupported":
      return "Unsupported";
    default:
      return honestStatus(action);
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
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);

  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [preview, setPreview] = useState<UpdatePreview | null>(null);
  const [preflight, setPreflight] = useState<UpdatePreflight | null>(null);
  const [checkpoint, setCheckpoint] = useState<UpdateCheckpoint | null>(null);
  const [lastOp, setLastOp] = useState<UpdateOperation | null>(null);
  const [loadState, setLoadState] = useState<"collecting" | "ready" | "unavailable">("collecting");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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
    return () => {
      cancelled = true;
    };
  }, []);

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
        "Apply control-plane package updates? Guests must keep running. Confirmation is sent as X-Nodal-Confirm: apply-update.",
      )
    ) {
      return;
    }
    await runAction(async () => {
      const next = await applyUpdates();
      setLastOp(next);
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
  const actionsEnabled = mutate && hostSupported && !busy;

  return (
    <section className="page page-wide" aria-labelledby="updates-heading">
      <header className="page-header">
        <h1 id="updates-heading">Updates</h1>
        <p className="page-kicker">
          Control-plane package bumps must not stop guests. Split packages update the management
          plane while workloads keep running.
        </p>
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

      {loadState === "ready" && status ? (
        <>
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

          <article className="panel">
            <h2>Last operation</h2>
            {lastOp || status.last_operation ? (
              (() => {
                const op = lastOp ?? status.last_operation!;
                return (
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
                );
              })()
            ) : (
              <p>Not configured</p>
            )}
          </article>

          <article className="panel">
            <h2>Actions</h2>
            {!mutate ? (
              <p className="banner" role="status">
                Update actions are read-only for your role. An operator or administrator can check,
                preflight, checkpoint, apply, or roll back.
              </p>
            ) : !hostSupported ? (
              <p className="muted">
                Actions stay disabled while the host is Unsupported. Check, apply, and rollback will
                not be treated as successful.
              </p>
            ) : (
              <p className="muted">
                Check is always a dry run. Apply and rollback require confirmation and send
                X-Nodal-Confirm.
              </p>
            )}
            <div className="btn-row">
              <button
                className="btn"
                type="button"
                disabled={!actionsEnabled}
                onClick={() => void onCheck()}
              >
                Check for updates
              </button>
              <button
                className="btn"
                type="button"
                disabled={!actionsEnabled}
                onClick={() => void onPreflight()}
              >
                Run preflight
              </button>
              <button
                className="btn"
                type="button"
                disabled={!actionsEnabled}
                onClick={() => void onCheckpoint()}
              >
                Create checkpoint
              </button>
              <button
                className="btn btn-primary"
                type="button"
                disabled={!actionsEnabled}
                onClick={() => void onApply()}
              >
                Apply update
              </button>
              <button
                className="btn"
                type="button"
                disabled={!actionsEnabled}
                onClick={() => void onRollback()}
              >
                Roll back update
              </button>
            </div>
          </article>

          {preview ? (
            <article className="panel">
              <h2>Preview</h2>
              <p className="muted">
                Dry run: {preview.dry_run ? "Yes" : "No"}. Channel: {preview.channel}.
              </p>
              {preview.changelog ? <p>{preview.changelog}</p> : <p>No changelog reported.</p>}
              {preview.items.length === 0 ? (
                <p>No package changes in this preview.</p>
              ) : (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Current</th>
                        <th>Candidate</th>
                        <th>Action</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.items.map((item) => (
                        <tr key={item.name}>
                          <td>{item.name}</td>
                          <td>{item.current_version || "Not reported"}</td>
                          <td>{item.candidate_version || "Not reported"}</td>
                          <td>{previewActionLabel(item.action)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </article>
          ) : null}

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
    </section>
  );
}
