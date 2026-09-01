import { useEffect, useState } from "react";
import { acmeCert, ApiError, generateCert, getCerts, importCert } from "../api/client";
import type { CertificateStatus } from "../generated/openapi";
import { Field } from "../components/Field";
import { formatWhen } from "../format";
import { useSession } from "../session";

const CONFIRM_ENABLE = "enable-tls";

function canManage(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin"));
}

function modeLabel(mode: CertificateStatus["mode"]): string {
  switch (mode) {
    case "self_signed":
      return "Self-signed";
    case "imported":
      return "Imported PEM";
    case "acme":
      return "ACME";
    default:
      return "Not configured";
  }
}

function statusLabel(certs: CertificateStatus): string {
  if (certs.mode === "acme" || certs.acme_status !== "not_configured") {
    if (certs.acme_status === "pending") {
      return "ACME pending";
    }
    if (certs.acme_status === "failed") {
      return "Failed";
    }
    if (certs.acme_status === "not_configured" && !certs.enabled) {
      return "Not configured";
    }
    if (certs.acme_status === "issued" && certs.enabled) {
      return "Enabled";
    }
  }
  if (!certs.enabled && !certs.fingerprint) {
    return "Not configured";
  }
  if (!certs.enabled) {
    return "Not configured";
  }
  return "Enabled";
}

