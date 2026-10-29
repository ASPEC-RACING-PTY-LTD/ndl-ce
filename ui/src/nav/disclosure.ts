import { paletteAllowed, type PaletteRequire } from "../palette";
import { hasGrant, hasRoleAccess, itemGateOpen, type ItemUser } from "../rbac";
import { CAPABILITIES, NAV_MODULES, type NavModule } from "./modules";

/** DisclosurePrefs holds the UI-only capabilities a person turned on. */
export type DisclosurePrefs = {
  version: 1;
  enabled: string[];
};

export const EMPTY_PREFS: DisclosurePrefs = { version: 1, enabled: [] };

export function parseDisclosurePrefs(raw: string | null): DisclosurePrefs {
  if (!raw) {
    return { version: 1, enabled: [] };
  }
  try {
    const parsed = JSON.parse(raw) as Partial<DisclosurePrefs>;
    return { version: 1, enabled: Array.isArray(parsed.enabled) ? parsed.enabled.filter((id) => typeof id === "string") : [] };
  } catch {
    return { version: 1, enabled: [] };
  }
}

export function isCapabilityEnabled(
  id: string,
  prefs: DisclosurePrefs,
  featureEnabled: Record<string, boolean>,
): boolean {
  const cap = CAPABILITIES.find((item) => item.id === id);
  if (!cap) {
    return prefs.enabled.includes(id);
  }
  if (cap.featureId) {
    return Boolean(featureEnabled[cap.featureId]);
  }
  return prefs.enabled.includes(id);
}

export function allowedByRbac(mod: NavModule, roles: string[] | undefined, grants?: string[], user?: ItemUser | null): boolean {
  const byItem = itemGateOpen(user, mod.items);
  // People without a role see only the areas of the items shared with them.
  if (!hasRoleAccess({ roles, grants })) {
    return byItem;
  }
  if (byItem) {
    return true;
  }
  if (mod.anyPermission) {
    return mod.anyPermission.some((perm) => hasGrant({ roles, grants }, perm));
  }
  if (mod.permission) {
    return hasGrant({ roles, grants }, mod.permission);
  }
  if (!mod.require) {
    return true;
  }
  return paletteAllowed(roles, mod.require as PaletteRequire);
}

function capabilityOn(mod: NavModule, prefs: DisclosurePrefs, featureEnabled: Record<string, boolean>): boolean {
  if (!mod.capability) {
    return true;
  }
  return isCapabilityEnabled(mod.capability, prefs, featureEnabled);
}

/** visibleModules lists every sidebar entry this person may open with its feature on. */
export function visibleModules(
  prefs: DisclosurePrefs,
  featureEnabled: Record<string, boolean>,
  roles: string[] | undefined,
  grants?: string[],
  /** user adds the item grants (item_access, item_grants) to the check. */
  user?: ItemUser | null,
): NavModule[] {
  return NAV_MODULES.filter(
    // The feature list is closed to people without a role, so their item grants stand in for it.
    (mod) => allowedByRbac(mod, roles, grants, user) && (capabilityOn(mod, prefs, featureEnabled) || itemGateOpen(user, mod.items)),
  );
}

export function groupedModules(items: NavModule[]): { label: string; items: NavModule[] }[] {
  const groups: { label: string; items: NavModule[] }[] = [];
  const index = new Map<string, number>();
  for (const item of items) {
    const existing = index.get(item.group);
    if (existing !== undefined) {
      groups[existing].items.push(item);
      continue;
    }
    index.set(item.group, groups.length);
    groups.push({ label: item.group, items: [item] });
  }
  return groups;
}

/** NavOrder is the user's order of sidebar entries, by group label. */
export type NavOrder = Record<string, string[]>;

export const NAV_ORDER_KEY = "nav.order";

/** applyNavOrder sorts each group's entries in the user's saved order; entries not in it keep their place after the ordered ones. */
export function applyNavOrder<T extends { label: string; items: NavModule[] }>(groups: T[], order: NavOrder | null | undefined): T[] {
  if (!order) {
    return groups;
  }
  return groups.map((g) => {
    const saved = order[g.label];
    if (!Array.isArray(saved) || saved.length === 0) {
      return g;
    }
    const rank = new Map(saved.map((id, i) => [id, i]));
    const items = g.items
      .map((item, i) => ({ item, i }))
      .sort((a, b) => (rank.get(a.item.id) ?? saved.length + a.i) - (rank.get(b.item.id) ?? saved.length + b.i))
      .map((x) => x.item);
    return { ...g, items };
  });
}

/** moveNavItem moves id before (or after) target within a group's current order. */
export function moveNavItem(order: NavOrder, group: string, current: string[], id: string, target: string, after: boolean): NavOrder {
  if (id === target) {
    return order;
  }
  const list = current.filter((x) => x !== id);
  let at = list.indexOf(target);
  if (at < 0) {
    return order;
  }
  if (after) {
    at++;
  }
  list.splice(at, 0, id);
  return { ...order, [group]: list };
}
