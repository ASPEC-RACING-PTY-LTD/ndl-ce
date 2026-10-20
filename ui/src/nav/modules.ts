export type NavRequire = "audit" | "groups" | "update";

export type NavModule = {
  id: string;
  href: string;
  label: string;
  group: string;
  match: (path: string) => boolean;
  capability?: string;
  require?: NavRequire;
  permission?: string;
  /** Shown when the user holds any of these grants. */
  anyPermission?: string[];
};

/** Grants that open at least one IAM section. */
export const IAM_PERMISSIONS = [
  "users.read",
  "roles.manage",
  "api_access.manage",
  "settings.security.manage",
  "identity.group.manage",
];

/** Paths that belong to IAM (Identity and Access Management). */
export function isIAMPath(p: string): boolean {
  return (
    p === "/iam" ||
    p.startsWith("/iam/") ||
    p === "/users" ||
    p.startsWith("/users/") ||
    p === "/roles" ||
    p.startsWith("/roles/") ||
    p === "/api-access" ||
    p === "/settings/security" ||
    p === "/groups"
  );
}

export type Capability = {
  id: string;
  title: string;
  summary: string;
  featureId?: string;
  href?: string;
  modules: string[];
};

export const NAV_MODULES: NavModule[] = [
  { id: "dashboard", href: "/", label: "Dashboard", group: "Overview", match: (p) => p === "/" },
  {
    id: "workloads",
    href: "/workloads",
    label: "Workloads",
    group: "Compute",
    match: (p) =>
      (p === "/workloads" || p.startsWith("/workloads/")) &&
      !p.startsWith("/workloads/game-servers"),
  },
  {
    id: "game-servers",
    href: "/game-servers",
    label: "Game Servers",
    group: "Compute",
    match: (p) =>
      p === "/game-servers" ||
      p.startsWith("/game-servers/") ||
      p === "/workloads/game-servers" ||
      p.startsWith("/workloads/game-servers/"),
    capability: "gameservers",
  },
  { id: "terminal", href: "/terminal", label: "Terminal", group: "Compute", match: (p) => p === "/terminal" },
  { id: "files", href: "/files", label: "Files", group: "Compute", match: (p) => p === "/files" },
  {
    id: "oci",
    href: "/oci",
    label: "OCI Containers",
    group: "Compute",
    match: (p) => p === "/oci",
    capability: "oci",
  },
  {
    id: "docker",
    href: "/docker",
    label: "Docker",
    group: "Compute",
    match: (p) => p === "/docker" || p.startsWith("/docker/"),
    capability: "docker",
  },
  {
    id: "codehold",
    href: "/codehold",
    label: "CodeHold Runners",
    group: "Compute",
    match: (p) => p === "/codehold",
    capability: "codehold",
  },
  { id: "templates", href: "/templates", label: "Templates", group: "Compute", match: (p) => p === "/templates" },
  { id: "registries", href: "/registries", label: "Registries", group: "Compute", match: (p) => p === "/registries" },
  {
    id: "node",
    href: "/node",
    label: "Node",
    group: "Infrastructure",
    match: (p) => p === "/node" || (p.startsWith("/node/") && !p.startsWith("/nodes/")),
  },
  {
    id: "storage",
    href: "/storage",
    label: "Storage",
    group: "Infrastructure",
    match: (p) => p === "/storage" || p.startsWith("/storage/"),
  },
  {
    id: "network",
    href: "/network",
    label: "Network",
    group: "Infrastructure",
    // Cloudflare, Caddy and Nginx live under Network.
    match: (p) => p === "/network" || p.startsWith("/network/") || p === "/cloudflare",
  },
  {
    id: "cluster",
    href: "/settings/cluster",
    label: "Cluster",
    group: "Infrastructure",
    match: (p) => p === "/settings/cluster",
    capability: "clustering",
  },
  {
    id: "events",
    href: "/events",
    label: "Events",
    group: "Operations",
    match: (p) => p === "/events",
  },
  { id: "alerts", href: "/alerts", label: "Alerts", group: "Operations", match: (p) => p === "/alerts" },
  { id: "backups", href: "/backups", label: "Backups", group: "Operations", match: (p) => p === "/backups" },
  {
    id: "import-export",
    href: "/import-export",
    label: "Import / Export",
    group: "Operations",
    match: (p) => p === "/import-export",
  },
  {
    id: "automation",
    href: "/automation",
    label: "Automation",
    group: "Operations",
    match: (p) => p === "/automation" || p.startsWith("/automation/"),
    capability: "automation",
  },
  {
    id: "ask",
    href: "/ask",
    label: "Ask",
    group: "Intelligence",
    match: (p) => p === "/ask",
    capability: "ai",
  },
  {
    id: "plans",
    href: "/plans",
    label: "Plans",
    group: "Intelligence",
    match: (p) => p === "/plans",
    capability: "ai",
  },
  {
    id: "store",
    href: "/store",
    label: "Store",
    group: "Catalog",
    match: (p) => p === "/store",
    capability: "integrations",
  },
  { id: "docs", href: "/docs", label: "Docs", group: "Catalog", match: (p) => p === "/docs" },
  {
    id: "iam",
    href: "/iam",
    label: "IAM",
    group: "Management",
    match: (p) => isIAMPath(p),
    anyPermission: IAM_PERMISSIONS,
  },
  {
    id: "add-features",
    href: "/settings/features",
    label: "Add Features",
    group: "Management",
    match: (p) => p === "/settings/features",
    permission: "feature.manage",
  },
  {
    id: "updates",
    href: "/settings/updates",
    label: "Updates",
    group: "Management",
    match: (p) => p === "/settings/updates",
    permission: "updates.manage",
  },
  {
    id: "license",
    href: "/settings/license",
    label: "License",
    group: "Management",
    match: (p) => p === "/settings/license",
    permission: "settings.license.manage",
  },
  {
    id: "audit",
    href: "/audit",
    label: "Audit Log",
    group: "Management",
    match: (p) => p === "/audit" || p === "/tasks",
  },
  {
    id: "kubernetes",
    href: "/settings/kubernetes",
    label: "Kubernetes",
    group: "Management",
    match: (p) => p === "/settings/kubernetes",
    capability: "kubernetes",
    permission: "feature.manage",
  },
  {
    id: "certificates",
    href: "/settings/certificates",
    label: "Certificates",
    group: "Management",
    match: (p) => p === "/settings/certificates",
    capability: "integrations",
    permission: "settings.tls.manage",
  },
];

