import { useEffect, useState } from "react";
import { getMigrationJobDiagnostics } from "../api/client";
import { asJobRecord, isRetryableMigrationState, jobHeadline, jobStateOf, migrationJobFields } from "../migration/jobView";
import { ActivityDetail } from "./ActivityDetail";

export function MigrationJobDetail({
  job,
  canRetry,
  onClose,
  onRetry,
  onOpenProgress,
}: {
  job: Record<string, unknown>;
  canRetry?: boolean;
  onClose?: () => void;
  onRetry?: (id: string) => void;
  onOpenProgress?: (job: Record<string, unknown>) => void;
}) {
  const id = String(job.id ?? "");
  const [bundle, setBundle] = useState<Record<string, unknown> | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) {
      return;
    }
    let cancelled = false;
    setBundle(null);
    setLoadError(null);
    void getMigrationJobDiagnostics(id)
      .then((row) => {
        if (!cancelled) {
          setBundle(row);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setLoadError(err instanceof Error ? err.message : "Diagnostics unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const latest = asJobRecord(bundle?.job) ? { ...job, ...asJobRecord(bundle?.job) } : job;
  const title = jobHeadline(latest);
  const retryable = canRetry && isRetryableMigrationState(jobStateOf(latest));

  return (
    <ActivityDetail
      title={title}
      fields={migrationJobFields(latest, bundle)}
      raw={latest}
      diagnostics={bundle}
      onClose={onClose}
      extraActions={
        <>
          {loadError ? <span className="muted">{loadError}</span> : null}
          {onOpenProgress ? (
            <button type="button" className="btn btn-ghost" onClick={() => onOpenProgress(latest)}>
              Open Progress
            </button>
          ) : null}
          {retryable && onRetry ? (
            <button type="button" className="btn btn-ghost" onClick={() => onRetry(id)}>
              Retry
            </button>
          ) : null}
        </>
      }
    />
  );
}
