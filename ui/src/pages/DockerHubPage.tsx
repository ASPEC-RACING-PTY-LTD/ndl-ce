import { PageNav, type PageNavItem } from "../components/PageNav";
import { DeployPage } from "./DeployPage";
import { DockerPage } from "./DockerPage";
import { UptimePage } from "./UptimePage";

export type DockerHubTab = "docker" | "deploy" | "uptime";

const ITEMS: PageNavItem[] = [
  { id: "docker", label: "Docker", href: "/docker", icon: "globe", match: (p) => p === "/docker" },
  { id: "deploy", label: "Deployments", href: "/docker/deploy", icon: "transfer" },
  { id: "uptime", label: "Uptime", href: "/docker/uptime", icon: "activity" },
];

/** DockerHubPage holds Docker, Deployments and Uptime under one sidebar entry, switched from its page bar. */
export function DockerHubPage({ tab }: { tab: DockerHubTab }) {
  return (
    <section className="page page-wide">
      <PageNav id="docker" label="Docker" icon="globe" items={ITEMS} />
      {tab === "docker" ? <DockerPage /> : tab === "deploy" ? <DeployPage /> : <UptimePage />}
    </section>
  );
}
