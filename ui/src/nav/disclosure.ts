import { paletteAllowed, type PaletteRequire } from "../palette";
import { CAPABILITIES, NAV_MODULES, moduleForPath, type NavModule, type NavTemplate } from "./modules";

export type DisclosurePrefs = {
  version: 1;
  template: NavTemplate;
  enabled: string[];
  customVisible: string[];
  customOrder: string[];
};

export const EMPTY_PREFS: DisclosurePrefs = {
  version: 1,
  template: "simple",
  enabled: [],
  customVisible: [],
  customOrder: [],
};

export function parseDisclosurePrefs(raw: string | null): DisclosurePrefs {
  if (!raw) {
    return { ...EMPTY_PREFS, enabled: [], customVisible: [], customOrder: [] };
  }
  try {
    const parsed = JSON.parse(raw) as Partial<DisclosurePrefs>;
    const template = parsed.template;
    return {
      version: 1,
      template: template === "advanced" || template === "custom" || template === "simple" ? template : "simple",
      enabled: Array.isArray(parsed.enabled) ? parsed.enabled.filter((id) => typeof id === "string") : [],
      customVisible: Array.isArray(parsed.customVisible) ? parsed.customVisible.filter((id) => typeof id === "string") : [],
      customOrder: Array.isArray(parsed.customOrder) ? parsed.customOrder.filter((id) => typeof id === "string") : [],
    };
  } catch {
    return { ...EMPTY_PREFS, enabled: [], customVisible: [], customOrder: [] };
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

export function allowedByRbac(mod: NavModule, roles: string[] | undefined): boolean {
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

export function visibleModules(
  prefs: DisclosurePrefs,
  featureEnabled: Record<string, boolean>,
  roles: string[] | undefined,
  path?: string,
): NavModule[] {
  const allowed = NAV_MODULES.filter((mod) => allowedByRbac(mod, roles));
  let chosen: NavModule[];
  if (prefs.template === "advanced") {
    chosen = allowed;
  } else if (prefs.template === "custom") {
    const visible = new Set(prefs.customVisible);
    chosen = allowed.filter((mod) => visible.has(mod.id));
    if (prefs.customOrder.length > 0) {
      const rank = new Map(prefs.customOrder.map((id, i) => [id, i]));
      chosen = [...chosen].sort((a, b) => (rank.get(a.id) ?? 1000) - (rank.get(b.id) ?? 1000));
    }
  } else {
    chosen = allowed.filter((mod) => mod.simple || (mod.capability && capabilityOn(mod, prefs, featureEnabled)));
  }
  const current = path ? moduleForPath(path) : undefined;
  if (current && allowedByRbac(current, roles) && !chosen.some((mod) => mod.id === current.id)) {
    chosen = [...chosen, current];
  }
  return chosen;
}

export function paletteMatchesVisible(action: { id: string; href: string }, visibleIds: Set<string>): boolean {
  const mod = NAV_MODULES.find((item) => item.id === action.id) ?? NAV_MODULES.find((item) => item.href === action.href);
  if (!mod) {
    return true;
  }
  return visibleIds.has(mod.id);
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

export function seedCustom(prefs: DisclosurePrefs, snapshotIds: string[]): DisclosurePrefs {
  if (prefs.customVisible.length > 0) {
    return prefs;
  }
  return {
    ...prefs,
    customVisible: [...snapshotIds],
    customOrder: [...snapshotIds],
  };
}

export function toggleCustomVisible(prefs: DisclosurePrefs, id: string, on: boolean): DisclosurePrefs {
  const visible = new Set(prefs.customVisible);
  const order = prefs.customOrder.filter((item) => item !== id);
  if (on) {
    visible.add(id);
    order.push(id);
  } else {
    visible.delete(id);
  }
  return { ...prefs, customVisible: [...visible], customOrder: order };
}

export function moveCustom(prefs: DisclosurePrefs, id: string, delta: number): DisclosurePrefs {
  const order = [...prefs.customOrder];
  const idx = order.indexOf(id);
  if (idx < 0) {
    return prefs;
  }
  const next = idx + delta;
  if (next < 0 || next >= order.length) {
    return prefs;
  }
  const swap = order[next];
  order[next] = id;
  order[idx] = swap;
  return { ...prefs, customOrder: order };
}
