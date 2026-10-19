import type { Feature } from "../generated/openapi";

// Pages of a disabled feature show "This feature is disabled", so tests that
// render those pages turn the features on first.

const IDS: Feature["id"][] = [
  "oci",
  "docker",
  "gameservers",
  "cloudflare",
  "caddy",
  "nginx",
  "codehold",
  "gpu",
  "k8s",
  "distributed_storage",
  "ai",
  "automation",
];

export function featureItems(on: Feature["id"][] = IDS): Feature[] {
  return IDS.map((id) => ({
    id,
    title: id,
    enabled: on.includes(id),
    installed: on.includes(id),
    core: false,
    package_status: "installed",
    runtime_status: "not_started",
    starts_runtime: false,
    kubelet_started: false,
    workload_count: 0,
    software_only: true,
  }));
}

/** featuresOnRoute is a mocked GET /features answer with every feature on. */
export const featuresOnRoute = { status: 200, body: { items: featureItems(), base_install: "light", gpu_optional: true } };

/** enableUICapabilities turns on capabilities that are per-user switches (no backend feature). */
export function enableUICapabilities(ids: string[] = ["clustering", "integrations", "advanced-network"]) {
  globalThis.localStorage?.setItem(
    "ndl-nav-disclosure",
    JSON.stringify({ version: 1, template: "simple", enabled: ids, customVisible: [], customOrder: [] }),
  );
}

/** seedFeaturesOn makes the sidebar's cached feature list start with every feature on. */
export function seedFeaturesOn() {
  globalThis.localStorage?.setItem("ndl-features-cache", JSON.stringify(featureItems()));
  enableUICapabilities();
}
