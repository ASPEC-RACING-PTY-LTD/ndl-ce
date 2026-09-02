export type TermKind = "node" | "workload";

export type TermGroup = "host" | "system-container" | "vm" | "application";

export type TermConnState = "connecting" | "active" | "disconnected" | "reconnecting" | "closed";

export type TermTarget = {
  kind: TermKind;
  id: string;
  name: string;
  group: TermGroup;
  typeLabel: string;
  status: string;
  nodeId?: string;
  nodeName?: string;
  terminalReady: boolean;
};

export type TermTab = {
  tabId: string;
  title: string;
  customTitle: boolean;
  target: TermTarget;
  ioSessionId?: string;
  state: TermConnState;
  cwd: string;
  jailRoot: string;
  error: string | null;
  startCwd: string;
};

export function targetKey(kind: TermKind, id: string): string {
  return `${kind}:${id}`;
}

export function statusLabel(state: TermConnState): string {
  switch (state) {
    case "connecting":
      return "Connecting";
    case "active":
      return "Connected";
    case "reconnecting":
      return "Reconnecting";
    case "disconnected":
      return "Disconnected";
    case "closed":
      return "Closed";
    default:
      return state;
  }
}
