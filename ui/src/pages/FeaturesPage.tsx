import { useEffect, useState } from "react";
import { ApiError, disableFeature, enableFeature, listFeatures } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import type { Feature, FeatureList } from "../generated/openapi";
import { allowedByRbac, isCapabilityEnabled } from "../nav/disclosure";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { CAPABILITIES, NAV_MODULES, TEMPLATES, moduleById } from "../nav/modules";
import { useSession } from "../session";
import { hasGrant } from "../rbac";
import { Checkbox } from "../ui/Checkbox";
import { SelectionCard } from "../ui/SelectionCard";
import { featureRuntimeLabel } from "../labels";

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

function installState(item?: Feature): { label: string; installed: boolean; enabled: boolean } {
  if (!item) {
    return { label: "Not installed", installed: false, enabled: false };
  }
  if (item.core) {
    return { label: "Installed", installed: true, enabled: true };
  }
  if (item.enabled) {
    return { label: "Enabled", installed: true, enabled: true };
  }
  return { label: "Not installed", installed: false, enabled: false };
}

export function FeaturesPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "feature.manage");
  const { prefs, featureEnabled, setTemplate, setUiEnabled, setCustomModule, reorderCustom, reloadFeatures } =
    useNavDisclosure();
  const [list, setList] = useState<FeatureList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);

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
      if (item.workload_count > 0 || ["k8s", "distributed_storage", "ai", "docker"].includes(item.id)) {
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
  const customModules = NAV_MODULES.filter((mod) => allowedByRbac(mod, user?.roles, user?.grants));

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

      <article className="stack">
        <h2>Navigation template</h2>
        {list?.base_install ? <p className="field-hint">Base install {list.base_install}.</p> : null}
        <div className="content-grid" role="radiogroup" aria-label="Navigation template">
          {TEMPLATES.map((item) => (
            <SelectionCard
              key={item.id}
              role="radio"
              title={item.label}
              description={item.summary}
              selected={prefs.template === item.id}
              onSelect={() => setTemplate(item.id)}
            />
          ))}
        </div>
      </article>

      {prefs.template === "custom" ? (
        <article className="stack">
          <h2>Custom modules</h2>
          <p className="lede">Hide or show sidebar entries. This does not enable or disable the capability itself.</p>
          <ul className="plain-list">
            {customModules.map((mod) => {
              const checked = prefs.customVisible.includes(mod.id);
              const idx = customIds.indexOf(mod.id);
              return (
                <li key={mod.id} className="inline-actions">
                  <Checkbox
                    id={`mod-${mod.id}`}
                    label={mod.label}
                    checked={checked}
                    onChange={(e) => setCustomModule(mod.id, e.target.checked)}
                  />
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

      <article className="stack">
        <h2>Optional capabilities</h2>
        <ul className="feature-card-grid">
          {CAPABILITIES.map((cap) => {
            const pack = cap.featureId ? items.find((item) => item.id === cap.featureId) : undefined;
            const on = isCapabilityEnabled(cap.id, prefs, featureEnabled);
            const dest = capabilityHref(cap.id);
            const title = pack?.title ?? cap.title;
            const state = installState(pack);
            return (
              <li key={cap.id}>
                <article className="compact-card">
                  <h3>{title}</h3>
                  <p className="lede">{cap.summary}</p>
                  <p>
                    {pack
                      ? `${state.installed ? "Installed" : "Not installed"}${
                          pack.core ? "" : ` · ${state.enabled ? "Enabled" : "Disabled"}`
                        }`
                      : on
                        ? "Enabled."
                        : "Not enabled."}
                  </p>
                  {openId === cap.id && pack ? (
                    <p className="field-hint">
                      Package {featureRuntimeLabel(pack.package_status)}. Runtime {featureRuntimeLabel(pack.runtime_status)}.
                      {pack.kubelet_started ? " Kubelet running." : ""}
                      {pack.workload_count ? ` Workloads ${pack.workload_count}.` : ""}
                      {pack.reason ? ` ${pack.reason}` : ""}
                    </p>
                  ) : null}
                  <div className="btn-row">
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
                    {pack ? (
                      <button className="btn btn-ghost" type="button" onClick={() => setOpenId(openId === cap.id ? null : cap.id)}>
                        Details
                      </button>
                    ) : null}
                  </div>
                </article>
              </li>
            );
          })}
        </ul>
      </article>

      {coreItems.length > 0 ? (
        <article className="stack">
          <h2>Included with the appliance</h2>
          <div className="content-grid">
            {coreItems.map((item) => (
              <article key={item.id} className="compact-card">
                <h3>{item.title}</h3>
                <p>Installed</p>
              </article>
            ))}
          </div>
        </article>
      ) : null}
    </section>
  );
}
