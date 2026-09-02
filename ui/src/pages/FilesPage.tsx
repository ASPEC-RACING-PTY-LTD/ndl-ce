import { useEffect, useState } from "react";
import {
  deleteFile,
  downloadFile,
  getWorkload,
  listFiles,
  mkdirFile,
  uploadFile,
} from "../api/client";
import type { FileEntry } from "../api/phase6";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { formatBytes } from "../format";
import { fileTypeLabel } from "../labels";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";

function idsFromPath(): { kind: "node" | "workload"; id: string } {
  const parts = currentPath().split("/").filter(Boolean);
  if (parts[0] === "nodes") {
    return { kind: "node", id: parts[1] ?? "" };
  }
  return { kind: "workload", id: parts[1] ?? "" };
}

function pathFromQuery(): string {
  const q = new URLSearchParams(window.location.search);
  return q.get("path") || "/";
}

export function FilesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const { kind, id } = idsFromPath();
  const host = kind === "node";
  const canWrite = host ? roles?.includes("admin") : Boolean(roles?.includes("admin") || roles?.includes("operator"));
  const canDownload = host ? roles?.includes("admin") : Boolean(roles?.includes("admin") || roles?.includes("operator"));
  const canRead = host ? roles?.includes("admin") : Boolean(roles?.includes("admin") || roles?.includes("operator") || roles?.includes("viewer"));
  const [path, setPath] = useState(pathFromQuery);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [mkdirName, setMkdirName] = useState("");
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (kind !== "workload") {
      return;
    }
    let cancelled = false;
    void getWorkload(id)
      .then((w) => {
        if (!cancelled && w.kind !== "system-container") {
          setUnsupported("Files are not available for this workload type.");
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [kind, id]);

  async function reload(next = path) {
    const listed = await listFiles(kind, id, next);
    setEntries(listed.entries ?? []);
    setPath(next);
  }

  useEffect(() => {
    if (!canRead || unsupported) {
      return;
    }
    let cancelled = false;
    void reload(path).catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canRead, unsupported, kind, id]);

  async function onUpload(files: FileList | null) {
    if (!files?.[0]) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const dest = joinPath(path, files[0].name);
      await uploadFile(kind, id, dest, files[0]);
      await reload(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed");
    } finally {
      setBusy(false);
    }
  }

  async function onMkdir() {
    setBusy(true);
    setError(null);
    try {
      await mkdirFile(kind, id, joinPath(path, mkdirName));
      setMkdirName("");
      await reload(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create folder failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(entry: FileEntry) {
    if (!window.confirm(`Delete ${entry.name}?`)) {
      return;
    }
    setBusy(true);
    try {
      await deleteFile(kind, id, joinPath(path, entry.name));
      await reload(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "delete failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDownload(entry: FileEntry) {
    try {
      await downloadFile(kind, id, joinPath(path, entry.name), entry.name);
    } catch (err) {
      setError(err instanceof Error ? err.message : "download failed");
    }
  }

  if (unsupported) {
    return (
      <section className="page">
        <h1>Files</h1>
        <p className="banner banner-warn" role="status">
          {unsupported}
        </p>
        <Link href={`/workloads/${id}`}>Back to workload</Link>
      </section>
    );
  }

  if (!canRead) {
    return (
      <section className="page">
        <h1>Files</h1>
        <p className="banner banner-error" role="alert">
          {host ? "Host files require admin." : "Files require at least viewer."}
        </p>
      </section>
    );
  }

  const termHref = host ? `/nodes/${id}/terminal` : `/workloads/${id}/terminal`;
  const backHref = host ? "/node" : `/workloads/${id}`;

  return (
    <section className="page page-wide" aria-labelledby="files-heading">
      <header className="page-header">
        <h1 id="files-heading">Files</h1>
        <p className="page-kicker">{path}</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <nav className="subnav" aria-label="IO">
        <Link href={backHref}>Back</Link>
        <Link href={termHref}>Terminal</Link>
      </nav>
      <div className="btn-row">
        <button className="btn" type="button" onClick={() => void reload(parentPath(path))}>
          Up
        </button>
        {canWrite ? (
          <>
            <label className="btn">
              Upload Here
              <input
                className="visually-hidden"
                type="file"
                onChange={(e) => void onUpload(e.target.files)}
                disabled={busy}
              />
            </label>
            <button className="btn" type="button" onClick={() => navigate(termHref)}>
              Terminal Here
            </button>
          </>
        ) : null}
      </div>
      {canWrite ? (
        <div className="btn-row">
          <Field id="mkdir" label="New folder" value={mkdirName} onChange={(e) => setMkdirName(e.target.value)} />
          <button className="btn btn-primary" type="button" disabled={busy || !mkdirName} onClick={() => void onMkdir()}>
            Create folder
          </button>
        </div>
      ) : null}
      <article className="panel">
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Size</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr key={entry.name}>
                  <td>
                    {entry.type === "dir" ? (
                      <button className="btn btn-ghost" type="button" onClick={() => void reload(joinPath(path, entry.name))}>
                        {entry.name}
                      </button>
                    ) : (
                      entry.name
                    )}
                  </td>
                  <td>{fileTypeLabel(entry.type)}</td>
                  <td>{entry.type === "dir" ? "" : formatBytes(entry.size)}</td>
                  <td>
                    <div className="btn-row">
                      {entry.type !== "dir" && canDownload ? (
                        <button className="btn" type="button" onClick={() => void onDownload(entry)}>
                          Download
                        </button>
                      ) : null}
                      {canWrite ? (
                        <button className="btn" type="button" onClick={() => void onDelete(entry)}>
                          Delete
                        </button>
                      ) : null}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {entries.length === 0 ? <p className="muted">This directory is empty.</p> : null}
        </div>
      </article>
    </section>
  );
}

function joinPath(base: string, name: string): string {
  if (!base || base === "/" || base === ".") {
    return name;
  }
  return `${base.replace(/\/+$/, "")}/${name}`;
}

function parentPath(p: string): string {
  const clean = p.replace(/\/+$/, "");
  const i = clean.lastIndexOf("/");
  if (i <= 0) {
    return "/";
  }
  return clean.slice(0, i);
}
