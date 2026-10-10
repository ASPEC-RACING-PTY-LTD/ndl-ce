/** filesWorkspaceHref opens the Files workspace on a target and directory. */
export function filesWorkspaceHref(target: { kind: "node" | "workload"; id: string; name?: string }, path = "/"): string {
  const q = new URLSearchParams({ kind: target.kind, id: target.id, path: path || "/" });
  if (target.name) {
    q.set("name", target.name);
  }
  return `/files?${q.toString()}`;
}
