import { useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { DeleteButton } from "../components/DeleteButton";
import {
  ApiError,
  applyAutomationPolicy,
  createAutomationPolicy,
  listAutomationPolicies,
  listAutomationPolicyRuns,
  listSavedScripts,
  type SavedScript,
} from "../api/client";
import type { AutomationPolicy, AutomationPolicyRun } from "../generated/openapi";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { useSession } from "../session";
import { ErrorNotice } from "../components/ErrorNotice";

type PolicyKind = "storage_pressure" | "uptime_check" | "docker_fix" | "docker_script";

const KIND_LABELS: Record<PolicyKind, string> = {
  storage_pressure: "Storage pressure: queue a migrate",
  uptime_check: "Uptime: check sites and recover failing ones",
  docker_fix: "Docker: fix unhealthy containers",
  docker_script: "Docker: run a saved script",
};

function describe(item: AutomationPolicy): string {
  const target = (item as AutomationPolicy & { target?: string }).target;
  switch (item.kind) {
    case "uptime_check":
      return `Checks ${target ? `the ${target} monitor` : "every Uptime monitor"} and restarts the containers of any that are down.`;
    case "docker_fix":
      return `Restarts unhealthy containers ${target ? `on ${target}` : "on every machine"}, dependencies first, and starts stopped tunnels.`;
    case "docker_script":
      return `Runs the saved Docker script ${target ?? ""} once.`;
    default:
      return `${item.kind} ${item.action} at ${item.threshold_percent} percent.`;
  }
}

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
  const [kind, setKind] = useState<PolicyKind>("storage_pressure");
  const [target, setTarget] = useState("");
  const [scripts, setScripts] = useState<SavedScript[]>([]);

  useEffect(() => {
    if (kind === "docker_script") {
      void listSavedScripts("docker")
        .then((r) => setScripts(r.items ?? []))
        .catch(() => setScripts([]));
    }
  }, [kind]);

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
        kind,
        ...(kind === "storage_pressure" ? { action: "enqueue_migrate_low_priority", threshold_percent: Number(threshold) || 85 } : {}),
        require_approval: requireApproval,
        ...(target.trim() ? { target: target.trim() } : {}),
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
      <PageHeader
        id="automation-heading"
        title="Automation"
        kicker={
          <>
            Deterministic policies. This is not an LLM loop. Policies cannot Host.Exec. The agent has no policy engine.
            Storage pressure above the threshold queues a migrate of the lowest-priority VM. Queued migrate is not live
            until the dest agent is connected. See <Link href="/tasks">Tasks</Link>.
          </>
        }
      />
      {error ? (
        <ErrorNotice error={error} />
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
                  {describe(item)}
                  {item.require_approval ? " Approval required." : ""}
                </p>
                {mutate ? (
                  <div className="btn-row">
                    <button
                      className="btn btn-primary"
                      type="button"
                      disabled={busy}
                      onClick={() => void onApply(item)}
                    >
                      Apply policy
                    </button>
                    <DeleteButton
                      path={`/policies/${item.id ?? ""}`}
                      name={item.name ?? "policy"}
                      noun="policy"
                      description={<p>The policy is removed. Its past runs stay in the history.</p>}
                      onDeleted={() => reload()}
                    />
                  </div>
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
            <div className="field">
              <label className="field-label" htmlFor="policy-kind">
                What it does
              </label>
              <select
                id="policy-kind"
                className="field-input"
                value={kind}
                onChange={(e) => {
                  setKind(e.target.value as PolicyKind);
                  setTarget("");
                }}
              >
                {(Object.keys(KIND_LABELS) as PolicyKind[]).map((k) => (
                  <option key={k} value={k}>
                    {KIND_LABELS[k]}
                  </option>
                ))}
              </select>
            </div>
            {kind === "uptime_check" ? (
              <Field
                id="policy-target"
                label="Monitor name"
                hint="Leave empty to check every enabled monitor."
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              />
            ) : null}
            {kind === "docker_fix" ? (
              <Field
                id="policy-target"
                label="Machine"
                hint="Leave empty to fix every machine. Ignored containers are skipped."
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              />
            ) : null}
            {kind === "docker_script" ? (
              <div className="field">
                <label className="field-label" htmlFor="policy-target">
                  Saved script
                </label>
                <select id="policy-target" className="field-input" value={target} onChange={(e) => setTarget(e.target.value)}>
                  <option value="">{scripts.length ? "Choose a script" : "No saved Docker scripts yet (save one from the Docker page)"}</option>
                  {scripts.map((s) => (
                    <option key={s.id} value={s.name}>
                      {s.name}
                    </option>
                  ))}
                </select>
                <p className="field-hint">Scripts run as the automation identity: exec and tunnel steps are refused.</p>
              </div>
            ) : null}
            {kind === "storage_pressure" ? (
            <Field
              id="policy-threshold"
              label="Threshold percent"
              hint="If a pool's physical filesystem used percent is at or above this threshold, enqueue migrate of the lowest-priority VM. Logical provisioned size is not a reservation. Queued migrate is not live until the dest agent is connected."
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
            />
            ) : null}
            <label>
              <input
                type="checkbox"
                checked={requireApproval}
                onChange={(e) => setRequireApproval(e.target.checked)}
              />{" "}
              Require approval
            </label>
            <button className="btn btn-primary" type="submit" disabled={busy || (kind === "docker_script" && !target)}>
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
