import { useEffect, useState } from "react";
import { ApiError, confirmMfa, enrollMfa, getMfa } from "../api/client";

export function MFAPage() {
  const [enabled, setEnabled] = useState(false);
  const [kind, setKind] = useState("not_configured");
  const [secret, setSecret] = useState<string | null>(null);
  const [codes, setCodes] = useState<string[]>([]);
  const [otp, setOtp] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function reload() {
    const status = await getMfa();
    setEnabled(status.enabled);
    setKind(status.kind);
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  return (
    <section className="page" aria-labelledby="mfa-heading">
      <header className="page-header">
        <h1 id="mfa-heading">Authenticator</h1>
        <p className="page-kicker">TOTP is the supported MFA method. WebAuthn is not implemented yet.</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <p>Status: {enabled ? "Enabled" : "Not configured"} ({kind})</p>
        {enabled ? (
          <p className="field-hint">
            TOTP is enabled. recover-admin on the appliance is required if the last authenticator is lost.
          </p>
        ) : (
          <button
            className="btn"
            type="button"
            onClick={() => {
              void enrollMfa()
                .then((row) => {
                  setSecret(row.secret);
                  setCodes(row.recovery_codes);
                  setError(null);
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Enroll failed"));
            }}
          >
            Enroll TOTP
          </button>
        )}
        {secret ? (
          <>
            <p className="field-hint">Secret: {secret}</p>
            <p className="field-hint">Store recovery codes now. They are shown once.</p>
            <ul>
              {codes.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
            <label htmlFor="mfa-confirm">Confirm code</label>
            <input id="mfa-confirm" value={otp} onChange={(e) => setOtp(e.target.value)} />
            <button
              className="btn btn-primary"
              type="button"
              onClick={() => {
                void confirmMfa(otp)
                  .then(() => reload())
                  .catch((err) => setError(err instanceof ApiError ? err.message : "Confirm failed"));
              }}
            >
              Confirm
            </button>
          </>
        ) : null}
      </article>
    </section>
  );
}
