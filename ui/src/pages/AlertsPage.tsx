import { useEffect, useState } from "react";
import { createAlert, createAlertChannel, listAlertChannels, listAlerts } from "../api/client";
import type { AlertRule, NotificationChannel } from "../generated/openapi";
import { formatWhen } from "../format";

export function AlertsPage() {
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("cpu high");
  const [metric, setMetric] = useState("cpu.busy_ratio");
  const [op, setOp] = useState("gt");
  const [threshold, setThreshold] = useState("0.9");
  const [hookURL, setHookURL] = useState("");

  async function refresh() {
    try {
      const [a, c] = await Promise.all([listAlerts(), listAlertChannels()]);
      setRules(a.items ?? []);
      setChannels(c.items ?? []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="alerts-heading">
      <header className="page-header">
        <h1 id="alerts-heading">Alerts</h1>
        <p className="page-kicker">Local rules write events. Webhook URLs are secrets. SMTP stays not configured until a host is set.</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <h2>Rules</h2>
        {rules == null ? (
          <p>Collecting</p>
        ) : rules.length === 0 ? (
          <p>No alert rules yet.</p>
        ) : (
          <ul className="plain-list">
            {rules.map((rule) => (
              <li key={rule.id}>
                <strong>{rule.name}</strong> {rule.metric} {rule.op} {rule.threshold}
                {rule.last_fired_at ? <span className="muted"> last fired {formatWhen(rule.last_fired_at)}</span> : null}
              </li>
            ))}
          </ul>
        )}
        <form
          className="stack"
          onSubmit={(ev) => {
            ev.preventDefault();
            void createAlert({ name, metric, op, threshold: Number(threshold) })
              .then(() => refresh())
              .catch((err: unknown) => setError(err instanceof Error ? err.message : "Unavailable"));
          }}
        >
          <label>
            Name
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label>
            Metric
            <input value={metric} onChange={(e) => setMetric(e.target.value)} />
          </label>
          <label>
            Operator
            <select value={op} onChange={(e) => setOp(e.target.value)}>
              <option value="gt">greater than</option>
              <option value="lt">less than</option>
            </select>
          </label>
          <label>
            Threshold
            <input value={threshold} onChange={(e) => setThreshold(e.target.value)} />
          </label>
          <button className="btn btn-primary" type="submit">
            Create rule
          </button>
        </form>
      </article>
      <article className="panel">
        <h2>Notification channels</h2>
        {channels.length === 0 ? (
          <p>Not configured</p>
        ) : (
          <ul className="plain-list">
            {channels.map((ch) => (
              <li key={ch.id}>
                {ch.name} ({ch.kind}) {ch.status}
                {ch.webhook_configured ? " webhook configured" : ""}
              </li>
            ))}
          </ul>
        )}
        <form
          className="stack"
          onSubmit={(ev) => {
            ev.preventDefault();
            void createAlertChannel({ name: "webhook", kind: "webhook", url: hookURL })
              .then(() => {
                setHookURL("");
                return refresh();
              })
              .catch((err: unknown) => setError(err instanceof Error ? err.message : "Unavailable"));
          }}
        >
          <label>
            Webhook URL
            <input value={hookURL} onChange={(e) => setHookURL(e.target.value)} autoComplete="off" />
          </label>
          <button className="btn" type="submit">
            Add webhook
          </button>
        </form>
      </article>
    </section>
  );
}
