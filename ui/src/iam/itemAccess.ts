import type { GrantScope, ItemKind } from "../api/client";
import { permissionGroup, permissionLabel } from "../labels";

// Labels, groups and presets for direct permissions (IAM > Users > Direct
// permissions): grants to a user, group or role, everywhere or on one item.
// The server lists the permissions and checks every grant; these only name,
// group and preset them.

export const KIND_ORDER: ItemKind[] = ["workload", "gameserver", "docker", "node"];

export const KIND_LABEL: Record<ItemKind, string> = {
  workload: "Workload",
  gameserver: "Game server",
  docker: "Docker container",
  node: "Node",
};

/** READ_PERMISSION is the view permission the server always adds for each kind. */
export const READ_PERMISSION: Record<ItemKind, string> = {
  workload: "compute.read",
  gameserver: "gameserver.read",
  docker: "compute.read",
  node: "node.read",
};

const LABEL: Record<string, string> = {
  "compute.read": "View",
  "compute.lifecycle": "Power (start, stop, restart)",
  "compute.start": "Start",
  "compute.stop": "Stop",
  "compute.console": "Console",
  "compute.snapshot": "Snapshots",
  "terminal.open": "Terminal",
  "files.read": "Browse files",
  "files.download": "Download files",
  "files.upload": "Upload files",
  "files.create": "Create files and folders",
  "files.modify": "Edit and rename files",
  "files.delete": "Delete files",
  "backup.read": "View backups",
  "backup.create": "Create backups",
  "metrics.read": "Metrics",
  "events.read": "Events and logs",
  "node.read": "View",
  "gameserver.read": "View",
  "gameserver.power": "Power (start, stop, restart)",
  "gameserver.console": "Console",
  "gameserver.files": "Files",
  "gameserver.config": "Settings",
  "gameserver.content": "Mods and content",
  "gameserver.backup": "Backups",
  "gameserver.schedule": "Schedules",
  "gameserver.reinstall": "Reinstall",
};

const KIND_LABEL_OVERRIDE: Partial<Record<ItemKind, Record<string, string>>> = {
  docker: { "compute.lifecycle": "Start, stop, restart, pull and update" },
  node: {
    "terminal.open": "Root terminal on the host",
    "files.read": "Browse host files",
    "files.download": "Download host files",
    "files.upload": "Upload host files",
    "files.create": "Create host files and folders",
    "files.modify": "Edit and rename host files",
    "files.delete": "Delete host files",
  },
};

const SHORT: Record<string, string> = {
  "compute.read": "View",
  "node.read": "View",
  "gameserver.read": "View",
  "compute.lifecycle": "Power",
  "gameserver.power": "Power",
  "compute.start": "Start",
  "compute.stop": "Stop",
  "compute.console": "Console",
  "gameserver.console": "Console",
  "compute.snapshot": "Snapshots",
  "terminal.open": "Terminal",
  "files.read": "Files",
  "files.download": "Download",
  "files.upload": "Upload",
  "files.create": "Create files",
  "files.modify": "Edit files",
  "files.delete": "Delete files",
  "backup.read": "Backups",
  "backup.create": "Create backups",
  "gameserver.backup": "Backups",
  "metrics.read": "Metrics",
  "events.read": "Logs",
  "gameserver.files": "Files",
  "gameserver.config": "Settings",
  "gameserver.content": "Content",
  "gameserver.schedule": "Schedules",
  "gameserver.reinstall": "Reinstall",
};

/** itemPermissionLabel names a permission as it acts on one item of a kind, for the dialog's "This item" group. */
export function itemPermissionLabel(kind: ItemKind, perm: string): string {
  return KIND_LABEL_OVERRIDE[kind]?.[perm] ?? LABEL[perm] ?? permissionLabel(perm);
}

/**
 * scopePermissionLabel names a permission where a grant applies: the
 * item's own wording for the permissions its pages check, the general
 * wording otherwise.
 */
export function scopePermissionLabel(scope: GrantScope, perm: string, suggested: string[]): string {
  return scope !== "cluster" && suggested.includes(perm) ? itemPermissionLabel(scope, perm) : permissionLabel(perm);
}

/** SUPERADMIN_ONLY are the permissions only a SuperAdmin can give. */
export const SUPERADMIN_ONLY = ["*", "permissions.assign.override"];

export const THIS_ITEM_GROUP = "This item";

