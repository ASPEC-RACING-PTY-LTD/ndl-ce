/** defaultFilesPath is where a Files tab opens: /root on a host, / in a workload. */
export function defaultFilesPath(kind: "node" | "workload"): string {
  return kind === "node" ? "/root" : "/";
}

/** filesWorkspaceHref opens the Files workspace on a target and directory. */
export function filesWorkspaceHref(target: { kind: "node" | "workload"; id: string; name?: string }, path?: string): string {
  const q = new URLSearchParams({ kind: target.kind, id: target.id, path: path || defaultFilesPath(target.kind) });
  if (target.name) {
    q.set("name", target.name);
  }
  return `/files?${q.toString()}`;
}
