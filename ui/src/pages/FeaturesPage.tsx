import { confirmAction } from "../confirm";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError, disableFeature, enableFeature, listFeatures } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import type { Feature, FeatureList } from "../generated/openapi";
import { isCapabilityEnabled } from "../nav/disclosure";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { CAPABILITIES, moduleById } from "../nav/modules";
import { useSession } from "../session";
import { hasGrant } from "../rbac";
import { ResultsToolbar, useResultsView } from "../components/ResultsToolbar";
import { ResourceTable } from "../components/ResourceTable";
import { featureRuntimeLabel } from "../labels";
import { ErrorNotice } from "../components/ErrorNotice";
import { LoadingState } from "../components/EmptyState";
import { CodeholdDisableDialog } from "../components/CodeholdDisableDialog";

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

export type FeatureState = "enabled" | "disabled" | "available" | "core";

const STATE_LABEL: Record<FeatureState, string> = {
  enabled: "Enabled",
  disabled: "Disabled",
  available: "Not installed",
  core: "Included",
};

/** featureState tells a turned-off feature that is still installed from one that never was. */
export function featureState(item: Feature | undefined, uiOn: boolean): FeatureState {
  if (!item) {
    // UI-only capabilities ship with the appliance; off just means disabled.
    return uiOn ? "enabled" : "disabled";
  }
  if (item.core) {
    return "core";
  }
  if (item.enabled) {
    return "enabled";
  }
  return item.installed ? "disabled" : "available";
}

const CATEGORY: Record<string, string> = {
  oci: "Compute",
  docker: "Compute",
  gameservers: "Compute",
  codehold: "Compute",
  kubernetes: "Compute",
  gpu: "Compute",
  cloudflare: "Network",
  caddy: "Network",
  nginx: "Network",
  "advanced-network": "Network",
  "advanced-storage": "Storage",
  automation: "Operations",
  clustering: "Operations",
  ai: "Intelligence",
  integrations: "Catalog",
};

const STATUS_FILTERS = [
  { value: "all", label: "All statuses" },
  { value: "enabled", label: "Enabled" },
  { value: "disabled", label: "Disabled" },
  { value: "available", label: "Not installed" },
];

const SORTS = [
  { value: "name", label: "Name" },
  { value: "category", label: "Category" },
  { value: "status", label: "Status" },
];

const STATE_ORDER: Record<FeatureState, number> = { enabled: 0, core: 1, disabled: 2, available: 3 };

