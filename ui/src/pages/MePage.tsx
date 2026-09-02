import { useState } from "react";
import { ApiError, patchMe } from "../api/client";
import { PageHeader } from "../components/PageHeader";
import { useSession } from "../session";
import { uxLevel, type UXLevel } from "../ux";

const LEVELS: { id: UXLevel; label: string; hint: string }[] = [
  { id: "guided", label: "Guided", hint: "Step through create forms. Same APIs as Advanced." },
  { id: "advanced", label: "Advanced", hint: "One form with every field visible. Same APIs as Guided." },
  {
    id: "expert",
    label: "Expert",
    hint: "Same forms plus a read-only JSON preview of the request body. Does not grant permissions.",
  },
];

export function MePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [level, setLevel] = useState<UXLevel>(uxLevel(user));
  const [ack, setAck] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!user) {
    return null;
  }

  const alreadyAcked = Boolean(user.expert_ack);

  async function onSave() {
    setBusy(true);
    setError(null);
    try {
      if (level === "expert" && !alreadyAcked && !ack) {
        setError("Expert requires a one-time acknowledgement.");
        return;
      }
      const next = await patchMe({
        ux_level: level,
        expert_ack: ack || undefined,
      });
      session.applyUser(next);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page form-narrow" aria-labelledby="me-heading">
      <PageHeader id="me-heading" title="Account" kicker="Identity for the current session" />
      <section className="section">
        <dl className="definition-list">
          <div>
            <dt>Username</dt>
            <dd>{user.username}</dd>
          </div>
          <div>
            <dt>User ID</dt>
            <dd>
              <code>{user.user_id}</code>
            </dd>
          </div>
          <div>
            <dt>Roles</dt>
            <dd>{user.roles.length > 0 ? user.roles.join(", ") : "None"}</dd>
          </div>
          <div>
            <dt>Edition</dt>
            <dd>{user.edition}</dd>
          </div>
          {user.cluster_id ? (
            <div>
              <dt>Cluster ID</dt>
              <dd>
                <code>{user.cluster_id}</code>
              </dd>
            </div>
          ) : null}
        </dl>
      </section>
      <section className="section stack">
        <h2>Operator UX</h2>
        <p className="lede">
          Guided, Advanced, and Expert change how forms are shown. They never change authorization.
        </p>
        {error ? (
          <p className="banner banner-error" role="alert">
            {error}
          </p>
        ) : null}
        <fieldset className="stack">
          <legend className="field-label">UX level</legend>
          {LEVELS.map((item) => (
            <div key={item.id}>
              <label className="field-label">
                <input
                  type="radio"
                  name="ux-level"
                  value={item.id}
                  checked={level === item.id}
                  onChange={() => setLevel(item.id)}
                />{" "}
                {item.label}
              </label>
              <p className="field-hint">{item.hint}</p>
            </div>
          ))}
        </fieldset>
        {level === "expert" && !alreadyAcked ? (
          <label className="check-row">
            <input type="checkbox" checked={ack} onChange={(event) => setAck(event.target.checked)} /> I understand
            Expert mode does not grant extra permissions and only shows more of the same APIs.
          </label>
        ) : null}
        {alreadyAcked ? <p className="field-hint">Expert acknowledgement is recorded for this account.</p> : null}
        <div className="btn-row">
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onSave()}>
            Save UX preferences
          </button>
        </div>
      </section>
    </section>
  );
}
