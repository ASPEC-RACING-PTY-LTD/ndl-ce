export function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin") || roles?.includes("admin"));
}

export function isAdmin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin"));
}

export function mutateHint(roles: string[] | undefined): string | null {
  return canMutate(roles) ? null : "Requires Admin or SuperAdmin.";
}

export type GrantUser = {
  roles?: string[];
  grants?: string[];
};

/**
 * Built-in role permissions (internal/rbac PermissionsForRole). The server
 * always sends the resolved grants on /me; these only stand in when a user
 * object carries roles without grants.
 */
const ADMIN_PERMS = [
  "identity.read", "identity.token.create", "identity.token.revoke", "identity.mfa", "identity.group.manage", "cluster.read",
  "node.read", "events.read", "metrics.read", "alert.read", "alert.manage",
  "storage.read", "storage.pool.create", "storage.volume.create", "storage.image.upload",
  "network.read", "network.create", "network.apply",
  "compute.read", "compute.create", "compute.lifecycle",
  "compute.modify", "compute.start", "compute.stop", "compute.delete", "compute.console", "compute.snapshot", "storage.snapshot", "compute.gpu.assign", "compute.migrate",
  "backup.read", "backup.create", "backup.restore", "node.update", "cluster.join", "node.revoke",
  "terminal.open", "files.read", "files.download", "files.upload", "files.create", "files.modify", "files.delete",
  "files.permissions", "files.ownership",
  "settings.tls.read", "feature.read", "feature.manage", "store.read", "store.install", "store.verify", "policy.read", "policy.apply", "policy.run", "ai.ask", "ai.manage",
  "settings.license.read",
  "migration.read", "migration.import", "migration.export", "migration.manage",
  "api_access.manage", "updates.manage",
  "gameserver.read", "gameserver.create", "gameserver.power", "gameserver.console", "gameserver.files", "gameserver.config",
  "gameserver.content", "gameserver.backup", "gameserver.schedule", "gameserver.network", "gameserver.users",
  "gameserver.reinstall", "gameserver.delete", "gameserver.admin",
  "cloudflare.read", "cloudflare.manage", "search.manage",
  "ddos.read", "ddos.manage",
];

const USER_PERMS = [
  "identity.read", "identity.mfa", "cluster.read", "node.read", "events.read", "metrics.read", "alert.read", "storage.read", "network.read",
  "compute.read", "files.read", "settings.tls.read", "backup.read", "feature.read", "store.read", "policy.read", "ai.ask",
  "settings.license.read", "migration.read", "gameserver.read", "cloudflare.read", "ddos.read",
];

function rolePerms(roles: string[] | undefined): string[] {
  const out: string[] = [];
  for (const role of roles ?? []) {
    if (role === "superadmin") {
      out.push("*");
    } else if (role === "admin") {
      out.push(...ADMIN_PERMS);
    } else if (role === "user") {
      out.push(...USER_PERMS);
    }
  }
  return out;
}

export function hasGrant(user: GrantUser | null | undefined, perm: string): boolean {
  if (!user) {
    return false;
  }
  const grants = user.grants ?? rolePerms(user.roles);
  if (grants.includes("*") || grants.includes(perm)) {
    return true;
  }
  for (const grant of grants) {
    if (grant.endsWith(".*") && perm.startsWith(grant.slice(0, -1))) {
      return true;
    }
  }
  return Boolean(user.roles?.includes("superadmin"));
}

export function canSeeManagement(user: GrantUser | null | undefined): boolean {
  return (
    hasGrant(user, "users.read") ||
    hasGrant(user, "roles.manage") ||
    hasGrant(user, "api_access.manage") ||
    hasGrant(user, "feature.manage") ||
    hasGrant(user, "updates.manage") ||
    hasGrant(user, "settings.license.manage") ||
    hasGrant(user, "audit.read") ||
    hasGrant(user, "settings.security.manage") ||
    hasGrant(user, "identity.group.manage") ||
    hasGrant(user, "settings.tls.manage")
  );
}

/** ItemKind names what a single-item grant is for. */
export type ItemKind = "workload" | "gameserver" | "docker" | "node";

export type ItemUser = GrantUser & {
  item_access?: Record<string, unknown>;
  item_grants?: { kind: string; id: string; permissions: string[] }[];
};

