import { useEffect } from "react";
import { Link } from "../components/Link";
import { EmptyState } from "../components/EmptyState";
import { SectionScopeProvider } from "../components/PageHeader";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { navigate } from "../router";
import { Segmented } from "../ui/Segmented";
import { CloudflarePage } from "./CloudflarePage";
import { ClusterPage } from "./ClusterPage";
import { DDoSPage } from "./DDoSPage";
import { EdgeNodesSection, EdgePage } from "./EdgePage";
import { NetworkPage } from "./NetworkPage";
import { RemoteNodeHelper } from "./NodePage";
import { ProxyPage } from "./ProxyPage";

export type NetworkTab = "network" | "cluster" | "cloudflare" | "caddy" | "nginx";

/** Sections of the Network tab that used to be tabs of their own; their old paths scroll to them. */
export type NetworkSection = "edge" | "ddos";

const TABS: { id: NetworkTab; label: string; feature?: string; capability?: string }[] = [
  { id: "network", label: "Network" },
  { id: "cluster", label: "Cluster", capability: "clustering" },
  { id: "cloudflare", label: "Cloudflare", feature: "cloudflare" },
  { id: "caddy", label: "Caddy", feature: "caddy" },
  { id: "nginx", label: "Nginx", feature: "nginx" },
];

export function networkTabPath(tab: NetworkTab): string {
  return tab === "network" ? "/network" : `/network/${tab}`;
}

/** networkRoute maps a path under /network (and the old Cluster path) to its tab and section. */
export function networkRoute(path: string): { tab: NetworkTab; section?: NetworkSection } | null {
  if (path === "/network") {
    return { tab: "network" };
  }
  if (path === "/network/edge" || path === "/network/ddos") {
    return { tab: "network", section: path.slice("/network/".length) as NetworkSection };
  }
  if (path === "/settings/cluster") {
    return { tab: "cluster" };
  }
  const tab = path.split("/")[2] as NetworkTab | undefined;
  return path.startsWith("/network/") && TABS.some((t) => t.id === tab) ? { tab: tab as NetworkTab } : null;
}

/** NetworkHubPage holds Network (with Edge forwarding and DDoS Protection), Cluster and the optional Cloudflare, Caddy and Nginx pages, switched at the top. */
export function NetworkHubPage({ tab, section }: { tab: NetworkTab; section?: NetworkSection }) {
  const { featureEnabled, features, capabilityOn } = useNavDisclosure();
  const tabs = TABS.filter((t) => (!t.feature || featureEnabled[t.feature]) && (!t.capability || capabilityOn(t.capability)));
  const current = TABS.find((t) => t.id === tab) ?? TABS[0];
  // A tab opened by its address still shows while its feature is hidden: hiding only takes it off the bar.
  const off = current.feature && !featureEnabled[current.feature] && features.length > 0;
  const ddosOn = featureEnabled.ddos || features.length === 0;

  useEffect(() => {
    if (!section) {
      return;
    }
    // Wait for the section to render before scrolling to it.
    const timer = setTimeout(() => document.getElementById(section)?.scrollIntoView({ block: "start" }), 300);
    return () => clearTimeout(timer);
  }, [section]);

  return (
    <section className="page page-wide">
      {tabs.length > 1 ? (
        <div className="hub-tabs">
          <Segmented<NetworkTab> ariaLabel="Network sections" value={tab} onChange={(t) => navigate(networkTabPath(t))} options={tabs.map((t) => ({ id: t.id, label: t.label }))} />
        </div>
      ) : null}
      {off ? (
        <EmptyState title={`${current.label} is turned off`}>
          Turn it on in <Link href="/settings/features">Add Features</Link> to manage it here. Turning it off only hid this page; nothing was changed.
        </EmptyState>
      ) : tab === "cluster" ? (
        <>
          <ClusterPage />
          <div className="cluster-extras">
            <EdgeNodesSection />
            <RemoteNodeHelper />
          </div>
        </>
      ) : tab === "cloudflare" ? (
        <CloudflarePage />
      ) : tab === "caddy" || tab === "nginx" ? (
        <ProxyPage key={tab} kind={tab} />
      ) : (
        <>
          <nav className="network-categories" aria-label="Network categories">
            <a href="#networks">Networks</a>
            <a href="#edge">Edge forwarding</a>
            {ddosOn ? <a href="#ddos">DDoS Protection</a> : null}
          </nav>
          <div id="networks">
            <NetworkPage />
          </div>
          <SectionScopeProvider>
            <EdgePage embedded />
            {ddosOn ? <DDoSPage embedded /> : null}
          </SectionScopeProvider>
        </>
      )}
    </section>
  );
}
