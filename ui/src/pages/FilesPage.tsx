import { useEffect, useState } from "react";
import {
  deleteFile,
  downloadFile,
  listFiles,
  mkdirFile,
  uploadFile,
} from "../api/client";
import type { FileEntry } from "../api/phase6";
import { ActionMenu } from "../components/ActionMenu";
import { EmptyState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { formatBytes } from "../format";
import { workloadGuestIOReason } from "../guestIO";
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
  const canRead = host
    ? roles?.includes("admin")
    : Boolean(roles?.includes("admin") || roles?.includes("operator") || roles?.includes("viewer"));
  const [path, setPath] = useState(pathFromQuery);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [mkdirName, setMkdirName] = useState("");
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ioReady, setIoReady] = useState(kind === "node");

  useEffect(() => {
    if (kind !== "workload") {
      setIoReady(true);
      return;
    }
    let cancelled = false;
    async function check() {
      try {
        const reason = await workloadGuestIOReason(id);
        if (cancelled) {
          return;
        }
        if (reason) {
          setUnsupported(reason);
          setIoReady(false);
          return;
        }
        setUnsupported(null);
        setIoReady(true);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    }
    void check();
    const timer = window.setInterval(() => {
      void check();
    }, 4000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [kind, id]);

  async function reload(next = path) {
    const listed = await listFiles(kind, id, next);
    setEntries(listed.entries ?? []);
    setPath(next);
  }

  useEffect(() => {
    if (!canRead || unsupported || !ioReady) {
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
  }, [canRead, unsupported, ioReady, kind, id]);

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
      setError(err instanceof Error ? err.message : "mkdir failed");
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
      <section className="page" aria-labelledby="files-heading">
        <PageHeader id="files-heading" title="Files" />
        <p className="banner banner-warn" role="status">
          {unsupported}
        </p>
        <Link href={`/workloads/${id}`}>Back to workload</Link>
      </section>
    );
  }

  if (!canRead) {
    return (
      <section className="page" aria-labelledby="files-heading">
        <PageHeader id="files-heading" title="Files" />
        <p className="banner banner-error" role="alert">
          {host ? "Host files require admin." : "Files require at least viewer."}
        </p>
      </section>
    );
  }

  const termHref = host ? `/nodes/${id}/terminal` : `/workloads/${id}/terminal`;
  const termHereHref = `${termHref}?cwd=${encodeURIComponent(path)}`;
  const backHref = host ? "/node" : `/workloads/${id}`;

  return (
    <section className="page page-wide" aria-labelledby="files-heading">
      <PageHeader id="files-heading" title="Files" kicker={path} />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <nav className="subnav" aria-label="IO">
        <Link href={backHref}>Back</Link>
        <Link href={termHref}>Terminal</Link>
      </nav>
      <div className="btn-row is-flush">
        <button className="btn btn-sm btn-secondary" type="button" onClick={() => void reload(parentPath(path))}>
          Up
        </button>
        {canWrite ? (
          <>
            <label className="btn btn-sm btn-secondary">
              Upload Here
              <input
                className="visually-hidden"
                type="file"
                onChange={(e) => void onUpload(e.target.files)}
                disabled={busy}
              />
            </label>
            <button className="btn btn-sm btn-ghost" type="button" onClick={() => navigate(termHereHref)}>
              <Icon name="terminal" size={14} />
              Terminal Here
            </button>
          </>
        ) : null}
      </div>
      {canWrite ? (
        <div className="btn-row is-flush">
          <Field
            id="mkdir"
            className="field-inline"
            label="New folder"
            value={mkdirName}
            onChange={(e) => setMkdirName(e.target.value)}
          />
          <button className="btn btn-sm btn-primary" type="button" disabled={busy || !mkdirName} onClick={() => void onMkdir()}>
            <Icon name="create" size={14} />
            mkdir
          </button>
        </div>
      ) : null}
      <section className="section">
        <ResourceTable
          headers={["Name", "Type", "Size", <span key="act" className="visually-hidden">Actions</span>]}
          numeric={[2]}
          empty={<EmptyState title="This directory is empty." />}
          rows={entries.map((entry) => {
            const actions: { label: string; onClick: () => void; danger?: boolean }[] = [];
            if (entry.type !== "dir" && canDownload) {
              actions.push({ label: "Download", onClick: () => void onDownload(entry) });
            }
            if (canWrite) {
              actions.push({ label: "Delete", onClick: () => void onDelete(entry), danger: true });
            }
            return [
              entry.type === "dir" ? (
                <button
                  key={entry.name}
                  className="btn btn-ghost btn-sm"
                  type="button"
                  onClick={() => void reload(joinPath(path, entry.name))}
                >
                  {entry.name}
                </button>
              ) : (
                entry.name
              ),
              fileTypeLabel(entry.type),
              entry.type === "dir" ? "" : formatBytes(entry.size),
              <ActionMenu key="actions" items={actions} />,
            ];
          })}
        />
      </section>
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
