import { useSyncExternalStore } from "react";

// Node nicknames: names everyone in the cluster sees in the UI in place of a
// node's own name. The node is not renamed; the API client swaps the name
// on node lists (keeping the real one in real_name), and labels built from
// a node ID or a reported host name go through nodeLabel.

type Listener = () => void;

let nicknames: Record<string, string> = {};
let canEdit = false;
// Real node names seen on node lists, so a label known only by host name finds its node.
const idByName = new Map<string, string>();
let version = 0;
const listeners = new Set<Listener>();

export function setNodeNicknames(
  next: Record<string, string>,
  editable?: boolean,
): void {
  nicknames = { ...next };
  if (editable !== undefined) {
    canEdit = editable;
  }
  version++;
  for (const l of listeners) {
    l();
  }
}

export function nodeNickname(id: string | undefined): string | undefined {
  return id ? nicknames[id] : undefined;
}

export function canEditNodeNicknames(): boolean {
  return canEdit;
}

/** withNickname shows a node under its nickname, keeping its own name in real_name. */
export function withNickname<T extends { id: string; name: string }>(node: T): T & { real_name?: string } {
  const { real_name: previous, ...rest } = node as T & { real_name?: string };
  const real = previous ?? node.name;
  if (real) {
    idByName.set(real, node.id);
  }
  const nick = nicknames[node.id];
  return (nick ? { ...rest, name: nick, real_name: real } : { ...rest, name: real }) as T & { real_name?: string };
}

/** nodeLabel is the name to show for a node known by ID, its own name, or both. */
export function nodeLabel(id?: string | null, name?: string | null): string {
  const byId = id ? nicknames[id] : undefined;
  const byName = name ? nicknames[idByName.get(name) ?? ""] : undefined;
  return byId || byName || name || id || "";
}

function subscribe(l: Listener): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** useNodeNicknames re-renders when a nickname changes and returns nodeLabel. */
export function useNodeNicknames(): typeof nodeLabel {
  useSyncExternalStore(subscribe, () => version);
  return nodeLabel;
}
