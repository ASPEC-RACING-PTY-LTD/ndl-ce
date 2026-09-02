export type PaletteRequire = "mutate" | "audit" | "update" | "groups";

export type PaletteAction = {
  id: string;
  label: string;
  href: string;
  keywords: string[];
  require?: PaletteRequire;
};

export const PALETTE_ACTIONS: PaletteAction[] = [
  {
    id: "create-vm",
    label: "Create VM",
    href: "/workloads/new/vm",
    keywords: ["virtual", "machine", "qemu", "guest"],
    require: "mutate",
  },
  {
    id: "create-ct",
    label: "Create system container",
    href: "/workloads/new/system-container",
    keywords: ["lxc", "container", "ct"],
    require: "mutate",
  },
  {
    id: "create-oci",
    label: "Create OCI application",
    href: "/workloads/new/oci",
    keywords: ["oci", "containerd", "docker", "image"],
    require: "mutate",
  },
  { id: "dashboard", label: "Dashboard", href: "/", keywords: ["home", "overview"] },
  { id: "workloads", label: "Workloads", href: "/workloads", keywords: ["vm", "container"] },
  { id: "stacks", label: "Stacks", href: "/stacks", keywords: ["compose", "multi-container", "oci"] },
  {
    id: "import-stack",
    label: "Import Compose stack",
    href: "/stacks",
    keywords: ["compose", "yaml", "docker-compose"],
    require: "mutate",
  },
  {
    id: "templates",
    label: "VM templates",
    href: "/templates",
    keywords: ["clone", "golden", "image"],
    require: "mutate",
  },
  {
    id: "import-vm",
    label: "Import VM",
    href: "/workloads/import",
    keywords: ["qcow2", "disk", "convert"],
    require: "mutate",
  },
  { id: "tasks", label: "Jump to tasks", href: "/tasks", keywords: ["task", "operation", "job"] },
  { id: "storage", label: "Storage", href: "/storage", keywords: ["pool", "volume", "rbd", "ceph"] },
  { id: "network", label: "Network", href: "/network", keywords: ["bridge", "nic"] },
  { id: "node", label: "Node", href: "/node", keywords: ["host", "hardware"] },
  { id: "cluster", label: "Cluster", href: "/settings/cluster", keywords: ["join", "worker", "node"] },
  { id: "features", label: "Features", href: "/settings/features", keywords: ["modules", "kubernetes", "gpu", "oci"] },
  { id: "kubernetes", label: "Kubernetes", href: "/settings/kubernetes", keywords: ["kubelet", "k8s"] },
  { id: "store", label: "Store", href: "/store", keywords: ["apps", "manifest", "jellyfin"] },
  { id: "automation", label: "Automation", href: "/automation", keywords: ["policy", "storage", "pressure", "migrate"] },
  { id: "ask", label: "Ask", href: "/ask", keywords: ["ai", "assistant", "diagnose"] },
  { id: "plans", label: "Plans", href: "/plans", keywords: ["ai", "approve", "operate", "automate"] },
  { id: "events", label: "Events", href: "/events", keywords: ["timeline"] },
  { id: "alerts", label: "Alerts", href: "/alerts", keywords: ["notify"] },
  { id: "backups", label: "Backups", href: "/backups", keywords: ["restore"] },
  { id: "account", label: "Account", href: "/me", keywords: ["profile", "ux", "expert"] },
  { id: "certificates", label: "Certificates", href: "/settings/certificates", keywords: ["tls", "https"] },
  { id: "mfa", label: "MFA", href: "/settings/mfa", keywords: ["totp"] },
  {
    id: "updates",
    label: "Updates",
    href: "/settings/updates",
    keywords: ["apt", "package"],
    require: "update",
  },
  {
    id: "groups",
    label: "Groups",
    href: "/groups",
    keywords: ["rbac", "members"],
    require: "groups",
  },
  {
    id: "audit",
    label: "Audit",
    href: "/audit",
    keywords: ["log", "security"],
    require: "audit",
  },
];

export function paletteAllowed(roles: string[] | undefined, require?: PaletteRequire): boolean {
  if (!require) {
    return true;
  }
  const admin = Boolean(roles?.includes("admin"));
  const operator = Boolean(roles?.includes("operator"));
  switch (require) {
    case "mutate":
    case "update":
    case "groups":
      return admin || operator;
    case "audit":
      return admin;
    default:
      return false;
  }
}

export function visiblePaletteActions(roles: string[] | undefined): PaletteAction[] {
  return PALETTE_ACTIONS.filter((action) => paletteAllowed(roles, action.require));
}

export function filterPaletteActions(actions: PaletteAction[], query: string): PaletteAction[] {
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return actions;
  }
  return actions.filter((action) => {
    if (action.label.toLowerCase().includes(needle)) {
      return true;
    }
    if (action.href.toLowerCase().includes(needle)) {
      return true;
    }
    return action.keywords.some((word) => word.includes(needle));
  });
}
