import { useEffect, useMemo, useRef, useState } from "react";
import type { FileContent, FileEntry } from "../api/phase6";
import { ActionMenu } from "./ActionMenu";
import { ConfirmDialog } from "./ConfirmDialog";
import { EmptyState, LoadingState } from "./EmptyState";
import { Field } from "./Field";
import { FileEditor } from "./FileEditor";
import { Icon } from "./Icon";
import { Link } from "./Link";
import { PageHeader } from "./PageHeader";
import { ResourceTable } from "./ResourceTable";
import { agentFilesBackend, gameFilesBackend, isArchive } from "../files/backend";
import { breadcrumbs, displayPath, destRel, joinPath, parentPath, relName } from "../files/paths";
import { formatBytes } from "../format";
import { workloadGuestIOReason } from "../guestIO";
import { fileTypeLabel } from "../labels";
import { navigate } from "../router";
import { useSession } from "../session";
import { itemAllows } from "../rbac";
import { mayOpenTerminal } from "../terminal/catalog";
import { ErrorNotice } from "./ErrorNotice";
import { UploadDialog } from "./UploadDialog";

type Dialog =
  | { kind: "mkdir" }
  | { kind: "create" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "move"; entries: FileEntry[] }
  | { kind: "copy"; entries: FileEntry[] }
  | { kind: "chmod"; entries: FileEntry[] }
  | { kind: "chown"; entries: FileEntry[] }
  | { kind: "delete"; entries: FileEntry[] }
  | { kind: "extract"; entry: FileEntry }
  | { kind: "zip"; entries: FileEntry[] };

// Folders first, then A to Z by name, ignoring case and reading numbers as numbers.
export function sortEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => {
    const dir = Number(b.type === "dir") - Number(a.type === "dir");
    return dir || a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" }) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
  });
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

export type FilesBrowserProps = {
  /** game is a game server's data volume, through the game file API. */
  kind: "node" | "workload" | "game";
  id: string;
  /** Directory to open first. */
  initialPath?: string;
  /** File in initialPath to open in the editor first. */
  initialEdit?: string;
  /** Called when the directory or the open file changes. */
  onLocation?: (path: string, editName: string) => void;
  /** Inside the Files workspace: no page heading, and keys act only while shown. */
  embedded?: boolean;
  active?: boolean;
  /** Where "Terminal Here" goes; defaults to the target's terminal page. */
  onTerminalHere?: (path: string) => void;
};

