import Editor, { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import { useEffect, useMemo, useState } from "react";
import { ApiError, downloadFile, uploadFile } from "../api/client";
import type { FileContent } from "../api/phase6";
import { languageFromName } from "../files/language";
import { joinPath, parentPath, relName } from "../files/paths";
import { ConfirmDialog } from "./ConfirmDialog";
import { Field } from "./Field";

loader.config({ monaco });

let themeReady = false;
function ensureTheme() {
  if (themeReady) {
    return;
  }
  monaco.editor.defineTheme("ndl-dark", {
    base: "vs-dark",
    inherit: true,
    rules: [],
    colors: {
      "editor.background": "#1b1f2a",
      "editor.foreground": "#e8ecf4",
      "editorLineNumber.foreground": "#7b8494",
      "editorCursor.foreground": "#c6e86a",
      "editor.selectionBackground": "#2f3a28",
    },
  });
  themeReady = true;
}

export function FileEditor({
  kind,
  id,
  dir,
  file,
  canWrite,
  onClose,
  onSaved,
  onError,
}: {
  kind: "node" | "workload";
  id: string;
  dir: string;
  file: FileContent;
  canWrite: boolean;
  onClose: () => void;
  onSaved: (path: string) => void;
  onError: (message: string) => void;
}) {
  const [value, setValue] = useState(file.content ?? "");
  const [saveAsOpen, setSaveAsOpen] = useState(false);
  const [saveAsName, setSaveAsName] = useState(file.name);
  const [busy, setBusy] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [overwriteOpen, setOverwriteOpen] = useState(false);
  const [pendingDest, setPendingDest] = useState("");
  const original = file.content ?? "";
  const dirty = value !== original;
  const readOnly = !canWrite || !file.editable;
  const language = useMemo(() => languageFromName(file.name), [file.name]);
  const originalPath = file.path || joinPath(dir, file.name);

  useEffect(() => {
    ensureTheme();
  }, []);

  useEffect(() => {
    function onKey(ev: KeyboardEvent) {
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "s") {
        ev.preventDefault();
        if (!readOnly && dirty) {
          void save(originalPath);
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  useEffect(() => {
    function onBefore(ev: BeforeUnloadEvent) {
      if (!dirty) {
        return;
      }
      ev.preventDefault();
      ev.returnValue = "";
    }
    window.addEventListener("beforeunload", onBefore);
    return () => window.removeEventListener("beforeunload", onBefore);
  }, [dirty]);

  async function save(dest: string, overwrite = false) {
    setBusy(true);
    try {
      const blob = new File([value], dest.split("/").pop() || file.name, { type: "text/plain" });
      await uploadFile(kind, id, dest, blob, {
        expectedMtime: overwrite || dest !== originalPath ? undefined : file.mtime,
      });
      setOverwriteOpen(false);
      onSaved(dest);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && !overwrite) {
        setPendingDest(dest);
        setOverwriteOpen(true);
        return;
      }
      onError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  function requestClose() {
    if (dirty) {
      setDiscardOpen(true);
      return;
    }
    onClose();
  }

  if (file.binary) {
    return (
      <section className="files-editor" aria-label="Binary file">
        <header className="files-editor-bar">
          <strong>{file.name}</strong>
          <span className="muted">Binary file</span>
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => void downloadFile(kind, id, file.path || joinPath(dir, file.name), file.name)}>
            Download
          </button>
          <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <p className="banner banner-warn" role="status">
          This file looks binary. Download it instead of opening it as text.
        </p>
      </section>
    );
  }

  if (file.too_large) {
    return (
      <section className="files-editor" aria-label="Large file">
        <header className="files-editor-bar">
          <strong>{file.name}</strong>
          <span className="muted">Too large for the editor</span>
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => void downloadFile(kind, id, file.path || joinPath(dir, file.name), file.name)}>
            Download
          </button>
          <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <p className="banner banner-warn" role="status">
          Files larger than 2 MiB stay download-only so the browser stays responsive.
        </p>
      </section>
    );
  }

  return (
    <section className="files-editor" aria-label="File editor">
      <header className="files-editor-bar">
        <strong>
          {file.name}
          {dirty ? " *" : ""}
        </strong>
        <span className="muted">{language}</span>
        {readOnly ? <span className="muted">Read-only</span> : null}
        {canWrite && file.editable ? (
          <>
            <button className="btn btn-sm btn-primary" type="button" disabled={busy || !dirty} onClick={() => void save(originalPath)}>
              Save
            </button>
            <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => setSaveAsOpen(true)}>
              Save As
            </button>
          </>
        ) : null}
        <button className="btn btn-sm btn-ghost" type="button" onClick={requestClose}>
          Close
        </button>
      </header>
      <Editor
        height="28rem"
        theme="ndl-dark"
        language={language}
        value={value}
        onChange={(next) => setValue(next ?? "")}
        options={{
          readOnly,
          minimap: { enabled: false },
          fontSize: 13,
          lineNumbers: "on",
          automaticLayout: true,
          matchBrackets: "always",
          autoIndent: "full",
          insertSpaces: true,
          tabSize: 2,
          wordWrap: "on",
          scrollBeyondLastLine: false,
        }}
      />
      <ConfirmDialog
        open={saveAsOpen}
        title="Save As"
        confirmLabel="Save"
        onClose={() => setSaveAsOpen(false)}
        onConfirm={() => {
          try {
            const dest = joinPath(parentPath(originalPath), relName(saveAsName));
            setSaveAsOpen(false);
            void save(dest);
          } catch (err) {
            onError(err instanceof Error ? err.message : "Name is invalid");
          }
        }}
      >
        <Field id="save-as-name" label="New name" value={saveAsName} onChange={(e) => setSaveAsName(e.target.value)} />
      </ConfirmDialog>
      <ConfirmDialog
        open={discardOpen}
        title="Unsaved changes"
        danger
        confirmLabel="Discard"
        onClose={() => setDiscardOpen(false)}
        onConfirm={() => {
          setDiscardOpen(false);
          onClose();
        }}
      >
        <p>Discard unsaved changes to {file.name}?</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={overwriteOpen}
        title="File changed on disk"
        danger
        confirmLabel="Overwrite"
        onClose={() => setOverwriteOpen(false)}
        onConfirm={() => {
          setOverwriteOpen(false);
          void save(pendingDest || originalPath, true);
        }}
      >
        <p>This file changed on disk. Overwrite it with the editor contents?</p>
      </ConfirmDialog>
    </section>
  );
}
