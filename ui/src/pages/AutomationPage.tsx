import { useEffect, useState } from "react";
import {
  ApiError,
  applyAutomationPolicy,
  createAutomationPolicy,
  listAutomationPolicies,
  listAutomationPolicyRuns,
} from "../api/client";
import type { AutomationPolicy, AutomationPolicyRun } from "../generated/openapi";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { useSession } from "../session";

function canApply(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function AutomationPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canApply(roles);
  const [policies, setPolicies] = useState<AutomationPolicy[] | null>(null);
  const [runs, setRuns] = useState<AutomationPolicyRun[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("storage pressure");
  const [threshold, setThreshold] = useState("85");
  const [requireApproval, setRequireApproval] = useState(false);

  async function reload() {
    const [next, history] = await Promise.all([listAutomationPolicies(), listAutomationPolicyRuns()]);
    setPolicies(next.items ?? []);
    setRuns(history.items ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await createAutomationPolicy({
        name,
        kind: "storage_pressure",
        action: "enqueue_migrate_low_priority",
        threshold_percent: Number(threshold) || 85,
        require_approval: requireApproval,
      });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onApply(item: AutomationPolicy) {
    setBusy(true);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.require_approval) {
        if (
          !window.confirm(
            "Apply this policy? It will enqueue migrate of the lowest-priority VM on pressured pools. Queued migrate is not live until the dest agent is connected.",
          )
        ) {
          return;
        }
        confirm = "apply-policy";
      }
      await applyAutomationPolicy(item.id ?? "", confirm);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Apply failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>Automation</h1>
        <p className="lede">
          Deterministic policies. This is not an LLM loop. Policies cannot Host.Exec. The agent has no policy engine.
          Storage pressure above the threshold queues a migrate of the lowest-priority VM. Queued migrate is not live
          until the dest agent is connected. See <Link href="/tasks">Tasks</Link>.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <h2>Policies</h2>
        {policies == null ? (
          <p>Collecting</p>
        ) : policies.length === 0 ? (
          <p>No automation policies yet.</p>
        ) : (
          <ul className="plain-list">
            {policies.map((item) => (
              <li key={item.id}>
                <h3>{item.name}</h3>
                <p>
                  {item.kind} {item.action} at {item.threshold_percent} percent.
                  {item.require_approval ? " Approval required." : ""}
                </p>
                {mutate ? (
                  <button
                    className="btn btn-primary"
                    type="button"
                    disabled={busy}
                    onClick={() => void onApply(item)}
                  >
                    Apply policy
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {mutate ? (
          <form
            className="stack"
            onSubmit={(ev) => {
              ev.preventDefault();
              void onCreate();
            }}
          >
            <Field id="policy-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
            <Field
              id="policy-threshold"
              label="Threshold percent"
              hint="If a pool is at or above this allocated percent, enqueue migrate of the lowest-priority VM. Queued migrate is not live until the dest agent is connected."
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
            />
            <label>
              <input
                type="checkbox"
                checked={requireApproval}
                onChange={(e) => setRequireApproval(e.target.checked)}
              />{" "}
              Require approval
            </label>
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Create policy
            </button>
          </form>
        ) : null}
      </article>
      <article className="panel">
        <h2>Runs</h2>
        {runs.length === 0 ? (
          <p>No policy runs yet.</p>
        ) : (
          <ul className="plain-list">
            {runs.map((run) => (
              <li key={run.id}>
                <strong>{run.status}</strong> as {run.service_identity}. {run.reason}
                {run.operation_ids && run.operation_ids.length > 0 ? (
                  <p>
                    Queued {run.operation_ids.length} operation
                    {run.operation_ids.length === 1 ? "" : "s"}. Queued migrate is not live until the dest agent is
                    connected. See <Link href="/tasks">Tasks</Link>.
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
