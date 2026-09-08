export type NavTemplate = "simple" | "advanced" | "custom";
export type NavRequire = "audit" | "groups" | "update";

export type NavModule = {
  id: string;
  href: string;
  label: string;
  group: string;
  match: (path: string) => boolean;
  simple: boolean;
  capability?: string;
  require?: NavRequire;
};

export type Capability = {
  id: string;
  title: string;
  summary: string;
  featureId?: string;
  href?: string;
  modules: string[];
};

export const NAV_MODULES: NavModule[] = [
  { id: "dashboard", href: "/", label: "Dashboard", group: "Overview", match: (p) => p === "/", simple: true },
  {
    id: "api-access",
    href: "/api-access",
    label: "API Access",
    group: "Overview",
    match: (p) => p === "/api-access",
    simple: true,
  },
  {
    id: "workloads",
    href: "/workloads",
    label: "Workloads",
    group: "Compute",
    match: (p) => p === "/workloads" || p.startsWith("/workloads/"),
    simple: true,
  },
  {
    id: "import-export",
    href: "/import-export",
    label: "Import / Export",
    group: "Compute",
    match: (p) => p === "/import-export",
    simple: true,
  },
  { id: "terminal", href: "/terminal", label: "Terminal", group: "Compute", match: (p) => p === "/terminal", simple: true },
  {
    id: "stacks",
    href: "/stacks",
    label: "Stacks",
    group: "Compute",
    match: (p) => p === "/stacks" || p.startsWith("/stacks/"),
    simple: false,
    capability: "oci",
  },
  { id: "templates", href: "/templates", label: "Templates", group: "Compute", match: (p) => p === "/templates", simple: false },
  {
    id: "node",
    href: "/node",
    label: "Node",
    group: "Infrastructure",
    match: (p) => p === "/node" || (p.startsWith("/node/") && !p.startsWith("/nodes/")),
    simple: true,
  },
  {
    id: "storage",
    href: "/storage",
    label: "Storage",
    group: "Infrastructure",
    match: (p) => p === "/storage" || p.startsWith("/storage/"),
    simple: true,
  },
  {
    id: "network",
    href: "/network",
    label: "Network",
    group: "Infrastructure",
    match: (p) => p === "/network" || p.startsWith("/network/"),
    simple: true,
  },
  {
    id: "cluster",
    href: "/settings/cluster",
    label: "Cluster",
    group: "Infrastructure",
    match: (p) => p === "/settings/cluster",
    simple: false,
    capability: "clustering",
  },
  { id: "tasks", href: "/tasks", label: "Tasks", group: "Operations", match: (p) => p === "/tasks", simple: true },
  {
    id: "events",
    href: "/events",
    label: "Events",
    group: "Operations",
    match: (p) => p === "/events" || p === "/node/events",
    simple: false,
  },
  { id: "alerts", href: "/alerts", label: "Alerts", group: "Operations", match: (p) => p === "/alerts", simple: false },
  { id: "backups", href: "/backups", label: "Backups", group: "Operations", match: (p) => p === "/backups", simple: true },
  {
    id: "automation",
    href: "/automation",
    label: "Automation",
    group: "Operations",
    match: (p) => p === "/automation",
    simple: false,
    capability: "automation",
  },
  {
    id: "ask",
    href: "/ask",
    label: "Ask",
    group: "Intelligence",
    match: (p) => p === "/ask",
    simple: false,
    capability: "ai",
  },
  {
    id: "plans",
    href: "/plans",
    label: "Plans",
    group: "Intelligence",
    match: (p) => p === "/plans",
    simple: false,
    capability: "ai",
  },
  {
    id: "store",
    href: "/store",
    label: "Store",
    group: "Catalog",
    match: (p) => p === "/store",
    simple: false,
    capability: "integrations",
  },
  { id: "docs", href: "/docs", label: "Docs", group: "Catalog", match: (p) => p === "/docs", simple: false },
  {
    id: "add-features",
    href: "/settings/features",
    label: "Add Features",
    group: "Settings",
    match: (p) => p === "/settings/features",
    simple: true,
  },
  {
    id: "kubernetes",
    href: "/settings/kubernetes",
    label: "Kubernetes",
    group: "Settings",
    match: (p) => p === "/settings/kubernetes",
    simple: false,
    capability: "kubernetes",
  },
  {
    id: "certificates",
    href: "/settings/certificates",
    label: "Certificates",
    group: "Settings",
    match: (p) => p === "/settings/certificates",
    simple: false,
    capability: "integrations",
  },
  {
    id: "updates",
    href: "/settings/updates",
    label: "Updates",
    group: "Settings",
    match: (p) => p === "/settings/updates",
    simple: true,
    require: "update",
  },
  { id: "mfa", href: "/settings/mfa", label: "MFA", group: "Settings", match: (p) => p === "/settings/mfa", simple: true },
  { id: "groups", href: "/groups", label: "Groups", group: "Settings", match: (p) => p === "/groups", simple: false, require: "groups" },
  { id: "audit", href: "/audit", label: "Audit", group: "Settings", match: (p) => p === "/audit", simple: false, require: "audit" },
  {
    id: "license",
    href: "/settings/license",
    label: "License",
    group: "Settings",
    match: (p) => p === "/settings/license",
    simple: true,
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
    summary: "Storage pressure and policy automation.",
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
    summary: "Application stacks and OCI extras. Compose import stays available after enable.",
    featureId: "oci",
    modules: ["stacks"],
  },
  {
    id: "gpu",
    title: "GPU services",
    summary: "Optional GPU package. Assignment stays available without this package.",
    featureId: "gpu",
    modules: [],
  },
];

export const TEMPLATES: { id: NavTemplate; label: string; summary: string }[] = [
  {
    id: "simple",
    label: "Simple",
    summary: "Core virtualization: guests, storage, network, backups, and Add Features.",
  },
  {
    id: "advanced",
    label: "Advanced",
    summary: "Full appliance navigation. Capability enablement is unchanged.",
  },
  {
    id: "custom",
    label: "Custom",
    summary: "Choose, hide, and reorder modules. Enablement is unchanged.",
  },
];

export function moduleById(id: string): NavModule | undefined {
  return NAV_MODULES.find((item) => item.id === id);
}

export function moduleForPath(path: string): NavModule | undefined {
  return NAV_MODULES.find((item) => item.match(path));
}
