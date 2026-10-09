import { useCallback, useEffect, useMemo, useState } from "react";
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
import { Segmented } from "../ui/Segmented";
import { SelectionCard } from "../ui/SelectionCard";
import { featureRuntimeLabel } from "../labels";
import { ErrorNotice } from "../components/ErrorNotice";

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
  const [query, setQuery] = useState("");
  const [show, setShow] = useState<"all" | "on" | "off">("all");

  const reload = useCallback(async () => {
    const next = await listFeatures();
    setList(next);
    await reloadFeatures();
  }, [reloadFeatures]);

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, [reload]);

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
      if (item.workload_count > 0 || ["k8s", "distributed_storage", "ai", "docker", "gameservers"].includes(item.id)) {
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

  const items = useMemo(() => list?.items ?? [], [list]);
  const catalogIds = new Set(CAPABILITIES.map((cap) => cap.featureId).filter((id): id is string => Boolean(id)));
  const coreItems = items.filter((item) => !catalogIds.has(item.id));
  const customIds = prefs.customOrder.length > 0 ? prefs.customOrder : prefs.customVisible;
  const customModules = NAV_MODULES.filter((mod) => allowedByRbac(mod, user?.roles, user?.grants));

  const rows = useMemo(
    () =>
      CAPABILITIES.map((cap) => {
        const pack = cap.featureId ? items.find((item) => item.id === cap.featureId) : undefined;
        const on = pack ? installState(pack).enabled : isCapabilityEnabled(cap.id, prefs, featureEnabled);
        return { cap, pack, on, title: pack?.title ?? cap.title };
      }),
    [items, prefs, featureEnabled],
  );
  const needle = query.trim().toLowerCase();
  const visible = rows.filter(
    (row) =>
      (show === "all" || (show === "on") === row.on) &&
      (!needle || `${row.title} ${row.cap.summary}`.toLowerCase().includes(needle)),
  );
  const enabledCount = rows.filter((row) => row.on).length;

  return (
    <section className="page page-wide features-page">
      <PageHeader
        id="features-heading"
        title="Add Features"
        kicker="Turn optional capabilities on or off. Turning one off never deletes workloads. Roles still decide who can change the system."
      />
      {error ? <ErrorNotice error={error} /> : null}

      <article className="stack">
        <div className="section-head">
          <h2>Features</h2>
          <span className="muted">
            {enabledCount} of {rows.length} on
          </span>
        </div>
        <div className="toolbar">
          <div className="toolbar-group">
            <label className="search-field">
              <input
                className="field-input"
                type="search"
                value={query}
                placeholder="Search features"
                aria-label="Search features"
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <Segmented
              ariaLabel="Show features"
              value={show}
              onChange={setShow}
              options={[
                { id: "all", label: "All" },
                { id: "on", label: "On" },
                { id: "off", label: "Off" },
              ]}
            />
          </div>
        </div>
        {visible.length === 0 ? <p className="muted">No feature matches.</p> : null}
        <ul className="feature-list">
          {visible.map(({ cap, pack, on, title }) => {
            const dest = capabilityHref(cap.id);
            const state = installState(pack);
            const status = pack
              ? pack.core
                ? "Included"
                : state.enabled
                  ? "Enabled"
                  : "Not installed"
              : on
                ? "Enabled"
                : "Not enabled";
            const working = busy === pack?.id;
            return (
              <li key={cap.id}>
                <article className={"feature-row" + (on ? " is-on" : "")}>
                  <div className="feature-main">
                    <h3>{title}</h3>
                    <p className="feature-summary">{cap.summary}</p>
                    {openId === cap.id && pack ? (
                      <p className="field-hint">
                        Package {featureRuntimeLabel(pack.package_status)}. Runtime {featureRuntimeLabel(pack.runtime_status)}.
                        {pack.kubelet_started ? " Kubelet running." : ""}
                        {pack.workload_count ? ` Workloads ${pack.workload_count}.` : ""}
                        {pack.reason ? ` ${pack.reason}` : ""}
                      </p>
                    ) : null}
                  </div>
                  <span className={"feature-state" + (on ? " is-on" : "")}>{working ? "Working" : status}</span>
                  <div className="feature-actions">
                    {mutate && pack && !pack.core && !pack.enabled ? (
                      <button className="btn btn-sm btn-primary" type="button" disabled={busy !== null} onClick={() => void onEnablePackage(pack)}>
                        {pack.software_only ? "Enable" : "Install"}
                      </button>
                    ) : null}
                    {mutate && pack && !pack.core && pack.enabled ? (
                      <button className="btn btn-sm btn-ghost" type="button" disabled={busy !== null} onClick={() => void onDisablePackage(pack)}>
                        Disable
                      </button>
                    ) : null}
                    {mutate && !cap.featureId ? (
                      <button
                        className={"btn btn-sm " + (on ? "btn-ghost" : "btn-primary")}
                        type="button"
                        onClick={() => setUiEnabled(cap.id, !on)}
                      >
                        {on ? "Disable" : "Enable"}
                      </button>
                    ) : null}
                    {dest && on ? (
                      <Link className="btn btn-sm btn-ghost" href={dest}>
                        Open
                      </Link>
                    ) : null}
                    {pack ? (
                      <button
                        className="btn btn-sm btn-ghost"
                        type="button"
                        aria-expanded={openId === cap.id}
                        onClick={() => setOpenId(openId === cap.id ? null : cap.id)}
                      >
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
          <ul className="feature-included">
            {coreItems.map((item) => (
              <li key={item.id}>{item.title}</li>
            ))}
          </ul>
        </article>
      ) : null}

      <article className="stack">
        <h2>Sidebar layout</h2>
        <p className="field-hint">
          Which entries the sidebar shows. This does not turn capabilities on or off.
          {list?.base_install ? ` Base install ${list.base_install}.` : ""}
        </p>
        <div className="feature-templates" role="radiogroup" aria-label="Navigation template">
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
    </section>
  );
}
