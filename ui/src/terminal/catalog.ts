import type { NodeSummary } from "../api/phase2";
import type { Workload } from "../api/phase5";
import type { Stack } from "../api/client";
import type { GameServer } from "../gameservers/types";
import { canOnItem, dockerItemId, hasGrant, itemAllows, type ItemUser } from "../rbac";
import { kindLabel } from "../labels";
import type { TermGroup, TermKind, TermTarget } from "./types";

/**
 * mayOpenTerminal reports whether the person may open a terminal on a host
 * or workload: through their role, or a terminal.open grant on that item.
 */
export function mayOpenTerminal(kind: TermKind, id: string, roles: string[] | undefined, user?: ItemUser | null, name?: string): boolean {
  const who = user ?? { roles };
  if (kind === "node") {
    return hasGrant(who, "node.host") || itemAllows(who, "node", id, "terminal.open");
  }
  if (kind === "docker") {
    // A Docker target is "machine/container id"; its grant is on "machine/container name".
    const machine = id.split("/")[0] ?? "";
    return hasGrant(who, "terminal.open") || (Boolean(name) && itemAllows(who, "docker", dockerItemId(machine, name ?? ""), "terminal.open"));
  }
  if (kind === "gameserver") {
    // The server also checks file access to this game server.
    return hasGrant(who, "terminal.open");
  }
  return canOnItem(who, "workload", id, "terminal.open");
}

export function canOpenTermTarget(target: TermTarget, roles: string[] | undefined, user?: ItemUser | null): boolean {
  if (!mayOpenTerminal(target.kind, target.id, roles, user, target.name)) {
    return false;
  }
  if (target.kind === "node") {
    return true;
  }
  return target.terminalReady;
}

export function filterTermTargets(targets: TermTarget[], query: string): TermTarget[] {
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return targets;
  }
  return targets.filter((t) => {
    const hay = [t.name, t.typeLabel, t.group, t.status, t.nodeName ?? "", t.nodeId ?? "", t.kind].join(" ").toLowerCase();
    return hay.includes(needle);
  });
}

export function buildTermCatalog(input: {
  roles?: string[];
  /** user adds the hosts and workloads the person holds a terminal grant on. */
  user?: ItemUser | null;
  nodes: NodeSummary[];
  workloads: Workload[];
  stacks?: Stack[];
  gameServers?: GameServer[];
}): TermTarget[] {
  const nodeName = new Map(input.nodes.map((n) => [n.id, n.name]));
  const out: TermTarget[] = [];
  for (const node of input.nodes) {
    if (mayOpenTerminal("node", node.id, input.roles, input.user)) {
      out.push({
        kind: "node",
        id: node.id,
        name: node.name || node.id,
        group: "host",
        typeLabel: "Host",
        status: node.status || "unknown",
        nodeId: node.id,
        nodeName: node.name,
        terminalReady: true,
      });
    }
  }
  for (const w of input.workloads) {
    const group = groupForKind(w.kind);
    if (!group || group === "host" || !mayOpenTerminal("workload", w.id, input.roles, input.user)) {
      continue;
    }
    const running = (w.status || "").toLowerCase() === "running";
    out.push({
      kind: "workload",
      id: w.id,
      name: w.name || w.id,
      group,
      typeLabel: kindLabel(w.kind),
      status: w.status || "unknown",
      nodeId: w.node_id,
      nodeName: w.node_id ? nodeName.get(w.node_id) : undefined,
      terminalReady: running && (group === "system-container" || group === "vm"),
    });
  }
  for (const stack of input.stacks ?? []) {
    for (const member of stack.members ?? []) {
      const wl = member.workload;
      if (!wl?.id || !wl.kind) {
        continue;
      }
      const group = groupForKind(wl.kind);
      if ((group !== "system-container" && group !== "vm") || !mayOpenTerminal("workload", wl.id, input.roles, input.user)) {
        continue;
      }
      if (out.some((t) => t.kind === "workload" && t.id === wl.id)) {
        continue;
      }
      const running = (wl.status || member.status || "").toLowerCase() === "running";
      out.push({
        kind: "workload",
        id: wl.id,
        name: wl.name || member.service_name || wl.id,
        group: "application",
        typeLabel: `${kindLabel(wl.kind)} · ${stack.name}`,
        status: wl.status || member.status || "unknown",
        terminalReady: running,
      });
    }
  }
  for (const gs of input.gameServers ?? []) {
    if (!mayOpenTerminal("gameserver", gs.id, input.roles, input.user)) {
      continue;
    }
    out.push({
      kind: "gameserver",
      id: gs.id,
      name: gs.name || gs.id,
      group: "game-server",
      typeLabel: `Game server · ${gs.template_name || gs.game}`,
      status: gs.status || "unknown",
      terminalReady: (gs.status || "").toLowerCase() === "running",
    });
  }
  return out;
}

function groupForKind(kind?: string): TermGroup | null {
  if (kind === "system-container") {
    return "system-container";
  }
  if (kind === "vm") {
    return "vm";
  }
  if (kind === "host") {
    return "host";
  }
  return null;
}

export function pushRecent(list: TermTarget[], target: TermTarget): TermTarget[] {
  // Hosts and nodes always sit at the top, so they are never recents.
  if (target.kind === "node") {
    return list;
  }
  const key = `${target.kind}:${target.id}`;
  return [target, ...list.filter((t) => `${t.kind}:${t.id}` !== key)].slice(0, 8);
}

export const GROUP_ORDER: TermGroup[] = ["host", "system-container", "vm", "game-server", "application"];

export function groupHeading(group: TermGroup): string {
  switch (group) {
    case "host":
      return "Host & Nodes";
    case "system-container":
      return "System containers";
    case "vm":
      return "Virtual machines";
    case "game-server":
      return "Game servers";
    case "application":
      return "Applications";
    default:
      return group;
  }
}

export function targetFromNode(node: NodeSummary): TermTarget {
  return {
    kind: "node",
    id: node.id,
    name: node.name || node.id,
    group: "host",
    typeLabel: "Host",
    status: node.status || "unknown",
    nodeId: node.id,
    nodeName: node.name,
    terminalReady: true,
  };
}

export function targetFromWorkload(w: Workload, nodeName?: string): TermTarget {
  const group = groupForKind(w.kind);
  const running = (w.status || "").toLowerCase() === "running";
  const readyKind = group === "system-container" || group === "vm";
  return {
    kind: "workload",
    id: w.id,
    name: w.name || w.id,
    group: group ?? "application",
    typeLabel: kindLabel(w.kind),
    status: w.status || "unknown",
    nodeId: w.node_id,
    nodeName,
    terminalReady: running && readyKind,
  };
}
