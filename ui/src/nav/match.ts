import type { OpView } from "./types";
import type { NavTarget } from "./types";

export function isWorkloadsContext(path: string): boolean {
  return path === "/workloads" || path.startsWith("/workloads/") || path.startsWith("/nodes/");
}

export function viewFromPath(path: string): OpView {
  const parts = path.split("/").filter(Boolean);
  const last = parts[parts.length - 1] ?? "";
  switch (last) {
    case "terminal":
      return "terminal";
    case "files":
      return "files";
    case "snapshots":
      return "snapshots";
    case "console":
      return "console";
    case "gpus":
      return "gpus";
    default:
      return "summary";
  }
}

export function selectedTargetFromPath(path: string): { kind: "node" | "workload"; id: string } | null {
  const parts = path.split("/").filter(Boolean);
  if (parts[0] === "nodes" && parts[1]) {
    return { kind: "node", id: parts[1] };
  }
  if (parts[0] === "workloads" && parts[1] && parts[1] !== "new" && parts[1] !== "import" && parts[1] !== "manage") {
    return { kind: "workload", id: parts[1] };
  }
  return null;
}

export function resolveView(target: NavTarget, requested: OpView): OpView {
  if (target.kind === "node") {
    if (requested === "terminal" || requested === "files") {
      return requested;
    }
    return "summary";
  }
  if (requested === "terminal") {
    return target.terminalReady ? "terminal" : "summary";
  }
  if (requested === "files") {
    return target.filesReady ? "files" : "summary";
  }
  if (requested === "console") {
    return target.group === "vm" ? "console" : "summary";
  }
  if (requested === "snapshots") {
    return target.group === "system-container" || target.group === "vm" ? "snapshots" : "summary";
  }
  if (requested === "gpus") {
    return "gpus";
  }
  return "summary";
}

export function hrefForTarget(target: NavTarget, requested: OpView): string {
  const view = resolveView(target, requested);
  if (target.kind === "node") {
    if (view === "terminal") {
      return `/nodes/${target.id}/terminal`;
    }
    if (view === "files") {
      return `/nodes/${target.id}/files`;
    }
    return `/nodes/${target.id}`;
  }
  if (view === "summary") {
    return `/workloads/${target.id}`;
  }
  return `/workloads/${target.id}/${view}`;
}

export function isCreatePath(path: string): boolean {
  return path.startsWith("/workloads/new") || path === "/workloads/import";
}

export function isManagePath(path: string): boolean {
  return path === "/workloads" || path === "/workloads/manage";
}
