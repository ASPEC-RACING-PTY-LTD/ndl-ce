import { useState } from "react";
import { ApiError, getSecuritySettings, patchSecuritySettings, type SecuritySettings } from "../api/client";
import { Dialog } from "../ui/Dialog";
import { ErrorNotice } from "./ErrorNotice";

/** MfaPolicyButton opens the sign-in policy (MFA requirement) from the Users page. */
export function MfaPolicyButton() {
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<SecuritySettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function show() {
    setOpen(true);
    setError(null);
    void getSecuritySettings()
      .then(setSettings)
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }

  async function setRequired(on: boolean) {
    setBusy(true);
    setError(null);
    try {
      setSettings(await patchSecuritySettings({ mfa_required: on }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button className="btn btn-secondary" type="button" onClick={show}>
        MFA policy
      </button>
      <Dialog open={open} title="MFA policy" onClose={() => setOpen(false)}>
        {error ? <ErrorNotice error={error} /> : null}
        {settings ? (
          <div className="stack">
            <label className="check-row">
              <input type="checkbox" checked={settings.mfa_required} disabled={busy} onChange={(e) => void setRequired(e.target.checked)} />
              <span>Require MFA for every person account</span>
            </label>
            <p className="field-hint">
              People without MFA are asked to set it up at their next sign-in. Each person manages their own MFA from their profile; reset it for
              someone from their row in the Users table.
            </p>
            <p className="meta">
              Sessions last {settings.session_ttl_hours} h. Sign-in locks for {settings.lockout_minutes} min after {settings.lockout_max_failures}{" "}
              failures in {settings.lockout_window_minutes} min. Lost SuperAdmin access: <code>{settings.recover_admin}</code> on the host.
            </p>
          </div>
        ) : !error ? (
          <p className="muted">Loading</p>
        ) : null}
      </Dialog>
    </>
  );
}
