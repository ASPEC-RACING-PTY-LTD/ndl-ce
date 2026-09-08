import { useEffect, useState } from "react";
import { ApiError, disableFeature, enableFeature, listFeatures } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import type { Feature, FeatureList } from "../generated/openapi";
import { allowedByRbac, isCapabilityEnabled } from "../nav/disclosure";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { CAPABILITIES, NAV_MODULES, TEMPLATES, moduleById } from "../nav/modules";
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

function capabilityHref(id: string): string | undefined {
  const cap = CAPABILITIES.find((item) => item.id === id);
  if (!cap) {
    return undefined;
  }
  if (cap.href) {
    return cap.href;
  }
  const first = cap.modules[0];
  return first ? moduleById(first)?.href : undefined;
}

export function FeaturesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canManage(roles);
  const { prefs, featureEnabled, setTemplate, setUiEnabled, setCustomModule, reorderCustom, reloadFeatures } =
    useNavDisclosure();
  const [list, setList] = useState<FeatureList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  async function reload() {
    const next = await listFeatures();
    setList(next);
    await reloadFeatures();
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, [reloadFeatures]);

  async function onEnablePackage(item: Feature) {
    setBusy(item.id);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.id === "k8s") {
        const msg = item.tiny_node
          ? "This node is at or below 8 GiB RAM. Enable Kubernetes anyway? Kubelet is not started."
          : "Enable Kubernetes? Kubelet is not started.";
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

  async function onDisablePackage(item: Feature) {
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
  const catalogIds = new Set(CAPABILITIES.map((cap) => cap.featureId).filter((id): id is string => Boolean(id)));
  const coreItems = items.filter((item) => !catalogIds.has(item.id));
  const customIds = prefs.customOrder.length > 0 ? prefs.customOrder : prefs.customVisible;
  const customModules = NAV_MODULES.filter((mod) => allowedByRbac(mod, roles));

  return (
    <section className="page page-wide">
      <PageHeader
        id="features-heading"
        title="Add Features"
        kicker="Enable optional capabilities here. Sidebar visibility is a separate template. Roles still decide who can change the system."
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}

      <article className="panel">
        <h2>Navigation template</h2>
        <p className="lede">
          Simple is the default for homelab and SMB virtualization. Switching templates does not disable packages or
          lose a Custom layout.
        </p>
        <fieldset className="stack">
          <legend className="field-label">Template</legend>
          {TEMPLATES.map((item) => (
            <div key={item.id}>
              <label className="field-label">
                <input
                  type="radio"
                  name="nav-template"
                  value={item.id}
                  checked={prefs.template === item.id}
                  onChange={() => setTemplate(item.id)}
                />{" "}
                {item.label}
              </label>
              <p className="field-hint">{item.summary}</p>
            </div>
          ))}
        </fieldset>
      </article>

      {prefs.template === "custom" ? (
        <article className="panel">
          <h2>Custom modules</h2>
          <p className="lede">Hide or show sidebar entries. This does not enable or disable the capability itself.</p>
          <ul className="plain-list">
            {customModules.map((mod) => {
              const checked = prefs.customVisible.includes(mod.id);
              const idx = customIds.indexOf(mod.id);
              return (
                <li key={mod.id} className="inline-actions">
                  <label>
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => setCustomModule(mod.id, e.target.checked)}
                    />{" "}
                    {mod.label}
                  </label>
                  {checked && idx >= 0 ? (
                    <>
                      <button type="button" className="btn btn-secondary" onClick={() => reorderCustom(mod.id, -1)}>
                        Up
                      </button>
                      <button type="button" className="btn btn-secondary" onClick={() => reorderCustom(mod.id, 1)}>
                        Down
                      </button>
                    </>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </article>
      ) : null}

      <article className="panel">
        <h2>Integrations and optional capabilities</h2>
        <p className="lede">
          Enablement is cluster or package state. It is not the same as pinning a link in the sidebar.
        </p>
        <ul className="plain-list feature-catalog">
          {CAPABILITIES.map((cap) => {
            const pack = cap.featureId ? items.find((item) => item.id === cap.featureId) : undefined;
            const on = isCapabilityEnabled(cap.id, prefs, featureEnabled);
            const dest = capabilityHref(cap.id);
            const title = pack?.title ?? cap.title;
            return (
              <li key={cap.id}>
                <article className="panel">
                  <h2>{title}</h2>
                  <p>{cap.summary}</p>
                  <p>{on ? "Enabled" : "Not enabled"}.</p>
                  {pack ? (
                    <p>
                      {statusLabel(pack)}. Package {pack.package_status}. Runtime {pack.runtime_status}. Kubelet
                      started {pack.kubelet_started ? "yes" : "no"}.
                      {pack.workload_count ? ` Workloads ${pack.workload_count}.` : ""}
                    </p>
                  ) : null}
                  {pack?.reason ? <p>{pack.reason}</p> : null}
                  <div className="inline-actions">
                    {mutate && pack && !pack.core && !pack.enabled ? (
                      <button
                        className="btn btn-primary"
                        type="button"
                        disabled={busy !== null}
                        onClick={() => void onEnablePackage(pack)}
                      >
                        Install
                      </button>
                    ) : null}
                    {mutate && pack && !pack.core && pack.enabled ? (
                      <button
                        className="btn btn-ghost"
                        type="button"
                        disabled={busy !== null}
                        onClick={() => void onDisablePackage(pack)}
                      >
                        Disable
                      </button>
                    ) : null}
                    {mutate && !cap.featureId ? (
                      <button className="btn btn-primary" type="button" onClick={() => setUiEnabled(cap.id, !on)}>
                        {on ? "Disable" : "Enable"}
                      </button>
                    ) : null}
                    {dest ? (
                      <Link className="btn btn-ghost" href={dest}>
                        Configure
                      </Link>
                    ) : null}
                  </div>
                </article>
              </li>
            );
          })}
        </ul>
      </article>

      {list ? (
        <p>
          Base install {list.base_install}. GPU services optional {list.gpu_optional ? "yes" : "no"}.
        </p>
      ) : (
        <p>Collecting</p>
      )}

      {coreItems.length > 0 ? (
        <article className="panel">
          <h2>Included with the appliance</h2>
          <ul className="plain-list">
            {coreItems.map((item) => (
              <li key={item.id}>
                <p>
                  <strong>{item.title}</strong>
                </p>
                <p>
                  {statusLabel(item)}. Package {item.package_status}. Runtime {item.runtime_status}.
                </p>
              </li>
            ))}
          </ul>
        </article>
      ) : null}
    </section>
  );
}
