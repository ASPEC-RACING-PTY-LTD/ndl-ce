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

export function hasGrant(user: GrantUser | null | undefined, perm: string): boolean {
  if (!user) {
    return false;
  }
  const grants = user.grants ?? [];
  if (grants.includes("*") || grants.includes(perm)) {
    return true;
  }
  for (const grant of grants) {
    if (grant.endsWith(".*") && perm.startsWith(grant.slice(0, -1))) {
      return true;
    }
  }
  if (user.roles?.includes("superadmin")) {
    return true;
  }
  if (user.roles?.includes("admin")) {
    return (
      perm === "api_access.manage" ||
      perm === "updates.manage" ||
      perm === "search.manage" ||
      perm === "feature.manage" ||
      perm === "identity.token.create" ||
      perm === "identity.group.manage" ||
      perm === "node.update" ||
      perm === "settings.license.read"
    );
  }
  return false;
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
