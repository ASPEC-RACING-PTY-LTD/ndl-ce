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

function shown(server: Pick<GameServer, "capabilities"> | null | undefined, def: Def): boolean {
  return !def.caps || def.caps.some((cap) => hasCap(server, cap));
}

export function buildGameServerNav(
  id: string,
  server: Pick<GameServer, "capabilities"> | null | undefined,
  section: string,
): GameNavGroup[] {
  const groups: GameNavGroup[] = [];
  for (const def of DEFS) {
    if (!shown(server, def)) {
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

export function gameServerTabList(server: Pick<GameServer, "capabilities"> | null | undefined): { id: string; label: string }[] {
  return DEFS.filter((def) => shown(server, def)).map((def) => ({
    id: def.id,
    label: def.label,
  }));
}
