import { useEffect, useState } from "react";
import { ApiError, activateLicense, clearLicense, getLicense } from "../api/client";
import type { LicenseStatus } from "../generated/openapi";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { useSession } from "../session";

function canManage(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin"));
}

export function LicensePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const manage = canManage(roles);
  const [status, setStatus] = useState<LicenseStatus | null>(null);
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void getLicense()
      .then((next) => {
        if (!cancelled) {
          setStatus(next);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function onActivate() {
    if (!window.confirm("Store this key and contact the licensing API? Workloads will not stop if the API is down.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setStatus(await activateLicense(key));
      setKey("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Activate failed");
    } finally {
      setBusy(false);
    }
  }

  async function onClear() {
    if (!window.confirm("Clear the stored key? This returns the surface to Community Edition. Workloads stay running.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setStatus(await clearLicense());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Clear failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>License</h1>
        <p className="lede">
          Community Edition does not require a key. CE 1.0 hardware gates are not proven on this host. This page does
          not download EE blobs. Entering a key contacts a licensing API only then. If that API is unreachable, grace
          applies and workloads are not stopped.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {status ? (
        <article className="panel">
          <p>Edition {status.edition}.</p>
          <p>Status {status.status}.</p>
          <p>{status.reason}</p>
          <p>Has key {status.has_key ? "yes" : "no"}{status.key_suffix ? ` suffix ${status.key_suffix}` : ""}.</p>
          <p>EE blobs {status.ee_blobs ? "yes" : "no"}.</p>
          <p>Workloads stopped {status.workloads_stopped ? "yes" : "no"}.</p>
          <p>
            See <Link href="/docs">Docs</Link> for CE 1.0.
          </p>
        </article>
      ) : (
        <p>Collecting</p>
      )}
      {manage ? (
        <article className="panel">
          <form
            className="stack"
            onSubmit={(ev) => {
              ev.preventDefault();
              void onActivate();
            }}
          >
            <Field
              id="license-key"
              label="License key"
              type="password"
              autoComplete="off"
              value={key}
              onChange={(e) => setKey(e.target.value)}
            />
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Activate license
            </button>
          </form>
          <button className="btn" type="button" disabled={busy} onClick={() => void onClear()}>
            Clear license
          </button>
        </article>
      ) : null}
    </section>
  );
}
