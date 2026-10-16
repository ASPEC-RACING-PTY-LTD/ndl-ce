import { Link } from "../components/Link";
import { EmptyState } from "../components/EmptyState";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { navigate } from "../router";
import { Segmented } from "../ui/Segmented";
import { CloudflarePage } from "./CloudflarePage";
import { NetworkPage } from "./NetworkPage";
import { ProxyPage } from "./ProxyPage";

export type NetworkTab = "network" | "cloudflare" | "caddy" | "nginx";

const TABS: { id: NetworkTab; label: string; feature?: string }[] = [
  { id: "network", label: "Network" },
  { id: "cloudflare", label: "Cloudflare", feature: "cloudflare" },
  { id: "caddy", label: "Caddy", feature: "caddy" },
  { id: "nginx", label: "Nginx", feature: "nginx" },
];

export function networkTabPath(tab: NetworkTab): string {
  return tab === "network" ? "/network" : `/network/${tab}`;
}

/** NetworkHubPage holds Network and the optional Cloudflare, Caddy and Nginx pages under one sidebar entry, switched at the top. */
export function NetworkHubPage({ tab }: { tab: NetworkTab }) {
  const { featureEnabled, features } = useNavDisclosure();
  const tabs = TABS.filter((t) => !t.feature || featureEnabled[t.feature]);
  const current = TABS.find((t) => t.id === tab) ?? TABS[0];
  const off = current.feature && !featureEnabled[current.feature] && features.length > 0;
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
      ) : tab === "cloudflare" ? (
        <CloudflarePage />
      ) : tab === "caddy" || tab === "nginx" ? (
        <ProxyPage key={tab} kind={tab} />
      ) : (
        <NetworkPage />
      )}
    </section>
  );
}