/**
 * permissionSections groups every permission for the grant dialog: on an
 * item, the permissions its own pages check come first under "This item";
 * the rest follow by area, in catalog order.
 */
export function permissionSections(scope: GrantScope, all: string[], suggested: string[]): { group: string; perms: string[] }[] {
  const sections: { group: string; perms: string[] }[] = [];
  const first = scope === "cluster" ? [] : suggested;
  if (first.length) {
    sections.push({ group: THIS_ITEM_GROUP, perms: [...first] });
  }
  const byGroup = new Map<string, string[]>();
  for (const p of all) {
    if (first.includes(p)) {
      continue;
    }
    const g = permissionGroup(p);
    byGroup.set(g, [...(byGroup.get(g) ?? []), p]);
  }
  for (const [group, perms] of byGroup) {
    sections.push({ group, perms });
  }
  return sections;
}

export type PresetId = "view" | "operate" | "full";

export const PRESET_LABEL: Record<PresetId, string> = { view: "View", operate: "Operate", full: "Full" };

const OPERATE_EXTRA: Record<ItemKind, string[]> = {
  workload: ["compute.lifecycle", "compute.start", "compute.stop", "compute.console", "terminal.open"],
  gameserver: ["gameserver.power", "gameserver.console"],
  docker: ["compute.lifecycle", "terminal.open"],
  node: ["terminal.open"],
};

function isView(kind: ItemKind, perm: string): boolean {
  // A node's files are the host's whole filesystem, so viewing a node leaves them out.
  if (kind === "node" && perm.startsWith("files.")) {
    return false;
  }
  return perm === READ_PERMISSION[kind] || perm.endsWith(".read");
}

/** presetPermissions is what a preset grants on a kind, limited to what the kind can grant. */
export function presetPermissions(kind: ItemKind, preset: PresetId, grantable: string[]): string[] {
  const view = grantable.filter((p) => isView(kind, p));
  if (preset === "view") {
    return withRead(kind, view);
  }
  if (preset === "operate") {
    const extra = new Set(OPERATE_EXTRA[kind]);
    return withRead(kind, grantable.filter((p) => isView(kind, p) || extra.has(p)));
  }
  return withRead(kind, [...grantable]);
}

/** withRead adds the kind's view permission, as the server does. */
export function withRead(kind: ItemKind, perms: string[]): string[] {
  const read = READ_PERMISSION[kind];
  return perms.includes(read) ? perms : [read, ...perms];
}

function sameSet(a: string[], b: string[]): boolean {
  const sa = new Set(a);
  const sb = new Set(b);
  return sa.size === sb.size && [...sa].every((p) => sb.has(p));
}

/** matchingPreset names the preset a permission set equals, if any. */
export function matchingPreset(kind: ItemKind, perms: string[], grantable: string[]): PresetId | null {
  for (const id of ["full", "operate", "view"] as PresetId[]) {
    if (sameSet(withRead(kind, perms), presetPermissions(kind, id, grantable))) {
      return id;
    }
  }
  return null;
}

/** accessSummary is a short line for the grant table, for example "View, Power, Terminal +2". */
export function accessSummary(scope: GrantScope, perms: string[], suggested: string[]): string {
  if (perms.includes("*")) {
    return "Everything";
  }
  if (scope !== "cluster" && suggested.length) {
    const preset = matchingPreset(scope, perms, suggested);
    if (preset) {
      return PRESET_LABEL[preset];
    }
  }
  const ordered = [...suggested.filter((p) => perms.includes(p)), ...perms.filter((p) => !suggested.includes(p))];
  const names: string[] = [];
  for (const p of ordered) {
    const name = (scope !== "cluster" ? SHORT[p] : undefined) ?? permissionLabel(p);
    if (!names.includes(name)) {
      names.push(name);
    }
  }
  if (names.length <= 3) {
    return names.join(", ") || "None";
  }
  return `${names.slice(0, 3).join(", ")} +${names.length - 3}`;
}

/** scopeLabel names where a grant applies, without the item's name. */
export function scopeLabel(scope: GrantScope): string {
  return scope === "cluster" ? "Everywhere" : KIND_LABEL[scope];
}

/** dockerItemID is how the server names a container for a grant: machine and name, so it outlives recreation. */
export function dockerItemID(machineID: string, name: string): string {
  return `${machineID}/${name.replace(/^\//, "")}`;
}
