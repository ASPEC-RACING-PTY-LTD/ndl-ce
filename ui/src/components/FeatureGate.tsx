import { useState, type ReactNode } from "react";
import { ApiError, enableFeature } from "../api/client";
import { CAPABILITIES, capabilityForPath } from "../nav/modules";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { navigate } from "../router";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { confirmAction } from "../confirm";
import { EmptyState, LoadingState } from "./EmptyState";
import { ErrorNotice } from "./ErrorNotice";
import { PageHeader } from "./PageHeader";

/**
 * FeatureGate renders a page only when the optional feature it belongs to is
 * on. A disabled feature's address shows FeatureDisabledPage instead, so no
 * link or typed URL reaches a page that is switched off.
 */
export function FeatureGate({ path, children }: { path: string; children: ReactNode }) {
  const { featuresLoaded, capabilityOn } = useNavDisclosure();
  const capability = capabilityForPath(path);
  if (!capability) {
    return <>{children}</>;
  }
  if (!featuresLoaded) {
    return (
      <section className="page">
        <LoadingState />
      </section>
    );
  }
  if (capabilityOn(capability)) {
    return <>{children}</>;
  }
  return <FeatureDisabledPage capability={capability} />;
}

export function FeatureDisabledPage({ capability }: { capability: string }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canEnable = hasGrant(user, "feature.manage");
  const { reloadFeatures, setUiEnabled } = useNavDisclosure();
  const cap = CAPABILITIES.find((c) => c.id === capability);
  const title = cap?.title ?? capability;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onEnable() {
    if (!cap) {
      return;
    }
    setError(null);
    if (!cap.featureId) {
      setUiEnabled(cap.id, true);
      return;
    }
    let confirm: string | undefined;
    if (cap.featureId === "k8s") {
      if (!confirmAction("Enable Kubernetes? Kubelet is not started.")) {
        return;
      }
      confirm = "enable-k8s";
    }
    setBusy(true);
    try {
      await enableFeature(cap.featureId, confirm);
      await reloadFeatures();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Enable failed");
    } finally {
      setBusy(false);
    }
  }

  function goBack() {
    if (window.history.length > 1) {
      window.history.back();
    } else {
      navigate("/");
    }
  }

  return (
    <section className="page" aria-labelledby="feature-off-heading">
      <PageHeader id="feature-off-heading" title={title} kicker="This page belongs to an optional feature." />
      {error ? <ErrorNotice error={error} /> : null}
      <EmptyState
        icon="lock"
        title="This feature is disabled"
        action={
          <div className="btn-row is-flush">
            <button className="btn" type="button" onClick={goBack}>
              Go back
            </button>
            {canEnable ? (
              <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onEnable()}>
                {busy ? "Enabling" : "Enable feature"}
              </button>
            ) : null}
            <button className="btn btn-ghost" type="button" onClick={() => navigate(`/settings/features?focus=${capability}`)}>
              Visit Features page
            </button>
          </div>
        }
      >
        {cap?.summary ?? ""}
        {canEnable ? "" : " Ask an administrator to enable it."}
      </EmptyState>
    </section>
  );
}
