import type { IconName } from "../components/Icon";
import { hasCap } from "../gameservers/caps";
import type { GameServer } from "../gameservers/types";
import { gameServerHref } from "./gameServers";

export type GameNavLink = {
  id: string;
  href: string;
  label: string;
  icon: IconName;
  current: boolean;
};

export type GameNavGroup = {
  label: string;
  items: GameNavLink[];
};

type Def = {
  id: string;
  label: string;
  icon: IconName;
  group: string;
  /** The tab shows when the server declares any of these capabilities. */
  caps?: string[];
};

// Seven main tabs. Configuration, startup, resources, networking,
// databases, activity, diagnostics, notes and access live as sections of
// Server Settings; schedules sit with backups.
const DEFS: Def[] = [
  { id: "overview", label: "Overview", icon: "dashboard", group: "Operate" },
  { id: "console", label: "Console", icon: "terminal", group: "Operate", caps: ["console"] },
  { id: "files", label: "Files", icon: "files", group: "Operate", caps: ["files"] },
  { id: "plugins", label: "Plugins", icon: "workloads", group: "Content", caps: ["plugins"] },
  { id: "mods", label: "Mods", icon: "workloads", group: "Content", caps: ["mods"] },
  { id: "workshop", label: "Workshop", icon: "workloads", group: "Content", caps: ["workshop"] },
  { id: "players", label: "Players", icon: "account", group: "Content", caps: ["players"] },
  { id: "backups", label: "Backups & Schedules", icon: "snapshots", group: "Protect", caps: ["backups", "schedules"] },
  { id: "settings", label: "Server Settings", icon: "settings", group: "Configure" },
];

/**
 * GAME_TAB_PERMS are the permissions a grant on one server needs to open
 * each tab (any of them). Tabs missing here need a role.
 */
export const GAME_TAB_PERMS: Record<string, string[]> = {
  overview: ["gameserver.read"],
  console: ["gameserver.console"],
  files: ["gameserver.files"],
  plugins: ["gameserver.content"],
  mods: ["gameserver.content"],
  workshop: ["gameserver.content"],
  players: ["gameserver.console"],
  backups: ["gameserver.backup", "gameserver.schedule"],
  settings: ["gameserver.config"],
};

/** TabAllow narrows a server's tabs to those the person may open. */
export type TabAllow = (tab: string) => boolean;

function shown(server: Pick<GameServer, "capabilities"> | null | undefined, def: Def, allow?: TabAllow): boolean {
  if (allow && !allow(def.id)) {
    return false;
  }
  return !def.caps || def.caps.some((cap) => hasCap(server, cap));
}

export function buildGameServerNav(
  id: string,
  server: Pick<GameServer, "capabilities"> | null | undefined,
  section: string,
  allow?: TabAllow,
): GameNavGroup[] {
  const groups: GameNavGroup[] = [];
  for (const def of DEFS) {
    if (!shown(server, def, allow)) {
      continue;
    }
    let group = groups.find((item) => item.label === def.group);
    if (!group) {
      group = { label: def.group, items: [] };
      groups.push(group);
    }
    group.items.push({
      id: def.id,
      href: gameServerHref(id, def.id),
      label: def.label,
      icon: def.icon,
      current: section === def.id,
    });
  }
  return groups;
}

export function gameServerTabList(server: Pick<GameServer, "capabilities"> | null | undefined, allow?: TabAllow): { id: string; label: string }[] {
  return DEFS.filter((def) => shown(server, def, allow)).map((def) => ({
    id: def.id,
    label: def.label,
  }));
}

/**
 * gameTabAllow is the tab filter for a person without a role: the tabs
 * their grant on this server opens. People with a role see every tab.
 */
export function gameTabAllow(roleAccess: boolean, itemPerm: (perm: string) => boolean): TabAllow | undefined {
  if (roleAccess) {
    return undefined;
  }
  return (tab) => (GAME_TAB_PERMS[tab] ?? []).some(itemPerm);
}
