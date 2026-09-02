import { useState } from "react";
import { ApiError, askAI } from "../api/client";
import type { AIAskResponse } from "../generated/openapi";
import { Field } from "../components/Field";
import { useSession } from "../session";

function canAsk(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator") || roles?.includes("viewer"));
}

export function AskPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const allowed = canAsk(roles);
  const [prompt, setPrompt] = useState("Why did this workload restart?");
  const [result, setResult] = useState<AIAskResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onAsk() {
    setBusy(true);
    setError(null);
    try {
      setResult(await askAI({ prompt }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Ask failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>Ask</h1>
        <p className="lede">
          Read-only assistant. BYO providers are optional. Offline install has no AI vendor and the platform still works.
          Ask cites events and metrics. It cannot Host.Exec and it cannot mutate.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {allowed ? (
        <article className="panel">
          <form
            className="stack"
            onSubmit={(ev) => {
              ev.preventDefault();
              void onAsk();
            }}
          >
            <Field id="ask-prompt" label="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Ask
            </button>
          </form>
        </article>
      ) : (
        <p>Ask is unavailable for this role.</p>
      )}
      {result ? (
        <article className="panel">
          <h2>Answer</h2>
          <p>{result.answer}</p>
          <p>
            Provider {result.provider_status}. Mode {result.mode}. Mutate {result.mutate ? "yes" : "no"}.
          </p>
          {result.citations && result.citations.length > 0 ? (
            <ul className="plain-list">
              {result.citations.map((c, i) => (
                <li key={`${c.kind}-${c.ref}-${i}`}>
                  <strong>{c.kind}</strong> {c.ref}: {c.summary}
                </li>
              ))}
            </ul>
          ) : (
            <p>No citations.</p>
          )}
        </article>
      ) : null}
    </section>
  );
}