export function FeaturesPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "feature.manage");
  const { prefs, featureEnabled, setUiEnabled, reloadFeatures } = useNavDisclosure();
  const [list, setList] = useState<FeatureList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("all");
  const [category, setCategory] = useState("all");
  const [sort, setSort] = useState("name");
  const [view, setView] = useResultsView("ndl-features-view", "grid");
  const [codeholdOff, setCodeholdOff] = useState(false);
  const focus = useMemo(() => new URLSearchParams(window.location.search).get("focus"), []);

  const reload = useCallback(async () => {
    const next = await listFeatures();
    setList(next);
    await reloadFeatures();
  }, [reloadFeatures]);

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, [reload]);

  useEffect(() => {
    if (!focus || !list) {
      return;
    }
    document.getElementById(`feature-${focus}`)?.scrollIntoView({ block: "center" });
  }, [focus, list]);

  async function onEnablePackage(item: Feature) {
    setBusy(item.id);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.id === "k8s") {
        const msg = item.tiny_node
          ? "This node is at or below 8 GiB RAM. Enable Kubernetes anyway? Kubelet is not started."
          : "Enable Kubernetes? Kubelet is not started.";
        if (!confirmAction(msg)) {
          return;
        }
        confirm = "enable-k8s";
      }
      await enableFeature(item.id, confirm);
      await reload();
      setNotice(`${item.title} is on.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Enable failed");
    } finally {
      setBusy(null);
    }
  }

  async function onDisablePackage(item: Feature) {
    if (item.id === "codehold") {
      // Runner containers need a decision first; the dialog asks.
      setCodeholdOff(true);
      return;
    }
    setBusy(item.id);
    setError(null);
    try {
      let confirm: string | undefined;
      if (item.workload_count > 0 || ["k8s", "distributed_storage", "ai", "docker", "gameservers"].includes(item.id)) {
        if (
          !confirmAction(
            item.software_only
              ? "Disable does not delete anything. Turn the feature off?"
              : "Disable removes the package but does not delete workloads. Turn the module off and leave existing workloads running?",
          )
        ) {
          return;
        }
        confirm = "disable-feature";
      }
      await disableFeature(item.id, confirm);
      await reload();
      setNotice(`${item.title} is off.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Disable failed");
    } finally {
      setBusy(null);
    }
  }

  const items = useMemo(() => list?.items ?? [], [list]);
  const catalogIds = new Set(CAPABILITIES.map((cap) => cap.featureId).filter((id): id is string => Boolean(id)));
  const coreItems = items.filter((item) => !catalogIds.has(item.id));

  const rows = useMemo(
    () =>
      CAPABILITIES.map((cap) => {
        const pack = cap.featureId ? items.find((item) => item.id === cap.featureId) : undefined;
        const uiOn = isCapabilityEnabled(cap.id, prefs, featureEnabled);
        const state = cap.featureId && !pack ? "available" : featureState(pack, uiOn);
        return {
          cap,
          pack,
          state,
          on: state === "enabled" || state === "core",
          title: pack?.title ?? cap.title,
          category: CATEGORY[cap.id] ?? "Other",
        };
      }),
    [items, prefs, featureEnabled],
  );
  const categories = useMemo(() => Array.from(new Set(rows.map((r) => r.category))).sort(), [rows]);
  const needle = query.trim().toLowerCase();
  const visible = rows
    .filter(
      (row) =>
        (status === "all" || row.state === status || (status === "enabled" && row.state === "core")) &&
        (category === "all" || row.category === category) &&
        (!needle || `${row.title} ${row.cap.summary} ${row.category}`.toLowerCase().includes(needle)),
    )
    .sort((a, b) => {
      if (sort === "category") {
        return a.category.localeCompare(b.category) || a.title.localeCompare(b.title);
      }
      if (sort === "status") {
        return STATE_ORDER[a.state] - STATE_ORDER[b.state] || a.title.localeCompare(b.title);
      }
      return a.title.localeCompare(b.title);
    });
  const installed = visible.filter((r) => r.state !== "available");
  const available = visible.filter((r) => r.state === "available");
  const enabledCount = rows.filter((row) => row.on).length;

  function actions(row: (typeof rows)[number]) {
    const { cap, pack, state } = row;
    const dest = capabilityHref(cap.id);
    const working = busy !== null;
    const out: ReactNode[] = [];
    if (mutate && state === "available" && pack) {
      out.push(
        <button key="install" className="btn btn-sm btn-primary" type="button" disabled={working} onClick={() => void onEnablePackage(pack)}>
          {pack.software_only ? "Enable" : "Install"}
        </button>,
      );
    }
    if (mutate && state === "disabled") {
      out.push(
        <button
          key="enable"
          className="btn btn-sm btn-primary"
          type="button"
          disabled={working}
          onClick={() => (pack ? void onEnablePackage(pack) : setUiEnabled(cap.id, true))}
        >
          Enable
        </button>,
      );
    }
    if ((state === "enabled" || state === "core") && dest) {
      out.push(
        <Link key="open" className="btn btn-sm" href={dest}>
          Open
        </Link>,
      );
    }
    if (mutate && state === "enabled") {
      out.push(
        <button
          key="disable"
          className="btn btn-sm btn-ghost"
          type="button"
          disabled={working}
          onClick={() => (pack ? void onDisablePackage(pack) : setUiEnabled(cap.id, false))}
        >
          Disable
        </button>,
      );
    }
    if (pack) {
      out.push(
        <button
          key="details"
          className="btn btn-sm btn-ghost"
          type="button"
          aria-expanded={openId === cap.id}
          onClick={() => setOpenId(openId === cap.id ? null : cap.id)}
        >
          Details
        </button>,
      );
    }
    return out;
  }

  function details(row: (typeof rows)[number]) {
    const { pack, cap } = row;
    if (openId !== cap.id || !pack) {
      return null;
    }
    return (
      <p className="field-hint">
        Package {featureRuntimeLabel(pack.package_status)}. Runtime {featureRuntimeLabel(pack.runtime_status)}.
        {pack.kubelet_started ? " Kubelet running." : ""}
        {pack.workload_count ? ` Workloads ${pack.workload_count}.` : ""}
        {pack.reason ? ` ${pack.reason}` : ""}
      </p>
    );
  }

  function section(title: string, rowsIn: typeof visible, emptyText: string) {
    return (
      <article className="stack">
        <div className="section-head">
          <h2>{title}</h2>
          <span className="muted">{rowsIn.length}</span>
        </div>
        {rowsIn.length === 0 ? (
          <p className="muted">{emptyText}</p>
        ) : view === "table" ? (
          <article className="panel">
            <ResourceTable
              className="docker-table-flat feature-table"
              headers={["Feature", "Category", "Status", ""]}
              rows={rowsIn.map((row) => [
                <div key="t" id={`feature-${row.cap.id}`} className={focus === row.cap.id ? "is-focused" : undefined}>
                  <strong>{row.title}</strong>
                  <p className="feature-summary">{row.cap.summary}</p>
                  {details(row)}
                </div>,
                row.category,
                <span key="s" className={"feature-state is-" + row.state}>
                  {busy === row.pack?.id ? "Working" : STATE_LABEL[row.state]}
                </span>,
                <div key="a" className="feature-actions">
                  {actions(row)}
                </div>,
              ])}
            />
          </article>
        ) : (
          <ul className="feature-list">
            {rowsIn.map((row) => (
              <li key={row.cap.id} id={`feature-${row.cap.id}`}>
                <article className={"feature-row is-" + row.state + (focus === row.cap.id ? " is-focused" : "")}>
                  <div className="feature-main">
                    <div className="feature-title">
                      <h3>{row.title}</h3>
                      <span className="tag-inline">{row.category}</span>
                    </div>
                    <p className="feature-summary">{row.cap.summary}</p>
                    {details(row)}
                  </div>
                  <span className={"feature-state is-" + row.state}>
                    {busy === row.pack?.id ? "Working" : STATE_LABEL[row.state]}
                  </span>
                  <div className="feature-actions">{actions(row)}</div>
                </article>
              </li>
            ))}
          </ul>
        )}
      </article>
    );
  }

  return (
    <section className="page page-wide features-page">
      <CodeholdDisableDialog
        open={codeholdOff}
        onClose={() => setCodeholdOff(false)}
        onDone={(msg) => {
          setCodeholdOff(false);
          setNotice(msg);
          void reload();
        }}
      />
      <PageHeader
        id="features-heading"
        title="Add Features"
        kicker="Turn optional capabilities on or off. Turning one off never deletes workloads. Roles still decide who can change the system."
      />
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}

      <div className="section-head">
        <span className="muted">
          {enabledCount} of {rows.length} on
        </span>
      </div>
      <ResultsToolbar
        label="Features"
        query={query}
        onQuery={setQuery}
        placeholder="Search features"
        filter={status}
        filters={STATUS_FILTERS}
        onFilter={setStatus}
        filterLabel="Status"
        sort={sort}
        sorts={SORTS}
        onSort={setSort}
        view={view}
        onView={setView}
      >
        <select className="field-input" aria-label="Category" value={category} onChange={(e) => setCategory(e.target.value)}>
          <option value="all">All categories</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </ResultsToolbar>

      {!list && !error ? <LoadingState label="Loading features" /> : null}
      {list && status !== "available" ? section("Installed", installed, "No installed feature matches.") : null}
      {list && (status === "all" || status === "available") ? section("Available", available, "Every feature is installed.") : null}

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

      <p className="field-hint">
        Sidebar layout moved to <Link href="/me?tab=appearance">Account → Appearance</Link>.
      </p>
    </section>
  );
}
