import { useEffect, useState } from "react";
import { ApiError, getIAMSettings, saveAutomationCreators, type IAMRole, type IAMSettings } from "../api/client";
import { ErrorNotice } from "../components/ErrorNotice";
import { useSession } from "../session";

type Mode = "min" | "roles";

/**
 * AutomationCreatorsPanel sets who may create and edit automation flows:
 * a minimum role, or chosen roles. Admins can change it until a SuperAdmin
 * saves it; after that only SuperAdmins can, unless they let admins.
 */
export function AutomationCreatorsPanel({ roles }: { roles: IAMRole[] }) {
  const session = useSession();
  const superAdmin = session.status === "ready" && Boolean(session.user?.roles?.includes("superadmin"));
  const [settings, setSettings] = useState<IAMSettings | null>(null);
  const [mode, setMode] = useState<Mode>("min");
  const [minRole, setMinRole] = useState("admin");
  const [chosen, setChosen] = useState<string[]>([]);
  const [adminsMayEdit, setAdminsMayEdit] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  function apply(s: IAMSettings) {
    const c = s.automation_creators;
    setSettings(s);
    setMode(c.roles.length > 0 ? "roles" : "min");
    setMinRole(c.min_role || "admin");
    setChosen(c.roles);
    setAdminsMayEdit(c.admins_may_edit);
  }

  useEffect(() => {
    getIAMSettings()
      .then(apply)
      .catch(() => setError("The automation creators setting is unavailable"));
  }, []);

  if (!settings) {
    return error ? <ErrorNotice error={error} /> : null;
  }
  const c = settings.automation_creators;
  const choices = roles.filter((r) => r.name !== "automation");

  async function save() {
    setSaving(true);
    setError(null);
    setSaved(false);
    try {
      const body = mode === "roles" ? { roles: chosen } : { min_role: minRole };
      apply(await saveAutomationCreators(superAdmin ? { ...body, admins_may_edit: adminsMayEdit } : body));
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="iam-section creators-panel" aria-labelledby="iam-creators-heading">
      <h3 id="iam-creators-heading" className="iam-section-title">
        Automation creators
      </h3>
      <p className="field-hint">
        Who may create and edit automation flows and connections. Flows run as the system; steps that are hard to undo still need
        approval unless a SuperAdmin built the flow.
        {c.locked && !c.admins_may_edit ? " A SuperAdmin has locked this setting." : ""}
        {c.locked && c.admins_may_edit ? " A SuperAdmin saved this setting and lets admins change it." : ""}
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <fieldset className="creators-fields" disabled={!c.can_edit || saving}>
        <label className="check-row">
          <input type="radio" name="creators-mode" checked={mode === "min"} onChange={() => setMode("min")} />
          <span>This role and every role ranked above it</span>
        </label>
        {mode === "min" ? (
          <select className="field-input" aria-label="Minimum role" value={minRole} onChange={(e) => setMinRole(e.target.value)}>
            {choices.map((r) => (
              <option key={r.name} value={r.name}>
                {r.title}
              </option>
            ))}
          </select>
        ) : null}
        <label className="check-row">
          <input type="radio" name="creators-mode" checked={mode === "roles"} onChange={() => setMode("roles")} />
          <span>Only these roles</span>
        </label>
        {mode === "roles" ? (
          <div className="creators-roles">
            {choices.map((r) => (
              <label key={r.name} className="check-row">
                <input
                  type="checkbox"
                  checked={chosen.includes(r.name)}
                  onChange={(e) => setChosen((cur) => (e.target.checked ? [...cur, r.name] : cur.filter((x) => x !== r.name)))}
                />
                <span>{r.title}</span>
              </label>
            ))}
          </div>
        ) : null}
        {superAdmin ? (
          <label className="check-row">
            <input type="checkbox" checked={adminsMayEdit} onChange={(e) => setAdminsMayEdit(e.target.checked)} />
            <span>Let admins change this</span>
          </label>
        ) : null}
        <div className="btn-row">
          <button className="btn btn-sm btn-primary" type="button" disabled={mode === "roles" && chosen.length === 0} onClick={() => void save()}>
            {saving ? "Saving" : "Save"}
          </button>
          {saved ? <span className="muted">Saved{superAdmin && !adminsMayEdit ? "; only SuperAdmins can change it now" : ""}.</span> : null}
        </div>
      </fieldset>
    </section>
  );
}
