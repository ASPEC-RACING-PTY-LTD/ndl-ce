import { useEffect, useState } from "react";
import { createAlert, createAlertChannel, listAlertChannels, listAlerts } from "../api/client";
import type { AlertRule, NotificationChannel } from "../generated/openapi";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
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
  const [dialog, setDialog] = useState<"rule" | "webhook" | null>(null);

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
      <PageHeader
        id="alerts-heading"
        title="Alerts"
        kicker="Local rules write events. Webhook URLs are secrets. SMTP stays not configured until a host is set."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-primary" type="button" onClick={() => setDialog("rule")}>
              Create rule
            </button>
            <button className="btn" type="button" onClick={() => setDialog("webhook")}>
              Add webhook
            </button>
          </div>
        }
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <section className="section-block" aria-labelledby="alert-rules-heading">
        <h2 id="alert-rules-heading">Rules</h2>
        <div className="card-grid">
          {rules == null ? (
            <article className="panel dashboard-card empty-card">
              <p>Collecting</p>
            </article>
          ) : rules.length === 0 ? (
            <article className="panel dashboard-card empty-card">
              <p className="empty-title">No alert rules yet</p>
              <button className="btn btn-primary" type="button" onClick={() => setDialog("rule")}>
                Create rule
              </button>
            </article>
          ) : (
            rules.map((rule) => (
              <article className="panel dashboard-card" key={rule.id}>
                <h3>{rule.name}</h3>
                <p>
                  {rule.metric} {rule.op} {rule.threshold}
                </p>
                {rule.last_fired_at ? <p className="muted">Last fired {formatWhen(rule.last_fired_at)}</p> : null}
              </article>
            ))
          )}
        </div>
      </section>
      <section className="section-block" aria-labelledby="alert-channels-heading">
        <h2 id="alert-channels-heading">Notification channels</h2>
        <div className="card-grid">
          {channels.length === 0 ? (
            <article className="panel dashboard-card empty-card">
              <p className="empty-title">Not configured</p>
            </article>
          ) : (
            channels.map((ch) => (
              <article className="panel dashboard-card" key={ch.id}>
                <h3>{ch.name}</h3>
                <p className="muted">
                  {ch.kind} {ch.status}
                  {ch.webhook_configured ? " webhook configured" : ""}
                </p>
              </article>
            ))
          )}
        </div>
      </section>
      <ConfirmDialog
        open={dialog === "rule"}
        title="Create alert rule"
        confirmLabel="Create rule"
        onClose={() => setDialog(null)}
        onConfirm={() => {
          void createAlert({ name, metric, op, threshold: Number(threshold) })
            .then(() => {
              setDialog(null);
              return refresh();
            })
            .catch((err: unknown) => setError(err instanceof Error ? err.message : "Unavailable"));
        }}
      >
        <Field id="alert-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <Field id="alert-metric" label="Metric" value={metric} onChange={(e) => setMetric(e.target.value)} />
        <div className="field">
          <label className="field-label" htmlFor="alert-op">
            Operator
          </label>
          <select id="alert-op" className="field-input" value={op} onChange={(e) => setOp(e.target.value)}>
            <option value="gt">greater than</option>
            <option value="lt">less than</option>
          </select>
        </div>
        <Field id="alert-threshold" label="Threshold" value={threshold} onChange={(e) => setThreshold(e.target.value)} />
      </ConfirmDialog>
      <ConfirmDialog
        open={dialog === "webhook"}
        title="Add webhook"
        confirmLabel="Add webhook"
        onClose={() => setDialog(null)}
        onConfirm={() => {
          void createAlertChannel({ name: "webhook", kind: "webhook", url: hookURL })
            .then(() => {
              setHookURL("");
              setDialog(null);
              return refresh();
            })
            .catch((err: unknown) => setError(err instanceof Error ? err.message : "Unavailable"));
        }}
      >
        <Field
          id="alert-webhook-url"
          label="Webhook URL"
          value={hookURL}
          onChange={(e) => setHookURL(e.target.value)}
          autoComplete="off"
        />
      </ConfirmDialog>
    </section>
  );
}
