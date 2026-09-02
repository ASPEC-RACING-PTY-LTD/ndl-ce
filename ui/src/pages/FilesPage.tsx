import { useEffect, useMemo, useRef, useState } from "react";
import {
  chmodFile,
  chownFile,
  copyFile,
  deleteFile,
  downloadFile,
  listFiles,
  mkdirFile,
  moveFile,
  readFileContent,
  uploadFile,
} from "../api/client";
import type { FileContent, FileEntry } from "../api/phase6";
import { ActionMenu } from "../components/ActionMenu";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { FileEditor } from "../components/FileEditor";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { breadcrumbs, displayPath, joinPath, parentPath, relName } from "../files/paths";
import { formatBytes } from "../format";
import { workloadGuestIOReason } from "../guestIO";
import { fileTypeLabel } from "../labels";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";

type Dialog =
  | { kind: "mkdir" }
  | { kind: "create" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "move"; entries: FileEntry[] }
  | { kind: "copy"; entries: FileEntry[] }
  | { kind: "chmod"; entries: FileEntry[] }
  | { kind: "chown"; entries: FileEntry[] }
  | { kind: "delete"; entries: FileEntry[] };

function idsFromPath(): { kind: "node" | "workload"; id: string } {
  const parts = currentPath().split("/").filter(Boolean);
  if (parts[0] === "nodes") {
    return { kind: "node", id: parts[1] ?? "" };
  }
  return { kind: "workload", id: parts[1] ?? "" };
}

function pathFromQuery(): string {
  return new URLSearchParams(window.location.search).get("path") || "/";
}

function editFromQuery(): string {
  return new URLSearchParams(window.location.search).get("edit") || "";
}

function formatMode(mode?: number): string {
  if (mode == null) {
    return "";
  }
  return mode.toString(8).padStart(3, "0");
}

function ownerLabel(entry: FileEntry): string {
  const user = entry.owner || (entry.uid != null ? String(entry.uid) : "");
  const group = entry.group || (entry.gid != null ? String(entry.gid) : "");
  if (!user && !group) {
    return "";
  }
  return group ? `${user}:${group}` : user;
}

