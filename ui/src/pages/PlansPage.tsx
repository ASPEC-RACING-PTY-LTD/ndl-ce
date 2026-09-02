import { useEffect, useState } from "react";
import { ApiError, approveAIPlan, createAIPlan, listAIPlans } from "../api/client";
import type { AIPlan } from "../generated/openapi";
import { Field } from "../components/Field";
import { useSession } from "../session";

function canOperate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function PlansPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const operate = canOperate(roles);
  const [prompt, setPrompt] = useState("install a database on node-02");
  const [plans, setPlans] = useState<AIPlan[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    setPlans((await listAIPlans()).items ?? []);
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

  async function onPlan() {
    setBusy(true);
    setError(null);
    try {
      await createAIPlan({ prompt });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Plan failed");
    } finally {
      setBusy(false);
    }
  }

  async function onApprove(id: string) {
    if (!window.confirm("Approve this plan? Destructive confirm still applies. This is not a shell.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await approveAIPlan(id);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Approve failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>Plans</h1>
        <p className="lede">
          Reviewable existing APIs. AI cannot Host.Exec. Approve executes with the same RBAC. Automate binds to Phase 40
          policies. Partial failure stops and audit remains.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <form
          className="stack"
          onSubmit={(ev) => {
            ev.preventDefault();
            void onPlan();
          }}
        >
          <Field id="plan-prompt" label="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
          <button className="btn btn-primary" type="submit" disabled={busy}>
            Create plan
          </button>
        </form>
      </article>
      <article className="panel">
        <h2>Preview</h2>
        {plans == null ? (
          <p>Collecting</p>
        ) : plans.length === 0 ? (
          <p>No plans yet.</p>
        ) : (
          <ul className="plain-list">
            {plans.map((plan) => (
              <li key={plan.id}>
                <h3>{plan.status}</h3>
                <p>{plan.prompt}</p>
                <p>Actor {plan.actor_type}. {plan.reason}</p>
                {(plan.steps ?? []).map((step) => (
                  <p key={step.id}>
                    {step.method} {step.path} ({step.permission}) {step.status}
                  </p>
                ))}
                {operate && plan.status === "preview" ? (
                  <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onApprove(plan.id ?? "")}>
                    Approve
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
