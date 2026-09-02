export type NavKind = "node" | "workload";

export type NavGroup = "host" | "system-container" | "vm" | "application";

export type OpView = "summary" | "terminal" | "files" | "snapshots" | "console" | "gpus";

export type NavTarget = {
  kind: NavKind;
  id: string;
  name: string;
  group: NavGroup;
  typeLabel: string;
  status: string;
  nodeId?: string;
  nodeName?: string;
  terminalReady: boolean;
  filesReady: boolean;
};

export function targetKey(kind: NavKind, id: string): string {
  return `${kind}:${id}`;
}

export const GROUP_ORDER: NavGroup[] = ["host", "system-container", "vm", "application"];

export function groupHeading(group: NavGroup): string {
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

export function targetIsLive(target: NavTarget): boolean {
  const status = (target.status || "").toLowerCase();
  if (target.kind === "node") {
    return status === "available" || status === "ready" || status === "running" || status === "online";
  }
  return status === "running";
}
