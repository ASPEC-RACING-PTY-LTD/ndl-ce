import type { IconName } from "../components/Icon";
import type { PageNavItem } from "../components/PageNav";
import { hasGrant } from "../rbac";
import { IAM_PERMISSIONS, isIAMPath } from "./modules";

// The modern navigation: the sidebar keeps Dashboard, Compute and one
// Administration entry; each area has a page bar of main buttons whose
// dropdowns hold the area's pages.

type Gate = {
  permission?: string;
  anyPermission?: string[];
  /** capability names an optional feature (nav/modules CAPABILITIES). */
  capability?: string;
};

export type SectionChild = Gate & { id: string; label: string; href: string; match?: (p: string) => boolean };

export type Section = Gate & {
  id: string;
  label: string;
  href: string;
  icon: IconName;
  description?: string;
  match?: (p: string) => boolean;
  children?: SectionChild[];
};

const exact = (...paths: string[]) => (p: string) => paths.includes(p);
const under = (...bases: string[]) => (p: string) => bases.some((b) => p === b || p.startsWith(b + "/"));

export const ADMIN_SECTIONS: Section[] = [
  {
    id: "iam",
    label: "IAM",
    href: "/iam",
    icon: "shield",
    description: "Users, roles, groups, API access, approvals and the audit log.",
    anyPermission: IAM_PERMISSIONS,
    match: (p) => isIAMPath(p) || p === "/audit" || p === "/tasks",
    children: [
      { id: "users", label: "Users", href: "/users", permission: "users.read" },
      { id: "access", label: "Access control", href: "/iam/access", anyPermission: ["roles.manage", "identity.group.manage"] },
      { id: "api", label: "API Access", href: "/api-access", anyPermission: ["api_access.manage", "identity.token.create"] },
      { id: "approvals", label: "Approvals", href: "/iam/approvals", permission: "approvals.view" },
      { id: "audit", label: "Audit log", href: "/audit", match: exact("/audit", "/tasks") },
    ],
  },
  {
    id: "infrastructure",
    label: "Infrastructure",
    href: "/node",
    icon: "node",
    description: "This node, storage, networking and the cluster.",
    match: (p) => p === "/node" || p.startsWith("/node/") || under("/storage", "/network")(p) || p === "/cloudflare" || p === "/settings/cluster",
    children: [
      { id: "node", label: "Node", href: "/node", match: (p) => p === "/node" || p.startsWith("/node/") },
      { id: "storage", label: "Storage", href: "/storage", match: under("/storage") },
      { id: "network", label: "Network", href: "/network", match: (p) => under("/network")(p) || p === "/cloudflare" },
      { id: "cluster", label: "Cluster", href: "/settings/cluster", capability: "clustering" },
    ],
  },
  {
    id: "operations",
    label: "Operations",
    href: "/backups",
    icon: "events",
    description: "Backups, events, alerts, import and export, and automation.",
    match: (p) => under("/backups", "/events", "/alerts", "/import-export", "/automation")(p),
    children: [
      { id: "backups", label: "Backups", href: "/backups" },
      { id: "events", label: "Events", href: "/events" },
      { id: "alerts", label: "Alerts", href: "/alerts" },
      { id: "import-export", label: "Import / Export", href: "/import-export" },
      { id: "automation", label: "Automation", href: "/automation", capability: "automation", match: under("/automation") },
    ],
  },
  {
    id: "features",
    label: "Add Features",
    href: "/settings/features",
    icon: "settings",
    description: "Turn optional features on and off.",
    permission: "feature.manage",
    children: [{ id: "kubernetes", label: "Kubernetes", href: "/settings/kubernetes", capability: "kubernetes", permission: "feature.manage" }],
  },
  {
    id: "updates",
    label: "Updates",
    href: "/settings/updates",
    icon: "restart",
    description: "Check for, apply and roll back platform updates.",
    permission: "updates.manage",
  },
  {
    id: "appearance",
    label: "Appearance",
    href: "/admin/theme",
    icon: "palette",
    description: "Global theme, themes per role and group, default layouts.",
    permission: "settings.appearance.manage",
  },
  {
    id: "search",
    label: "Search",
    href: "/admin/search",
    icon: "search",
    description: "What search indexes, custom filters and indexing runs.",
    permission: "search.manage",
  },
  {
    id: "security",
    label: "Security",
    href: "/settings/certificates",
    icon: "lock",
    description: "Certificates and the license.",
    anyPermission: ["settings.tls.manage", "settings.license.manage"],
    match: exact("/settings/certificates", "/settings/license"),
    children: [
      { id: "certificates", label: "Certificates", href: "/settings/certificates", permission: "settings.tls.manage", capability: "integrations" },
      { id: "license", label: "License", href: "/settings/license", permission: "settings.license.manage" },
    ],
  },
  {
    id: "intelligence",
    label: "Intelligence",
    href: "/ask",
    icon: "info",
    description: "Ask and Plans.",
    capability: "ai",
    match: exact("/ask", "/plans"),
    children: [
      { id: "ask", label: "Ask", href: "/ask" },
      { id: "plans", label: "Plans", href: "/plans" },
    ],
  },
  {
    id: "catalog",
    label: "Catalog",
    href: "/docs",
    icon: "files",
    description: "The Store and the documentation.",
    match: exact("/store", "/docs"),
    children: [
      { id: "store", label: "Store", href: "/store", capability: "integrations" },
      { id: "docs", label: "Docs", href: "/docs" },
    ],
  },
];

