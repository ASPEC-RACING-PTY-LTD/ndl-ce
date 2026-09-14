import type { NodeSummary } from "../api/phase2";
import type { Workload } from "../api/phase5";
import type { Stack } from "../api/client";
import { isAdmin, canMutate } from "../rbac";
import { kindLabel } from "../labels";
import type { TermGroup, TermTarget } from "./types";

export function canOpenTermTarget(target: TermTarget, roles: string[] | undefined): boolean {
  if (!canMutate(roles)) {
    return false;
  }
  if (target.kind === "node") {
    return isAdmin(roles);
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
  nodes: NodeSummary[];
  workloads: Workload[];
  stacks?: Stack[];
}): TermTarget[] {
  const nodeName = new Map(input.nodes.map((n) => [n.id, n.name]));
  const out: TermTarget[] = [];
  if (isAdmin(input.roles)) {
    for (const node of input.nodes) {
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
  if (!canMutate(input.roles)) {
    return out;
  }
  for (const w of input.workloads) {
    const group = groupForKind(w.kind);
    if (!group || group === "host") {
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
      if (group !== "system-container" && group !== "vm") {
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
  const key = `${target.kind}:${target.id}`;
  return [target, ...list.filter((t) => `${t.kind}:${t.id}` !== key)].slice(0, 8);
}

export const GROUP_ORDER: TermGroup[] = ["host", "system-container", "vm", "application"];

export function groupHeading(group: TermGroup): string {
  switch (group) {
    case "host":
      return "Hosts";
    case "system-container":
      return "System containers";
    case "vm":
      return "Virtual machines";
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
