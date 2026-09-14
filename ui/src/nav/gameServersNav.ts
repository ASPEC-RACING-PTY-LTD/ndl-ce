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
  cap?: string;
};

const DEFS: Def[] = [
  { id: "overview", label: "Overview", icon: "dashboard", group: "Operate" },
  { id: "console", label: "Console", icon: "terminal", group: "Operate", cap: "console" },
  { id: "files", label: "Files", icon: "files", group: "Operate", cap: "files" },
  { id: "config", label: "Configuration", icon: "settings", group: "Operate", cap: "config" },
  { id: "startup", label: "Startup", icon: "settings", group: "Operate", cap: "startup" },
  { id: "plugins", label: "Plugins", icon: "workloads", group: "Content", cap: "plugins" },
  { id: "mods", label: "Mods", icon: "workloads", group: "Content", cap: "mods" },
  { id: "workshop", label: "Workshop", icon: "workloads", group: "Content", cap: "workshop" },
  { id: "players", label: "Players", icon: "account", group: "Content", cap: "players" },
  { id: "backups", label: "Backups", icon: "snapshots", group: "Protect", cap: "backups" },
  { id: "schedules", label: "Schedules", icon: "tasks", group: "Protect", cap: "schedules" },
  { id: "network", label: "Networking", icon: "network", group: "Connect", cap: "network" },
  { id: "resources", label: "Resources", icon: "storage", group: "Connect", cap: "resources" },
  { id: "databases", label: "Databases", icon: "storage", group: "Connect", cap: "databases" },
  { id: "activity", label: "Activity", icon: "activity", group: "Review" },
  { id: "diagnostics", label: "Diagnostics", icon: "info", group: "Review" },
  { id: "settings", label: "Settings", icon: "settings", group: "Review" },
];

export function buildGameServerNav(
  id: string,
  server: Pick<GameServer, "capabilities"> | null | undefined,
  section: string,
): GameNavGroup[] {
  const groups: GameNavGroup[] = [];
  for (const def of DEFS) {
    if (def.cap && !hasCap(server, def.cap)) {
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
  return DEFS.filter((def) => !def.cap || hasCap(server, def.cap)).map((def) => ({
    id: def.id,
    label: def.label,
  }));
}