function permMatches(list: string[], perm: string): boolean {
  if (list.includes("*") || list.includes(perm)) {
    return true;
  }
  return list.some((g) => g.endsWith(".*") && perm.startsWith(g.slice(0, -1)));
}

/** dockerItemId is a Docker container's item id: its machine and its name, without a leading slash. */
export function dockerItemId(machineId: string, name: string): string {
  return `${machineId}/${name.replace(/^\/+/, "")}`;
}

/** itemPerms lists what one item grant allows on that item (empty when none). */
export function itemPerms(user: ItemUser | null | undefined, kind: ItemKind, id: string): string[] {
  if (!user || !id) {
    return [];
  }
  const out = new Set<string>();
  for (const g of user.item_grants ?? []) {
    if (g.kind === kind && g.id === id) {
      for (const p of g.permissions ?? []) {
        out.add(p);
      }
    }
  }
  return [...out];
}

/** itemAllows reports whether a grant on that one item (not a role) allows perm. */
export function itemAllows(user: ItemUser | null | undefined, kind: ItemKind, id: string, perm: string): boolean {
  return permMatches(itemPerms(user, kind, id), perm);
}

/** canOnItem reports whether the person may do perm on one item, through a role or a grant on that item. */
export function canOnItem(user: ItemUser | null | undefined, kind: ItemKind, id: string, perm: string): boolean {
  return hasGrant(user, perm) || itemAllows(user, kind, id, perm);
}

/** itemAccessPerms lists the permissions held on items of a kind, merged across those items. */
function itemAccessPerms(user: ItemUser | null | undefined, kind: ItemKind): string[] {
  const merged = user?.item_access?.[kind];
  const out = new Set<string>(Array.isArray(merged) ? merged.filter((p): p is string => typeof p === "string") : []);
  for (const g of user?.item_grants ?? []) {
    if (g.kind === kind) {
      for (const p of g.permissions ?? []) {
        out.add(p);
      }
    }
  }
  return [...out];
}

/** hasItemAccess reports whether the person holds a grant on at least one item of a kind (with perm, when given). */
export function hasItemAccess(user: ItemUser | null | undefined, kind: ItemKind, perm?: string): boolean {
  const perms = itemAccessPerms(user, kind);
  if (perms.length === 0) {
    return false;
  }
  return perm ? permMatches(perms, perm) : true;
}

/** grantedItemIds lists the ids of the items of a kind the person holds a grant on (with perm, when given). */
export function grantedItemIds(user: ItemUser | null | undefined, kind: ItemKind, perm?: string): string[] {
  const out: string[] = [];
  for (const g of user?.item_grants ?? []) {
    if (g.kind === kind && (!perm || permMatches(g.permissions ?? [], perm)) && !out.includes(g.id)) {
      out.push(g.id);
    }
  }
  return out;
}

/**
 * hasRoleAccess reports whether the person holds a role that opens the
 * appliance. People without one (only item grants) get just their own
 * account permissions.
 */
export function hasRoleAccess(user: GrantUser | null | undefined): boolean {
  if (!user) {
    return false;
  }
  if ((user.roles?.length ?? 0) > 0) {
    return true;
  }
  return (user.grants ?? []).some((g) => !g.startsWith("identity."));
}

/** ItemGate names the item kind (and permission on it) that also opens a page. */
export type ItemGate = { kind: ItemKind; perm?: string };

/** itemGateOpen reports whether any of the item gates is met. */
export function itemGateOpen(user: ItemUser | null | undefined, gates: ItemGate[] | undefined): boolean {
  return (gates ?? []).some((g) => hasItemAccess(user, g.kind, g.perm));
}

/**
 * firstItemArea is where a person with item grants only starts: the first
 * area holding one of their items, or null when nothing is shared with them.
 */
export function firstItemArea(user: ItemUser | null | undefined): string | null {
  if (hasItemAccess(user, "workload")) {
    // The Instance overview lists every kind; /workloads may show system containers only.
    return "/instance";
  }
  if (hasItemAccess(user, "gameserver")) {
    return "/game-servers";
  }
  if (hasItemAccess(user, "docker")) {
    return "/docker";
  }
  if (hasItemAccess(user, "node")) {
    // The Node page opens the first node the person may see.
    return "/node";
  }
  return null;
}