export const INSTANCE_SECTIONS: Section[] = [
  {
    id: "workloads",
    label: "Workloads",
    href: "/workloads",
    icon: "workloads",
    description: "System containers.",
    match: (p) =>
      p === "/workloads" ||
      p === "/workloads/new/system-container" ||
      p === "/instance/components" ||
      (p.startsWith("/workloads/") && !["/workloads/new/vm", "/workloads/new/oci", "/workloads/import"].includes(p) && !p.startsWith("/workloads/game-servers")),
    children: [
      { id: "create-ct", label: "Create system container", href: "/workloads/new/system-container" },
      { id: "components", label: "Components", href: "/instance/components" },
    ],
  },
  {
    id: "vms",
    label: "VMs",
    href: "/instance/vms",
    icon: "monitor",
    description: "Virtual machines.",
    match: exact("/instance/vms", "/workloads/new/vm", "/workloads/import"),
    children: [
      { id: "create-vm", label: "Create VM", href: "/workloads/new/vm" },
      { id: "import-vm", label: "Import VM", href: "/workloads/import" },
    ],
  },
  {
    id: "game-servers",
    label: "Game Servers",
    href: "/game-servers",
    icon: "game",
    description: "Dedicated game servers.",
    capability: "gameservers",
    match: (p) => under("/game-servers", "/workloads/game-servers")(p),
    children: [
      { id: "gs-all", label: "All servers", href: "/game-servers", match: exact("/game-servers") },
      { id: "gs-fav", label: "Favourites", href: "/game-servers/favourites" },
      { id: "gs-recent", label: "Recent", href: "/game-servers/recent" },
      { id: "gs-create", label: "Create game server", href: "/game-servers/create" },
    ],
  },
  {
    id: "oci",
    label: "OCI Containers",
    href: "/oci",
    icon: "cloud",
    description: "Docker-compatible images run as lightweight containers.",
    capability: "oci",
    match: exact("/oci", "/workloads/new/oci"),
    children: [{ id: "create-oci", label: "Create OCI container", href: "/workloads/new/oci" }],
  },
  {
    id: "docker",
    label: "Docker",
    href: "/docker",
    icon: "globe",
    description: "Docker engines on this node and in containers.",
    capability: "docker",
    match: under("/docker"),
    children: [
      { id: "docker-engines", label: "Engines", href: "/docker", match: exact("/docker") },
      { id: "docker-deploy", label: "Deployments", href: "/docker/deploy" },
      { id: "docker-uptime", label: "Uptime", href: "/docker/uptime" },
    ],
  },
  {
    id: "codehold",
    label: "CodeHold",
    href: "/codehold",
    icon: "activity",
    description: "CodeHold runners and the CodeHold CLI.",
    capability: "codehold",
    match: exact("/codehold", "/codehold/cli"),
    children: [
      { id: "codehold-runners", label: "Runners", href: "/codehold", match: exact("/codehold") },
      { id: "codehold-cli", label: "CodeHold CLI", href: "/codehold/cli" },
    ],
  },
  {
    id: "templates",
    label: "Templates",
    href: "/templates",
    icon: "snapshots",
    description: "Reusable settings for every instance type.",
    match: exact("/templates"),
  },
  {
    id: "registries",
    label: "Registries",
    href: "/registries",
    icon: "storage",
    description: "Image registries.",
    match: exact("/registries"),
  },
];