export function FilesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const { kind, id } = idsFromPath();
  const host = kind === "node";
  const canWrite = host ? Boolean(roles?.includes("admin")) : Boolean(roles?.includes("admin") || roles?.includes("operator"));
  const canDownload = host ? Boolean(roles?.includes("admin")) : Boolean(roles?.includes("admin") || roles?.includes("operator"));
  const canRead = host
    ? roles?.includes("admin")
    : Boolean(roles?.includes("admin") || roles?.includes("operator") || roles?.includes("viewer"));
  const [path, setPath] = useState(pathFromQuery);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ioReady, setIoReady] = useState(kind === "node");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [dialogValue, setDialogValue] = useState("");
  const [editor, setEditor] = useState<FileContent | null>(null);
  const [dropActive, setDropActive] = useState(false);
  const [uploadNote, setUploadNote] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

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

  function syncUrl(nextPath: string, editName = "") {
    const u = new URL(window.location.href);
    u.searchParams.set("path", nextPath);
    if (editName) {
      u.searchParams.set("edit", editName);
    } else {
      u.searchParams.delete("edit");
    }
    window.history.replaceState({}, "", `${u.pathname}${u.search}`);
  }

  async function reload(next = path) {
    const listed = await listFiles(kind, id, next);
    setEntries(listed.entries ?? []);
    setPath(next);
    setSelected([]);
    syncUrl(next, editor ? (editor.name ?? "") : "");
  }

  useEffect(() => {
    if (!canRead || unsupported || !ioReady) {
      return;
    }
    let cancelled = false;
    void reload(path)
      .then(async () => {
        const edit = editFromQuery();
        if (!edit || cancelled) {
          return;
        }
        const listed = await listFiles(kind, id, path);
        const hit = listed.entries?.find((e) => e.name === edit && e.type !== "dir");
        if (hit) {
          await openEditor(hit);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canRead, unsupported, ioReady, kind, id]);

  const selectedEntries = useMemo(
    () => entries.filter((e) => selected.includes(e.name)),
    [entries, selected],
  );

  async function openEditor(entry: FileEntry) {
    setError(null);
    try {
      const content = await readFileContent(kind, id, joinPath(path, entry.name));
      setEditor(content);
      syncUrl(path, entry.name);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Open failed");
    }
  }

  async function onUploadList(list: FileList | File[] | null) {
    const files = list ? Array.from(list) : [];
    if (files.length === 0) {
      return;
    }
    setBusy(true);
    setError(null);
    setUploadNote(`Uploading 0/${files.length}`);
    try {
      for (let i = 0; i < files.length; i += 1) {
        const file = files[i];
        setUploadNote(`Uploading ${i + 1}/${files.length}: ${file.name}`);
        await uploadFile(kind, id, joinPath(path, file.name), file);
      }
      setUploadNote(null);
      await reload(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed");
      setUploadNote(null);
    } finally {
      setBusy(false);
    }
  }

  async function runDialog() {
    if (!dialog) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (dialog.kind === "mkdir") {
        await mkdirFile(kind, id, joinPath(path, relName(dialogValue)));
      } else if (dialog.kind === "create") {
        const name = relName(dialogValue);
        const dest = joinPath(path, name);
        await uploadFile(kind, id, dest, new File([""], name, { type: "text/plain" }));
        setDialog(null);
        setDialogValue("");
        await reload(path);
        await openEditor({ name, type: "file", size: 0, path: dest });
        return;
      } else if (dialog.kind === "rename") {
        await moveFile(kind, id, joinPath(path, dialog.entry.name), joinPath(path, relName(dialogValue)));
      } else if (dialog.kind === "move") {
        for (const entry of dialog.entries) {
          await moveFile(kind, id, joinPath(path, entry.name), joinPath(dialogValue.trim() || path, entry.name));
        }
      } else if (dialog.kind === "copy") {
        for (const entry of dialog.entries) {
          await copyFile(kind, id, joinPath(path, entry.name), joinPath(dialogValue.trim() || path, entry.name));
        }
      } else if (dialog.kind === "chmod") {
        const mode = Number.parseInt(dialogValue.trim(), 8);
        if (!Number.isFinite(mode)) {
          throw new Error("Mode must be octal, for example 644");
        }
        for (const entry of dialog.entries) {
          await chmodFile(kind, id, joinPath(path, entry.name), mode);
        }
      } else if (dialog.kind === "chown") {
        const [uidRaw, gidRaw] = dialogValue.trim().split(":");
        const uid = Number.parseInt(uidRaw ?? "", 10);
        const gid = Number.parseInt(gidRaw ?? uidRaw ?? "", 10);
        if (!Number.isFinite(uid) || !Number.isFinite(gid)) {
          throw new Error("Owner must be uid:gid");
        }
        for (const entry of dialog.entries) {
          await chownFile(kind, id, joinPath(path, entry.name), uid, gid);
        }
      } else if (dialog.kind === "delete") {
        for (const entry of dialog.entries) {
          await deleteFile(kind, id, joinPath(path, entry.name), entry.mtime);
        }
      }
      setDialog(null);
      setDialogValue("");
      await reload(path);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Operation failed");
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    function onKey(ev: KeyboardEvent) {
      if (ev.key === "Delete" && canWrite && selectedEntries.length && !dialog && !editor) {
        ev.preventDefault();
        setDialog({ kind: "delete", entries: selectedEntries });
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [canWrite, selectedEntries, dialog, editor]);

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
  const summaryHref = host ? `/nodes/${id}` : `/workloads/${id}`;
  const crumbs = breadcrumbs(displayPath(path));
  const deleteDirs = dialog?.kind === "delete" ? dialog.entries.filter((e) => e.type === "dir") : [];

  function toggle(name: string) {
    setSelected((cur) => (cur.includes(name) ? cur.filter((n) => n !== name) : [...cur, name]));
  }

  function entryActions(entry: FileEntry) {
    const items: { label: string; onClick: () => void; danger?: boolean }[] = [];
    if (entry.type !== "dir") {
      items.push({ label: "Open", onClick: () => void openEditor(entry) });
      if (canDownload) {
        items.push({ label: "Download", onClick: () => void downloadFile(kind, id, joinPath(path, entry.name), entry.name).catch((err) => setError(err instanceof Error ? err.message : "download failed")) });
      }
    }
    if (canWrite) {
      items.push({ label: "Rename", onClick: () => { setDialogValue(entry.name); setDialog({ kind: "rename", entry }); } });
      items.push({ label: "Move", onClick: () => { setDialogValue(path); setDialog({ kind: "move", entries: [entry] }); } });
      items.push({ label: "Copy", onClick: () => { setDialogValue(path); setDialog({ kind: "copy", entries: [entry] }); } });
      items.push({ label: "Permissions", onClick: () => { setDialogValue(formatMode(entry.mode) || "644"); setDialog({ kind: "chmod", entries: [entry] }); } });
      items.push({ label: "Owner", onClick: () => { setDialogValue(entry.uid != null && entry.gid != null ? `${entry.uid}:${entry.gid}` : "0:0"); setDialog({ kind: "chown", entries: [entry] }); } });
      items.push({ label: "Delete", onClick: () => setDialog({ kind: "delete", entries: [entry] }), danger: true });
    }
    return items;
  }

  return (
    <section
      className="page page-wide"
      aria-labelledby="files-heading"
      onDragOver={(e) => {
        e.preventDefault();
        setDropActive(true);
      }}
      onDragLeave={() => setDropActive(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDropActive(false);
        if (canWrite) {
          void onUploadList(e.dataTransfer.files);
        }
      }}
    >
      <PageHeader id="files-heading" title="Files" kicker={displayPath(path)} />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {uploadNote ? (
        <p className="banner" role="status">
          {uploadNote}
        </p>
      ) : null}
      {dropActive && canWrite ? (
        <p className="banner" role="status">
          Drop files to upload into this directory
        </p>
      ) : null}
      <nav className="subnav" aria-label="IO">
        <Link href={summaryHref}>Summary</Link>
        <Link href={termHref}>Terminal</Link>
        <Link href={host ? `/nodes/${id}/files` : `/workloads/${id}/files`} aria-current="page">
          Files
        </Link>
      </nav>
      <nav className="files-crumbs" aria-label="Path">
        {crumbs.map((c) => (
          <button key={c.path} className="btn btn-ghost btn-sm" type="button" onClick={() => void reload(c.path)}>
            {c.label}
          </button>
        ))}
      </nav>
      <div className="btn-row is-flush files-toolbar">
        <button className="btn btn-sm btn-secondary" type="button" onClick={() => void reload(parentPath(path))}>
          Up
        </button>
        <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void reload(path)}>
          Refresh
        </button>
        {canWrite ? (
          <>
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => fileInput.current?.click()} disabled={busy}>
              Upload
            </button>
            <input
              ref={fileInput}
              className="visually-hidden"
              type="file"
              multiple
              aria-label="Upload files"
              onChange={(e) => {
                void onUploadList(e.target.files);
                e.target.value = "";
              }}
              disabled={busy}
            />
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(""); setDialog({ kind: "create" }); }}>
              New file
            </button>
            <button className="btn btn-sm btn-primary" type="button" onClick={() => { setDialogValue(""); setDialog({ kind: "mkdir" }); }}>
              <Icon name="create" size={14} />
              New folder
            </button>
            <button className="btn btn-sm btn-ghost" type="button" onClick={() => navigate(termHereHref)}>
              <Icon name="terminal" size={14} />
              Terminal Here
            </button>
          </>
        ) : null}
      </div>
      {canWrite && selectedEntries.length > 0 ? (
        <div className="btn-row is-flush">
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(path); setDialog({ kind: "move", entries: selectedEntries }); }}>
            Move selected
          </button>
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(path); setDialog({ kind: "copy", entries: selectedEntries }); }}>
            Copy selected
          </button>
          <button className="btn btn-sm btn-danger" type="button" onClick={() => setDialog({ kind: "delete", entries: selectedEntries })}>
            Delete selected
          </button>
        </div>
      ) : null}
      {editor ? (
        <FileEditor
          kind={kind}
          id={id}
          dir={path}
          file={editor}
          canWrite={canWrite}
          onClose={() => {
            setEditor(null);
            syncUrl(path);
          }}
          onSaved={() => {
            setEditor(null);
            void reload(path);
          }}
          onError={setError}
        />
      ) : null}
      <section className="section">
        <ResourceTable
          headers={[
            <input
              key="all"
              type="checkbox"
              aria-label="Select all"
              checked={entries.length > 0 && selected.length === entries.length}
              onChange={(e) => setSelected(e.target.checked ? entries.map((x) => x.name) : [])}
            />,
            "Name",
            "Type",
            "Size",
            "Modified",
            "Owner",
            "Mode",
            <span key="act" className="visually-hidden">
              Actions
            </span>,
          ]}
          numeric={[3]}
          empty={<EmptyState title="This directory is empty." />}
          rows={entries.map((entry) => [
            <input
              key={`sel-${entry.name}`}
              type="checkbox"
              aria-label={`Select ${entry.name}`}
              checked={selected.includes(entry.name)}
              onChange={() => toggle(entry.name)}
            />,
            <button
              key={entry.name}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => {
                if (entry.type === "dir") {
                  void reload(joinPath(path, entry.name));
                  return;
                }
                void openEditor(entry);
              }}
              onDoubleClick={() => {
                if (entry.type === "dir") {
                  void reload(joinPath(path, entry.name));
                  return;
                }
                void openEditor(entry);
              }}
            >
              {entry.name}
            </button>,
            fileTypeLabel(entry.type),
            entry.type === "dir" ? "" : formatBytes(entry.size),
            entry.mtime ?? "",
            ownerLabel(entry),
            formatMode(entry.mode),
            <ActionMenu key={`act-${entry.name}`} items={entryActions(entry)} />,
          ])}
        />
      </section>
      <ConfirmDialog
        open={dialog != null}
        title={dialogTitle(dialog)}
        danger={dialog?.kind === "delete"}
        confirmLabel={dialog?.kind === "delete" ? "Delete" : "Confirm"}
        onClose={() => setDialog(null)}
        onConfirm={() => void runDialog()}
      >
        {dialog?.kind === "delete" ? (
          <p>
            {deleteDirs.length
              ? `Delete ${dialog.entries.length} item(s), including ${deleteDirs.length} director(ies) and everything inside them? This cannot be undone.`
              : `Delete ${dialog.entries.map((e) => e.name).join(", ")}?`}
          </p>
        ) : (
          <Field
            id="files-dialog"
            label={dialogFieldLabel(dialog)}
            value={dialogValue}
            onChange={(e) => setDialogValue(e.target.value)}
          />
        )}
      </ConfirmDialog>
    </section>
  );
}

function dialogTitle(dialog: Dialog | null): string {
  switch (dialog?.kind) {
    case "mkdir":
      return "New folder";
    case "create":
      return "New file";
    case "rename":
      return "Rename";
    case "move":
      return "Move";
    case "copy":
      return "Copy";
    case "chmod":
      return "Permissions";
    case "chown":
      return "Owner";
    case "delete":
      return "Delete";
    default:
      return "Files";
  }
}

function dialogFieldLabel(dialog: Dialog | null): string {
  switch (dialog?.kind) {
    case "mkdir":
    case "create":
    case "rename":
      return "Name";
    case "move":
    case "copy":
      return "Destination directory";
    case "chmod":
      return "Mode (octal)";
    case "chown":
      return "uid:gid";
    default:
      return "Value";
  }
}
