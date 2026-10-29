import type { ItemKind } from "../api/client";

// Labels and presets for access grants on single items (IAM > Users > Item access).
// The server decides what each kind can grant; these only name and group them.

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

export function permissionLabel(kind: ItemKind, perm: string): string {
  return KIND_LABEL_OVERRIDE[kind]?.[perm] ?? LABEL[perm] ?? perm;
}

/** permissionGroup sorts a permission into a heading in the grant dialog. */
export function permissionGroup(perm: string): "Control" | "Files" | "Backups" | "Monitoring" {
  if (perm.startsWith("files.") || perm === "gameserver.files" || perm === "gameserver.content") {
    return "Files";
  }
  if (perm.startsWith("backup.") || perm === "gameserver.backup") {
    return "Backups";
  }
  if (perm === "metrics.read" || perm === "events.read") {
    return "Monitoring";
  }
  return "Control";
}

export const GROUP_ORDER = ["Control", "Files", "Backups", "Monitoring"] as const;

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
export function accessSummary(kind: ItemKind, perms: string[], grantable: string[]): string {
  const preset = grantable.length ? matchingPreset(kind, perms, grantable) : null;
  if (preset) {
    return PRESET_LABEL[preset];
  }
  const ordered = grantable.length ? [...grantable.filter((p) => perms.includes(p)), ...perms.filter((p) => !grantable.includes(p))] : perms;
  const names: string[] = [];
  for (const p of ordered) {
    const name = SHORT[p] ?? p;
    if (!names.includes(name)) {
      names.push(name);
    }
  }
  if (names.length <= 3) {
    return names.join(", ") || "None";
  }
  return `${names.slice(0, 3).join(", ")} +${names.length - 3}`;
}

/** dockerItemID is how the server names a container for a grant: machine and name, so it outlives recreation. */
export function dockerItemID(machineID: string, name: string): string {
  return `${machineID}/${name.replace(/^\//, "")}`;
}
