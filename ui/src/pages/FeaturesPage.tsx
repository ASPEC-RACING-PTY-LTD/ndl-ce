import { useEffect, useState } from "react";
import { ApiError, disableFeature, enableFeature, listFeatures } from "../api/client";
import type { Feature, FeatureList } from "../generated/openapi";
import { useSession } from "../session";

function canManage(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function statusLabel(item: Feature): string {
  if (item.core) {
    return "Installed";
  }
  if (item.enabled) {
    return "Enabled";
  }
  return "Not installed";
}

export function FeaturesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canManage(roles);
  const [list, setList] = useState<FeatureList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  async function reload() {
    setList(await listFeatures());
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

  async function onEnable(item: Feature) {
    setBusy(item.id);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.id === "k8s") {
        const msg = item.tiny_node
          ? "This node is at or below 8 GiB RAM. Enable Kubernetes anyway? Kubelet is not started. Runtime is a later phase."
          : "Enable Kubernetes? Kubelet is not started. Runtime is a later phase.";
        if (!window.confirm(msg)) {
          return;
        }
        confirm = "enable-k8s";
      }
      await enableFeature(item.id, confirm);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Enable failed");
    } finally {
      setBusy(null);
    }
  }

  async function onDisable(item: Feature) {
    setBusy(item.id);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.workload_count > 0) {
        if (
          !window.confirm(
            "Disable does not delete workloads. Turn the module off and leave existing workloads running?",
          )
        ) {
          return;
        }
        confirm = "disable-feature";
      }
      await disableFeature(item.id, confirm);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Disable failed");
    } finally {
      setBusy(null);
    }
  }

  const items = list?.items ?? [];

  return (
    <section className="page">
      <header className="page-header">
        <h1>Features</h1>
        <p className="lede">
          A home server stays small. GPU, Kubernetes, distributed storage, and AI are opt-in packages. Enabling
          Kubernetes does not start kubelet.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {list ? (
        <p>
          Base install {list.base_install}. GPU services optional {list.gpu_optional ? "yes" : "no"}.
        </p>
      ) : (
        <p>Collecting</p>
      )}
      <ul className="plain-list">
        {items.map((item) => (
          <li key={item.id}>
            <article className="panel">
              <h2>{item.title}</h2>
              <p>
                {statusLabel(item)}. Package {item.package_status}. Runtime {item.runtime_status}. Kubelet started{" "}
                {item.kubelet_started ? "yes" : "no"}.
                {item.workload_count ? ` Workloads ${item.workload_count}.` : ""}
              </p>
              {item.reason ? <p>{item.reason}</p> : null}
              {mutate && !item.core && !item.enabled ? (
                <button
                  className="btn btn-primary"
                  type="button"
                  disabled={busy !== null}
                  onClick={() => void onEnable(item)}
                >
                  Install
                </button>
              ) : null}
              {mutate && !item.core && item.enabled ? (
                <button
                  className="btn btn-ghost"
                  type="button"
                  disabled={busy !== null}
                  onClick={() => void onDisable(item)}
                >
                  Disable
                </button>
              ) : null}
            </article>
          </li>
        ))}
      </ul>
    </section>
  );
}