export function FilesBrowser({
  kind,
  id,
  initialPath = "/",
  initialEdit = "",
  onLocation,
  embedded = false,
  active = true,
  onTerminalHere,
}: FilesBrowserProps) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const roles = user?.roles;
  const host = kind === "node";
  const game = kind === "game";
  // A files grant on this one workload or host opens the matching actions.
  const grantOn = (perm: string) => !game && itemAllows(user, host ? "node" : "workload", id, perm);
  const backend = useMemo(() => (kind === "game" ? gameFilesBackend(id) : agentFilesBackend(kind, id)), [kind, id]);
  // Game servers check per-server grants on the server, so the browser offers every action.
  const roleWrite = game || (host ? Boolean(roles?.includes("superadmin")) : Boolean(roles?.includes("superadmin") || roles?.includes("admin")));
  const canUpload = roleWrite || grantOn("files.upload");
  const canCreate = roleWrite || grantOn("files.create");
  const canWrite = roleWrite || grantOn("files.modify");
  const canDelete = roleWrite || grantOn("files.delete");
  const canDownload = roleWrite || grantOn("files.download");
  const canTerminal = game || mayOpenTerminal(host ? "node" : "workload", id, roles, user);
  const canRead =
    game ||
    grantOn("files.read") ||
    (host ? Boolean(roles?.includes("superadmin")) : Boolean(roles?.includes("superadmin") || roles?.includes("admin") || roles?.includes("user")));
  const [path, setPath] = useState(initialPath || "/");
  const [pathInput, setPathInput] = useState(displayPath(initialPath || "/"));
  useEffect(() => {
    setPathInput(displayPath(path));
  }, [path]);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [listing, setListing] = useState(true);
  const [selected, setSelected] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ioReady, setIoReady] = useState(kind !== "workload");
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
    onLocation?.(nextPath, editName);
  }

  async function reload(next = path) {
    setListing(true);
    try {
      setEntries(sortEntries(await backend.list(next)));
      setPath(next);
      setSelected([]);
      syncUrl(next, editor ? (editor.name ?? "") : "");
    } finally {
      setListing(false);
    }
  }

  useEffect(() => {
    if (!canRead || unsupported || !ioReady) {
      return;
    }
    let cancelled = false;
    void reload(path)
      .then(async () => {
        const edit = initialEdit;
        if (!edit || cancelled) {
          return;
        }
        const listed = await backend.list(path);
        const hit = listed.find((e) => e.name === edit && e.type !== "dir");
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
  }, [canRead, unsupported, ioReady, backend]);

  const selectedEntries = useMemo(
    () => entries.filter((e) => selected.includes(e.name)),
    [entries, selected],
  );

  async function openEditor(entry: FileEntry) {
    setError(null);
    try {
      const content = await backend.read(joinPath(path, entry.name));
      setEditor(content);
      syncUrl(path, entry.name);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Open failed");
    }
  }

  const [uploadOpen, setUploadOpen] = useState(false);

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
        await backend.upload(joinPath(path, relName(file.name)), file);
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
        await backend.mkdir(joinPath(path, relName(dialogValue)));
      } else if (dialog.kind === "create") {
        const name = relName(dialogValue);
        const dest = joinPath(path, name);
        await backend.upload(dest, new File([""], name, { type: "text/plain" }));
        setDialog(null);
        setDialogValue("");
        await reload(path);
        await openEditor({ name, type: "file", size: 0, path: dest });
        return;
      } else if (dialog.kind === "rename") {
        await backend.move(joinPath(path, dialog.entry.name), joinPath(path, relName(dialogValue)));
      } else if (dialog.kind === "move") {
        for (const entry of dialog.entries) {
          await backend.move(joinPath(path, entry.name), joinPath(destRel(dialogValue.trim() || path), entry.name));
        }
      } else if (dialog.kind === "copy" && backend.copy) {
        for (const entry of dialog.entries) {
          await backend.copy(joinPath(path, entry.name), joinPath(destRel(dialogValue.trim() || path), entry.name));
        }
      } else if (dialog.kind === "chmod" && backend.chmod) {
        const mode = Number.parseInt(dialogValue.trim(), 8);
        if (!Number.isFinite(mode)) {
          throw new Error("Mode must be octal, for example 644");
        }
        for (const entry of dialog.entries) {
          await backend.chmod(joinPath(path, entry.name), mode);
        }
      } else if (dialog.kind === "chown" && backend.chown) {
        const [uidRaw, gidRaw] = dialogValue.trim().split(":");
        const uid = Number.parseInt(uidRaw ?? "", 10);
        const gid = Number.parseInt(gidRaw ?? uidRaw ?? "", 10);
        if (!Number.isFinite(uid) || !Number.isFinite(gid)) {
          throw new Error("Owner must be uid:gid");
        }
        for (const entry of dialog.entries) {
          await backend.chown(joinPath(path, entry.name), uid, gid);
        }
      } else if (dialog.kind === "delete") {
        for (const entry of dialog.entries) {
          await backend.remove(joinPath(path, entry.name), entry.mtime);
        }
      } else if (dialog.kind === "extract" && backend.extract) {
        const dest = destRel(dialogValue.trim() || path);
        await backend.extract(joinPath(path, dialog.entry.name), dest);
        setUploadNote(`Extracted ${dialog.entry.name} into ${displayPath(dest)}.`);
      } else if (dialog.kind === "zip" && backend.compress) {
        let name = relName(dialogValue.trim() || "archive.zip");
        if (!/\.zip$/i.test(name)) {
          name += ".zip";
        }
        const made = await backend.compress(
          dialog.entries.map((e) => joinPath(path, e.name)),
          joinPath(path, name),
        );
        setSelected([]);
        setUploadNote(`Created ${displayPath(made)}.`);
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
      if (!active) {
        return;
      }
      const tag = (ev.target as HTMLElement | null)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
        return;
      }
      if (ev.key === "Delete" && canDelete && selectedEntries.length && !dialog && !editor) {
        ev.preventDefault();
        setDialog({ kind: "delete", entries: selectedEntries });
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [active, canDelete, selectedEntries, dialog, editor]);

  if (unsupported) {
    if (embedded) {
      return (
        <p className="banner banner-warn" role="status">
          {unsupported}
        </p>
      );
    }
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
    const msg = host ? "Host files require admin." : "Files require at least viewer.";
    if (embedded) {
      return (
        <p className="banner banner-error" role="alert">
          {msg}
        </p>
      );
    }
    return (
      <section className="page" aria-labelledby="files-heading">
        <PageHeader id="files-heading" title="Files" />
        <p className="banner banner-error" role="alert">
          {msg}
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
        items.push({ label: "Download", onClick: () => void backend.download(joinPath(path, entry.name), entry.name).catch((err) => setError(err instanceof Error ? err.message : "download failed")) });
      }
    }
    if (canWrite) {
      items.push({ label: "Rename", onClick: () => { setDialogValue(entry.name); setDialog({ kind: "rename", entry }); } });
      items.push({ label: "Move", onClick: () => { setDialogValue(path); setDialog({ kind: "move", entries: [entry] }); } });
      if (backend.copy) {
        items.push({ label: "Copy", onClick: () => { setDialogValue(path); setDialog({ kind: "copy", entries: [entry] }); } });
      }
      if (backend.chmod && roleWrite) {
        items.push({ label: "Permissions", onClick: () => { setDialogValue(formatMode(entry.mode) || "644"); setDialog({ kind: "chmod", entries: [entry] }); } });
      }
      if (backend.chown && roleWrite) {
        items.push({ label: "Owner", onClick: () => { setDialogValue(entry.uid != null && entry.gid != null ? `${entry.uid}:${entry.gid}` : "0:0"); setDialog({ kind: "chown", entries: [entry] }); } });
      }
      if (backend.extract && entry.type !== "dir" && isArchive(entry.name)) {
        items.push({ label: "Extract", onClick: () => { setDialogValue(path); setDialog({ kind: "extract", entry }); } });
      }
      if (backend.compress) {
        items.push({ label: "Zip", onClick: () => { setDialogValue(`${entry.name}.zip`); setDialog({ kind: "zip", entries: [entry] }); } });
      }
    }
    if (canDelete) {
      items.push({ label: "Delete", onClick: () => setDialog({ kind: "delete", entries: [entry] }), danger: true });
    }
    return items;
  }

  const uploadDialog = (
    <UploadDialog
      open={uploadOpen}
      destination={displayPath(path)}
      onClose={() => setUploadOpen(false)}
      upload={(file) => backend.upload(joinPath(path, relName(file.name)), file)}
      onDone={() => void reload(path)}
    />
  );

  const Wrap = embedded ? "div" : "section";
  return (
    <Wrap
      className={embedded ? "files-browser" : "page page-wide"}
      aria-labelledby={embedded ? undefined : "files-heading"}
      onDragOver={(e) => {
        e.preventDefault();
        setDropActive(true);
      }}
      onDragLeave={() => setDropActive(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDropActive(false);
        if (canUpload) {
          void onUploadList(e.dataTransfer.files);
        }
      }}
    >
      {uploadDialog}
      {embedded ? null : <PageHeader id="files-heading" title="Files" kicker={displayPath(path)} />}
      {error ? (
        <ErrorNotice error={error} />
      ) : null}
      {uploadNote ? (
        <p className="banner" role="status">
          {uploadNote}
        </p>
      ) : null}
      {dropActive && canUpload ? (
        <p className="banner" role="status">
          Drop files to upload into this directory
        </p>
      ) : null}
      {host && !embedded ? (
        <nav className="subnav" aria-label="IO">
          <Link href={summaryHref}>Summary</Link>
          <Link href={termHref}>Terminal</Link>
          <Link href={`/nodes/${id}/files`} aria-current="page">
            Files
          </Link>
        </nav>
      ) : null}
      <form
        className="files-path-form"
        onSubmit={(e) => {
          e.preventDefault();
          const next = pathInput.trim().replace(/\/+$/, "") || "/";
          setError(null);
          void reload(next).catch((err) => setError(err instanceof Error ? err.message : "Could not open that path"));
        }}
      >
        <input
          className="field-input files-path-input"
          aria-label="Go to path"
          value={pathInput}
          onChange={(e) => setPathInput(e.target.value)}
          onFocus={(e) => e.target.select()}
          spellCheck={false}
          autoComplete="off"
        />
        <button className="btn btn-sm btn-secondary" type="submit">
          Go
        </button>
      </form>
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
        {canUpload ? (
          <>
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => setUploadOpen(true)} disabled={busy}>
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
          </>
        ) : null}
        {canCreate ? (
          <>
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(""); setDialog({ kind: "create" }); }}>
              New file
            </button>
            <button className="btn btn-sm btn-primary" type="button" onClick={() => { setDialogValue(""); setDialog({ kind: "mkdir" }); }}>
              <Icon name="create" size={14} />
              New folder
            </button>
          </>
        ) : null}
        {canTerminal ? (
          <>
            {!game || onTerminalHere ? (
              <button className="btn btn-sm btn-ghost" type="button" onClick={() => (onTerminalHere ? onTerminalHere(path) : navigate(termHereHref))}>
                <Icon name="terminal" size={14} />
                Terminal Here
              </button>
            ) : null}
          </>
        ) : null}
      </div>
      {(canWrite || canDelete) && selectedEntries.length > 0 ? (
        <div className="btn-row is-flush">
          {canWrite ? (
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(path); setDialog({ kind: "move", entries: selectedEntries }); }}>
            Move selected
          </button>
          ) : null}
          {canWrite && backend.copy ? (
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(path); setDialog({ kind: "copy", entries: selectedEntries }); }}>
              Copy selected
            </button>
          ) : null}
          {canWrite && backend.compress ? (
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => { setDialogValue(selectedEntries.length === 1 ? `${selectedEntries[0].name}.zip` : "archive.zip"); setDialog({ kind: "zip", entries: selectedEntries }); }}>
              Zip selected
            </button>
          ) : null}
          {canDelete ? (
          <button className="btn btn-sm btn-danger" type="button" onClick={() => setDialog({ kind: "delete", entries: selectedEntries })}>
            Delete selected
          </button>
          ) : null}
        </div>
      ) : null}
      {editor ? (
        <FileEditor
          backend={backend}
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
          empty={listing ? <LoadingState label="Loading files" /> : <EmptyState title="This directory is empty." />}
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
        confirmLabel={dialog?.kind === "delete" ? "Delete" : dialog?.kind === "extract" ? "Extract" : dialog?.kind === "zip" ? "Zip" : "Confirm"}
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
    </Wrap>
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
    case "extract":
      return `Extract ${dialog.entry.name}`;
    case "zip":
      return dialog.entries.length === 1 ? `Zip ${dialog.entries[0].name}` : `Zip ${dialog.entries.length} items`;
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
    case "extract":
      return "Extract into directory";
    case "zip":
      return "Archive name (saved in this folder)";
    default:
      return "Value";
  }
}
