import { useEffect, useState } from "react";
import { ApiError, listAlertChannels, setDeployWebhook, type DeployProject, type DeployProjectConfig } from "../api/client";
import type { NotificationChannel } from "../generated/openapi";
import { confirmAction } from "../confirm";
import { ErrorNotice } from "../components/ErrorNotice";

const EVENTS: { id: string; label: string }[] = [
  { id: "deploy.failed", label: "A deployment fails" },
  { id: "deploy.succeeded", label: "A deployment succeeds" },
  { id: "update.available", label: "New commits are found" },
  { id: "bulk.finished", label: "A bulk deployment finishes (summary)" },
];

/** NotifySection picks which events a project sends, and where. */
export function NotifySection({ cfg, set }: { cfg: DeployProjectConfig; set: (c: DeployProjectConfig) => void }) {
  const [channels, setChannels] = useState<NotificationChannel[] | null>(null);
  useEffect(() => {
    listAlertChannels()
      .then((r) => setChannels(r.items ?? []))
      .catch(() => setChannels([]));
  }, []);
  const events = cfg.notify_events ?? [];
  const chosen = cfg.notify_channels ?? [];
  const flip = (list: string[], id: string, on: boolean) => (on ? [...list.filter((x) => x !== id), id] : list.filter((x) => x !== id));
  return (
    <fieldset>
      <legend>Notifications</legend>
      {EVENTS.map((e) => (
        <label key={e.id} className="check-row">
          <input type="checkbox" checked={events.includes(e.id)} onChange={(ev) => set({ ...cfg, notify_events: flip(events, e.id, ev.target.checked) })} />{" "}
          {e.label}
        </label>
      ))}
      {channels && channels.length ? (
        <>
          <p className="bk-hint">Send to {chosen.length ? "these channels" : "every notification channel"}:</p>
          {channels.map((c) => (
            <label key={c.id} className="check-row">
              <input
                type="checkbox"
                checked={chosen.includes(c.id ?? "")}
                onChange={(ev) => set({ ...cfg, notify_channels: flip(chosen, c.id ?? "", ev.target.checked) })}
              />{" "}
              {c.name} <span className="bk-hint">({c.kind})</span>
            </label>
          ))}
        </>
      ) : (
        <p className="bk-hint">No notification channels yet. Add a webhook channel under Alerts; Slack and Discord webhooks work as they are.</p>
      )}
    </fieldset>
  );
}

/** WebhookSection turns a project's push webhook on or off. */
export function WebhookSection({ project, onChanged }: { project: DeployProject; onChanged: () => void }) {
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const url = `${window.location.origin}${project.webhook_path ?? `/api/v1/hooks/deploy/${project.id}`}`;
  async function change(enabled: boolean) {
    setBusy(true);
    setError(null);
    try {
      const res = await setDeployWebhook(project.id, enabled);
      setSecret(res.secret || null);
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : "Could not change the webhook");
    } finally {
      setBusy(false);
    }
  }
  const on = !!project.config.webhook;
  return (
    <section className="dp-form">
      <fieldset>
        <legend>Push webhook</legend>
        <p className="bk-hint">
          Point a CodeHold or GitHub push webhook here (content type JSON, with the secret), or call it from a CI job with
          <code> Authorization: Bearer &lt;secret&gt;</code>. A push checks the project at once; it deploys only if the project deploys
          automatically.
        </p>
        {error ? <ErrorNotice error={error} /> : null}
        {on ? (
          <label className="field">
            <span className="field-label">URL</span>
            <input className="field-input" readOnly value={url} onFocus={(e) => e.currentTarget.select()} />
          </label>
        ) : null}
        {secret ? (
          <label className="field">
            <span className="field-label">Secret (shown once; copy it now)</span>
            <input className="field-input" readOnly value={secret} onFocus={(e) => e.currentTarget.select()} />
          </label>
        ) : null}
        <div className="bk-actions" style={{ justifyContent: "flex-start" }}>
          {on ? (
            <>
              <button
                className="btn btn-sm"
                type="button"
                disabled={busy}
                onClick={() => {
                  if (confirmAction("Make a new secret? The old one stops working.")) {
                    void change(true);
                  }
                }}
              >
                New secret
              </button>
              <button className="btn btn-sm btn-ghost btn-danger-text" type="button" disabled={busy} onClick={() => void change(false)}>
                Turn off
              </button>
            </>
          ) : (
            <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void change(true)}>
              Turn on
            </button>
          )}
        </div>
      </fieldset>
    </section>
  );
}
