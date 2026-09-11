import { useEffect, useState } from "react";
import { ApiError, activateLicense, clearLicense, getLicense } from "../api/client";
import type { LicenseStatus } from "../generated/openapi";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { useSession } from "../session";
import { editionLabel } from "../labels";
import { SummaryCard } from "../ui/SummaryCard";
import { Dialog } from "../ui/Dialog";

import { hasGrant } from "../rbac";

export function LicensePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const manage = hasGrant(user, "settings.license.manage");
  const [status, setStatus] = useState<LicenseStatus | null>(null);
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [activateOpen, setActivateOpen] = useState(false);

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
      setActivateOpen(false);
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
    <section className="page page-wide" aria-labelledby="license-heading">
      <PageHeader
        id="license-heading"
        title="License"
        kicker="Community Edition does not require a key. CE 1.0 hardware gates are not proven on this host. This page does not download EE blobs. Entering a key contacts a licensing API only then. If that API is unreachable, grace applies and workloads are not stopped."
        actions={
          manage ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => setActivateOpen(true)}>
                Activate license
              </button>
              <button className="btn btn-ghost" type="button" disabled={busy} onClick={() => void onClear()}>
                Clear license
              </button>
            </div>
          ) : null
        }
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {status ? (
        <>
          <div className="summary-grid">
            <SummaryCard label="Edition" value={editionLabel(status.edition)} meta={`Edition ${status.edition}.`} />
            <SummaryCard label="Status" value={status.status} meta={status.reason} />
            <SummaryCard
              label="Key"
              value={status.has_key ? "Present" : "None"}
              meta={`Has key ${status.has_key ? "yes" : "no"}${status.key_suffix ? ` suffix ${status.key_suffix}` : ""}.`}
            />
            <SummaryCard label="EE blobs" value={status.ee_blobs ? "Yes" : "No"} meta={`EE blobs ${status.ee_blobs ? "yes" : "no"}.`} />
            <SummaryCard
              label="Workloads"
              value={status.workloads_stopped ? "Stopped" : "Running"}
              meta={`Workloads stopped ${status.workloads_stopped ? "yes" : "no"}.`}
            />
          </div>
          <p>
            See <Link href="/docs">Docs</Link> for CE 1.0.
          </p>
        </>
      ) : (
        <p>Collecting</p>
      )}
      <Dialog open={activateOpen} title="Activate license" onClose={() => setActivateOpen(false)}>
        <form
          className="form"
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
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Store key
            </button>
          </div>
        </form>
      </Dialog>
    </section>
  );
}
