import { useEffect, useState } from "react";
import { ApiError, getSecuritySettings, patchSecuritySettings, type SecuritySettings } from "../api/client";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Switch } from "../ui/Switch";

export function SecurityPage() {
  const [settings, setSettings] = useState<SecuritySettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void getSecuritySettings()
      .then((body) => {
        setSettings(body);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  async function onToggle(next: boolean) {
    setBusy(true);
    setError(null);
    try {
      const body = await patchSecuritySettings({ mfa_required: next });
      setSettings(body);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page" aria-labelledby="security-heading">
      <PageHeader
        id="security-heading"
        title="Security"
        kicker="Appliance authentication policy. Personal authenticators stay on Account. Recovery stays on the host."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {!settings ? <LoadingState label="Loading security policy" /> : null}
      {settings ? (
        <div className="stack">
          <section className="panel stack">
            <h2>MFA policy</h2>
            <p className="lede">
              When required, accounts without an enrolled authenticator are marked as needing enrollment. Existing
              passwords, roles, and secrets are not changed by this switch.
            </p>
            <Switch
              id="mfa-required"
              label="Require MFA enrollment for person accounts"
              checked={Boolean(settings.mfa_required)}
              disabled={busy}
              onChange={(event) => void onToggle(event.target.checked)}
            />
          </section>
          <section className="panel">
            <h2>Session and lockout</h2>
            <dl className="definition-list">
              <div>
                <dt>Session lifetime</dt>
                <dd>{settings.session_ttl_hours} hours</dd>
              </div>
              <div>
                <dt>Failed attempts</dt>
                <dd>{settings.lockout_max_failures} in {settings.lockout_window_minutes} minutes</dd>
              </div>
              <div>
                <dt>Lock duration</dt>
                <dd>{settings.lockout_minutes} minutes</dd>
              </div>
            </dl>
            <p className="field-hint">These values are the live control-plane policy, not placeholders.</p>
          </section>
          <section className="panel stack">
            <h2>Recovery</h2>
            <p className="lede">
              Password recovery for a lost owner is <code>nodalctl recover-admin</code> on this host as root. It is
              not available from this page and does not display host keys or secrets.
            </p>
            <p className="field-hint">recover-admin: {settings.recover_admin}</p>
          </section>
        </div>
      ) : null}
    </section>
  );
}
