import type { NodeSummary } from "../api/phase2";
import type { Workload } from "../api/phase5";
import type { Stack } from "../api/client";
import { kindLabel } from "../labels";
import { GROUP_ORDER, groupHeading, targetIsLive, type NavGroup, type NavTarget } from "./types";

export function filterNavTargets(targets: NavTarget[], query: string): NavTarget[] {
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return targets;
  }
  return targets.filter((t) => {
    const hay = [t.name, t.typeLabel, t.group, t.status, t.nodeName ?? "", t.nodeId ?? "", t.kind].join(" ").toLowerCase();
    return hay.includes(needle);
  });
}

export function buildNavTargets(input: {
  nodes: NodeSummary[];
  workloads: Workload[];
  stacks?: Stack[];
}): NavTarget[] {
  const nodeName = new Map(input.nodes.map((n) => [n.id, n.name]));
  const out: NavTarget[] = [];
  for (const node of input.nodes) {
    const target: NavTarget = {
      kind: "node",
      id: node.id,
      name: node.name || node.id,
      group: "host",
      typeLabel: "Host",
      status: node.status || "unknown",
      nodeId: node.id,
      nodeName: node.name,
      terminalReady: false,
      filesReady: false,
    };
    const live = targetIsLive(target);
    target.terminalReady = live;
    target.filesReady = live;
    out.push(target);
  }
  for (const w of input.workloads) {
    const group = groupForKind(w.kind);
    if (!group) {
      continue;
    }
    const running = (w.status || "").toLowerCase() === "running";
    const ioKind = group === "system-container" || group === "vm";
    out.push({
      kind: "workload",
      id: w.id,
      name: w.name || w.id,
      group,
      typeLabel: kindLabel(w.kind),
      status: w.status || "unknown",
      nodeId: w.node_id,
      nodeName: w.node_id ? nodeName.get(w.node_id) : undefined,
      terminalReady: running && ioKind,
      filesReady: running && ioKind,
    });
  }
  for (const stack of input.stacks ?? []) {
    for (const member of stack.members ?? []) {
      const wl = member.workload;
      if (!wl?.id || !wl.kind) {
        continue;
      }
      if (out.some((t) => t.kind === "workload" && t.id === wl.id)) {
        continue;
      }
      out.push({
        kind: "workload",
        id: wl.id,
        name: wl.name || member.service_name || wl.id,
        group: "application",
        typeLabel: `${kindLabel(wl.kind)} · ${stack.name}`,
        status: wl.status || member.status || "unknown",
        terminalReady: false,
        filesReady: false,
      });
    }
  }
  return out;
}

function groupForKind(kind?: string): NavGroup | null {
  if (kind === "system-container") {
    return "system-container";
  }
  if (kind === "vm") {
    return "vm";
  }
  if (kind === "oci") {
    return "application";
  }
  return null;
}

export function groupedTargets(targets: NavTarget[]): { group: NavGroup; heading: string; items: NavTarget[] }[] {
  return GROUP_ORDER.map((group) => ({
    group,
    heading: groupHeading(group),
    items: targets.filter((t) => t.group === group),
  })).filter((g) => g.items.length > 0);
}