export const CAPABILITIES: Capability[] = [
  {
    id: "kubernetes",
    title: "Kubernetes",
    summary: "Optional kubelet runtime. Enabling Kubernetes does not start kubelet. Virtual machines and system containers do not require it.",
    featureId: "k8s",
    modules: ["kubernetes"],
  },
  {
    id: "clustering",
    title: "Clustering",
    summary: "Join workers, inventory, and rolling update controls for more than one box.",
    modules: ["cluster"],
  },
  {
    id: "automation",
    title: "Automation",
    summary:
      "Flows that react to events, schedules and webhooks and drive Workloads, Backups, Docker and saved scripts. Disabling it pauses every flow and deletes none.",
    featureId: "automation",
    modules: ["automation"],
  },
  {
    id: "ai",
    title: "AI",
    summary: "Ask and Plans. The AI package is opt-in and does not start models by itself.",
    featureId: "ai",
    modules: ["ask", "plans"],
  },
  {
    id: "advanced-storage",
    title: "Advanced storage",
    summary: "Distributed storage package. Ceph is not started from this switch.",
    featureId: "distributed_storage",
    href: "/storage",
    modules: [],
  },
  {
    id: "advanced-network",
    title: "Advanced networking",
    summary: "Extra cluster networking and overlay controls beyond the local bridge.",
    href: "/network",
    modules: [],
  },
  {
    id: "integrations",
    title: "Integrations",
    summary: "Store catalog and certificate integrations.",
    modules: ["store", "certificates"],
  },
  {
    id: "oci",
    title: "OCI applications",
    summary: "Run Docker-compatible images as lightweight containers, grouped per app, with Compose import.",
    featureId: "oci",
    modules: ["oci"],
  },
  {
    id: "docker",
    title: "Docker Management",
    summary: "Discover Docker engines on this node and in system containers. Start, stop, logs, and health without wrapping docker ps.",
    featureId: "docker",
    href: "/docker",
    modules: ["docker"],
  },
  {
    id: "gameservers",
    title: "Game Servers",
    summary: "Optional dedicated game-server panel under Compute, beside Workloads. Enabling it does not start servers. Disabling it does not delete them.",
    featureId: "gameservers",
    href: "/game-servers",
    modules: ["game-servers"],
  },
  {
    id: "cloudflare",
    title: "Cloudflare",
    summary:
      "Tunnels, public hostnames and DNS from your Cloudflare accounts, under Network. Turning it off only hides it; tokens, tunnels and connectors keep running.",
    featureId: "cloudflare",
    href: "/network/cloudflare",
    modules: [],
  },
  {
    id: "caddy",
    title: "Caddy",
    summary: "Find Caddy on this node and in system containers, see its sites and add reverse proxy sites, under Network. Turning it off only hides it.",
    featureId: "caddy",
    href: "/network/caddy",
    modules: [],
  },
  {
    id: "nginx",
    title: "Nginx",
    summary: "Find Nginx on this node and in system containers, see its sites and add reverse proxy sites, under Network. Turning it off only hides it.",
    featureId: "nginx",
    href: "/network/nginx",
    modules: [],
  },
  {
    id: "codehold",
    title: "CodeHold runners",
    summary:
      "Host CodeHold Actions runners here. No-dal connects to CodeHold as a host controller and gives every runner its own system container (2 vCPU, 2 GB, 50 GB by default) with Docker inside. Turning it off asks what happens to those containers first.",
    featureId: "codehold",
    href: "/codehold",
    modules: ["codehold"],
  },
  {
    id: "gpu",
    title: "GPU services",
    summary: "Optional GPU package. Assignment stays available without this package.",
    featureId: "gpu",
    modules: [],
  },
];

