import { navigate } from "../router";
import { DeployPage } from "./DeployPage";
import { DockerPage } from "./DockerPage";
import { UptimePage } from "./UptimePage";

export type DockerHubTab = "docker" | "deploy" | "uptime";

/** DockerHubPage holds Docker, Deployments and Uptime under one sidebar entry, switched at the top. */
export function DockerHubPage({ tab }: { tab: DockerHubTab }) {
  return (
    <section className="page page-wide">
      <nav className="segmented" aria-label="Docker sections">
        <button type="button" aria-pressed={tab === "docker"} onClick={() => navigate("/docker")}>
          Docker
        </button>
        <button type="button" aria-pressed={tab === "deploy"} onClick={() => navigate("/docker/deploy")}>
          Deployments
        </button>
        <button type="button" aria-pressed={tab === "uptime"} onClick={() => navigate("/docker/uptime")}>
          Uptime
        </button>
      </nav>
      {tab === "docker" ? <DockerPage /> : tab === "deploy" ? <DeployPage /> : <UptimePage />}
    </section>
  );
}
