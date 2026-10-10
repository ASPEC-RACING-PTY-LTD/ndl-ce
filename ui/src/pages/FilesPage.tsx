import { FilesBrowser } from "../components/FilesBrowser";
import { currentPath } from "../router";

function idsFromPath(): { kind: "node" | "workload"; id: string } {
  const parts = currentPath().split("/").filter(Boolean);
  if (parts[0] === "nodes") {
    return { kind: "node", id: parts[1] ?? "" };
  }
  return { kind: "workload", id: parts[1] ?? "" };
}

function syncUrl(nextPath: string, editName: string) {
  const u = new URL(window.location.href);
  u.searchParams.set("path", nextPath);
  if (editName) {
    u.searchParams.set("edit", editName);
  } else {
    u.searchParams.delete("edit");
  }
  window.history.replaceState({}, "", `${u.pathname}${u.search}`);
}

/** FilesPage is one workload's or host's files at /workloads/{id}/files or /nodes/{id}/files. */
export function FilesPage() {
  const { kind, id } = idsFromPath();
  const query = new URLSearchParams(window.location.search);
  return (
    <FilesBrowser
      key={`${kind}:${id}`}
      kind={kind}
      id={id}
      initialPath={query.get("path") || "/"}
      initialEdit={query.get("edit") || ""}
      onLocation={syncUrl}
    />
  );
}