export function moduleById(id: string): NavModule | undefined {
  return NAV_MODULES.find((item) => item.id === id);
}

export function moduleForPath(path: string): NavModule | undefined {
  return NAV_MODULES.find((item) => item.match(path));
}

// Gated pages that have no sidebar entry of their own.
const PATH_GATES: { capability: string; match: (p: string) => boolean }[] = [
  { capability: "cloudflare", match: (p) => p === "/cloudflare" || p === "/network/cloudflare" || p.startsWith("/network/cloudflare/") },
  { capability: "caddy", match: (p) => p === "/network/caddy" || p.startsWith("/network/caddy/") },
  { capability: "nginx", match: (p) => p === "/network/nginx" || p.startsWith("/network/nginx/") },
  { capability: "oci", match: (p) => p === "/workloads/new/oci" || p === "/stacks" || p.startsWith("/stacks/") },
  { capability: "docker", match: (p) => p === "/uptime" },
  { capability: "codehold", match: (p) => p === "/codehold/cli" },
];

/** capabilityForPath names the optional capability a page belongs to, if any. */
export function capabilityForPath(path: string): string | undefined {
  const gate = PATH_GATES.find((g) => g.match(path));
  if (gate) {
    return gate.capability;
  }
  return moduleForPath(path)?.capability;
}

/** capabilityForHref is capabilityForPath for a link, ignoring query and hash. */
export function capabilityForHref(href: string): string | undefined {
  if (!href.startsWith("/")) {
    return undefined;
  }
  return capabilityForPath(href.split(/[?#]/)[0] || "/");
}