/** The order the "+ Create" menu lists instance types in, by main button id. */
export const CREATE_ENTRIES: { section: string; label: string; href: string; capability?: string }[] = [
  { section: "workloads", label: "System container", href: "/workloads/new/system-container" },
  { section: "game-servers", label: "Game server", href: "/game-servers/create", capability: "gameservers" },
  { section: "oci", label: "OCI container", href: "/workloads/new/oci", capability: "oci" },
  { section: "vms", label: "Virtual machine", href: "/workloads/new/vm" },
];

type Who = { roles?: string[]; grants?: string[] } | null;

function allowed(g: Gate, user: Who, capabilityOn: (id: string) => boolean): boolean {
  if (g.capability && !capabilityOn(g.capability)) {
    return false;
  }
  if (g.anyPermission) {
    return g.anyPermission.some((p) => hasGrant(user, p));
  }
  if (g.permission) {
    return hasGrant(user, g.permission);
  }
  return true;
}

/** visibleSections filters an area's buttons and their pages to what this person may open. */
export function visibleSections(sections: Section[], user: Who, capabilityOn: (id: string) => boolean): Section[] {
  const out: Section[] = [];
  for (const s of sections) {
    // A whole area belongs to a feature: with it off, none of its pages show.
    if (s.capability && !capabilityOn(s.capability)) {
      continue;
    }
    const children = (s.children ?? []).filter((c) => allowed(c, user, capabilityOn));
    const own = allowed(s, user, capabilityOn);
    if (!own && children.length === 0) {
      continue;
    }
    // A button whose own page is closed opens its first allowed page.
    out.push({ ...s, href: own ? s.href : children[0].href, children });
  }
  return out;
}

export function toPageNav(sections: Section[]): PageNavItem[] {
  return sections.map((s) => ({
    id: s.id,
    label: s.label,
    href: s.href,
    icon: s.icon,
    match: s.match,
    children: (s.children ?? []).length > 0 ? s.children : undefined,
  }));
}

function inSection(s: Section, path: string): boolean {
  if (s.match?.(path)) {
    return true;
  }
  const base = s.href.split(/[?#]/)[0];
  if (path === base || path.startsWith(base + "/")) {
    return true;
  }
  return (s.children ?? []).some((c) => (c.match ? c.match(path) : path === c.href || path.startsWith(c.href + "/")));
}

/** isAdminPath reports whether a page belongs to Administration. */
export function isAdminPath(path: string): boolean {
  return path === "/admin" || path.startsWith("/admin/") || ADMIN_SECTIONS.some((s) => inSection(s, path));
}

/** isInstancePath reports whether a page belongs to the Instance area. */
export function isInstancePath(path: string): boolean {
  if (path === "/instance" || path.startsWith("/instance/")) {
    return true;
  }
  return INSTANCE_SECTIONS.some((s) => inSection(s, path));
}
