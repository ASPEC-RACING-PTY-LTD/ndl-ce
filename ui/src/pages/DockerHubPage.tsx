import { PageNav, type PageNavItem } from "../components/PageNav";
import { DeployPage } from "./DeployPage";
import { DockerPage } from "./DockerPage";

/** "uptime" is the old Uptime address: it shows the Docker page at its Sites section. */
export type DockerHubTab = "docker" | "deploy" | "uptime";

const ITEMS: PageNavItem[] = [
  { id: "docker", label: "Docker", href: "/docker", icon: "globe", match: (p) => p === "/docker" || p === "/docker/uptime" },
  { id: "deploy", label: "Deployments", href: "/docker/deploy", icon: "transfer" },
];

/** DockerHubPage holds Docker (containers and sites) and Deployments under one sidebar entry. */
export function DockerHubPage({ tab }: { tab: DockerHubTab }) {
  return (
    <section className="page page-wide">
      <PageNav id="docker" label="Docker" items={ITEMS} className="is-sub" />
      {tab === "deploy" ? <DeployPage /> : <DockerPage focus={tab === "uptime" ? "sites" : undefined} />}
    </section>
  );
}