function parseSans(raw: string): string[] {
  return raw
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function CertificatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const manage = canManage(roles);

  const [certs, setCerts] = useState<CertificateStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState("");

  const [commonName, setCommonName] = useState("nodal");
  const [sansText, setSansText] = useState("");
  const [certPem, setCertPem] = useState("");
  const [keyPem, setKeyPem] = useState("");
  const [acmeDirectory, setAcmeDirectory] = useState("https://acme-v02.api.letsencrypt.org/directory");
  const [acmeEmail, setAcmeEmail] = useState("");
  const [acmeDomain, setAcmeDomain] = useState("");

  async function reload() {
    const next = await getCerts();
    setCerts(next);
    if (next.common_name) {
      setCommonName(next.common_name);
    }
    if (next.sans?.length) {
      setSansText(next.sans.join("\n"));
    }
    if (next.acme_directory) {
      setAcmeDirectory(next.acme_directory);
    }
    if (next.acme_email) {
      setAcmeEmail(next.acme_email);
    }
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

  const firstEnable = !(certs?.enabled);
  const confirmReady = !firstEnable || confirm === CONFIRM_ENABLE;

  async function runAction(action: () => Promise<CertificateStatus>) {
    setBusy(true);
    setError(null);
    try {
      const next = await action();
      setCerts(next);
      setConfirm("");
      setKeyPem("");
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Request failed";
      setError(message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="certificates-heading">
      <header className="page-header">
        <h1 id="certificates-heading">Certificates</h1>
        <p className="page-kicker">
          Management-plane TLS. Private keys stay on the appliance and are never shown in the browser.
        </p>
      </header>

      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}

      {!certs ? (
        <p className="banner" role="status">
          Loading certificate status.
        </p>
      ) : (
        <>
          <article className="panel">
            <h2>Status</h2>
            <dl className="definition-list">
              <div>
                <dt>State</dt>
                <dd>{statusLabel(certs)}</dd>
              </div>
              <div>
                <dt>Mode</dt>
                <dd>{modeLabel(certs.mode)}</dd>
              </div>
              <div>
                <dt>Common name</dt>
                <dd>{certs.common_name || "Not configured"}</dd>
              </div>
              <div>
                <dt>SANs</dt>
                <dd>{certs.sans?.length ? certs.sans.join(", ") : "None"}</dd>
              </div>
              <div>
                <dt>Fingerprint (SHA-256)</dt>
                <dd>
                  {certs.fingerprint ? <code>{certs.fingerprint}</code> : "Not configured"}
                </dd>
              </div>
              <div>
                <dt>Valid from</dt>
                <dd>{formatWhen(certs.not_before)}</dd>
              </div>
              <div>
                <dt>Valid until</dt>
                <dd>{formatWhen(certs.not_after)}</dd>
              </div>
              <div>
                <dt>ACME status</dt>
                <dd>
                  {certs.acme_status === "not_configured"
                    ? "Not configured"
                    : certs.acme_status === "pending"
                      ? "ACME pending"
                      : certs.acme_status === "issued"
                        ? "Issued"
                        : "Failed"}
                </dd>
              </div>
              <div>
                <dt>Next renewal</dt>
                <dd>{formatWhen(certs.next_renewal_at)}</dd>
              </div>
              <div>
                <dt>TLS listen</dt>
                <dd>{certs.tls_listen || "Not reported"}</dd>
              </div>
              {certs.enabled ? (
                <div>
                  <dt>HTTPS URL</dt>
                  <dd>
                    {certs.https_url ? (
                      <code>{certs.https_url}</code>
                    ) : (
                      "Use the HTTPS management URL after TLS is enabled."
                    )}
                  </dd>
                </div>
              ) : (
                <div>
                  <dt>HTTP listen</dt>
                  <dd>{certs.http_listen || "Not reported"}</dd>
                </div>
              )}
            </dl>
            {certs.enabled ? (
              <p className="banner banner-warn" role="status">
                TLS is enabled. Use the HTTPS management URL. Do not treat cleartext HTTP as the normal
                management address.
              </p>
            ) : null}
          </article>

          <article className="panel">
            <h2>Trust</h2>
            {certs.mode === "self_signed" && certs.fingerprint ? (
              <>
                <p>
                  This certificate is self-signed by the appliance. It is not issued by a public
                  certificate authority. Pin or verify the SHA-256 fingerprint below before trusting
                  the management URL in a browser or client.
                </p>
                <p>
                  Fingerprint (SHA-256): <code>{certs.fingerprint}</code>
                </p>
              </>
            ) : certs.mode === "imported" ? (
              <p>
                Trust follows the imported certificate chain. Confirm the fingerprint matches the
                certificate you installed:{" "}
                {certs.fingerprint ? <code>{certs.fingerprint}</code> : "not reported"}.
              </p>
            ) : certs.mode === "acme" && certs.acme_status === "issued" ? (
              <p>
                Trust follows the ACME-issued certificate for the configured directory. Public
                directories such as Let&apos;s Encrypt use HTTP-01 over the public internet. Private
                directories such as step-ca serve names on your own network.
              </p>
            ) : (
              <p>
                {certs.trust_note ||
                  "TLS is not configured yet. After you enable a certificate, trust instructions for the active mode appear here."}
              </p>
            )}
            {certs.trust_note && certs.mode === "self_signed" ? (
              <p className="field-hint">{certs.trust_note}</p>
            ) : null}
          </article>

          {manage ? (
            <>
              {firstEnable ? (
                <article className="panel">
                  <h2>Confirm first enable</h2>
                  <p>
                    Enabling TLS changes how you reach the management plane. Type{" "}
                    <code>{CONFIRM_ENABLE}</code> to confirm the first enable. The same confirmation
                    is sent as header <code>X-Nodal-Confirm</code>.
                  </p>
                  <Field
                    id="tls-confirm"
                    label={`Type ${CONFIRM_ENABLE}`}
                    value={confirm}
                    autoComplete="off"
                    onChange={(e) => setConfirm(e.target.value)}
                  />
                </article>
              ) : null}

              <article className="panel">
                <h2>Generate self-signed</h2>
                <p>
                  Creates an appliance-local certificate. Browsers will warn until you trust the
                  fingerprint. This is not a public CA certificate.
                </p>
                <Field
                  id="cert-cn"
                  label="Common name"
                  value={commonName}
                  onChange={(e) => setCommonName(e.target.value)}
                />
                <div className="field">
                  <label className="field-label" htmlFor="cert-sans">
                    Subject alternative names
                  </label>
                  <textarea
                    id="cert-sans"
                    className="field-input"
                    rows={3}
                    value={sansText}
                    onChange={(e) => setSansText(e.target.value)}
                    placeholder="One host or IP per line"
                  />
                </div>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={busy || !confirmReady || !commonName.trim()}
                  onClick={() =>
                    void runAction(() =>
                      generateCert(
                        { common_name: commonName.trim(), sans: parseSans(sansText) },
                        CONFIRM_ENABLE,
                      ),
                    )
                  }
                >
                  Generate self-signed
                </button>
              </article>

              <article className="panel">
                <h2>Import PEM</h2>
                <p>
                  Paste a certificate chain and matching private key. The key is submitted once to
                  the appliance and is never displayed or offered for download afterward.
                </p>
                <div className="field">
                  <label className="field-label" htmlFor="cert-pem">
                    Certificate PEM
                  </label>
                  <textarea
                    id="cert-pem"
                    className="field-input"
                    rows={6}
                    value={certPem}
                    onChange={(e) => setCertPem(e.target.value)}
                    spellCheck={false}
                  />
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="key-pem">
                    Private key PEM (write-only)
                  </label>
                  <textarea
                    id="key-pem"
                    className="field-input"
                    rows={6}
                    value={keyPem}
                    onChange={(e) => setKeyPem(e.target.value)}
                    spellCheck={false}
                    autoComplete="off"
                  />
                  <p className="field-hint">Cleared from this form after a successful import.</p>
                </div>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={busy || !confirmReady || !certPem.trim() || !keyPem.trim()}
                  onClick={() =>
                    void runAction(() =>
                      importCert({ cert_pem: certPem, key_pem: keyPem }, CONFIRM_ENABLE),
                    )
                  }
                >
                  Import PEM
                </button>
              </article>

              <article className="panel">
                <h2>ACME</h2>
                <p>
                  Configure an ACME directory for automatic issuance. Let&apos;s Encrypt is a public
                  HTTP-01 directory for publicly reachable names. step-ca and similar private
                  directories issue certificates for private names on your network. Status stays honest:
                  pending means not issued yet; failed means issuance did not succeed.
                </p>
                <Field
                  id="acme-directory"
                  label="Directory URL"
                  value={acmeDirectory}
                  onChange={(e) => setAcmeDirectory(e.target.value)}
                  hint="Example public: https://acme-v02.api.letsencrypt.org/directory. Private: your step-ca directory URL."
                />
                <Field
                  id="acme-email"
                  label="Email"
                  type="email"
                  value={acmeEmail}
                  onChange={(e) => setAcmeEmail(e.target.value)}
                />
                <Field
                  id="acme-domain"
                  label="Domain"
                  value={acmeDomain}
                  onChange={(e) => setAcmeDomain(e.target.value)}
                />
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={
                    busy ||
                    !confirmReady ||
                    !acmeDirectory.trim() ||
                    !acmeEmail.trim() ||
                    !acmeDomain.trim()
                  }
                  onClick={() =>
                    void runAction(() =>
                      acmeCert(
                        {
                          directory: acmeDirectory.trim(),
                          email: acmeEmail.trim(),
                          domain: acmeDomain.trim(),
                        },
                        CONFIRM_ENABLE,
                      ),
                    )
                  }
                >
                  Configure ACME
                </button>
              </article>
            </>
          ) : (
            <p className="banner" role="status">
              Certificate settings are read-only for your role. An administrator can generate,
              import, or configure ACME.
            </p>
          )}
        </>
      )}
    </section>
  );
}
