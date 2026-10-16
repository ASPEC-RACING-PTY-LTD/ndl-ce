import { confirmAction } from "../confirm";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { deleteSavedScript, listSavedScripts, saveScript, type SavedScript } from "../api/client";
import { Dialog } from "../ui/Dialog";
import { ErrorNotice } from "./ErrorNotice";

function parse(text: string): { value?: unknown; error?: string } {
  if (!text.trim()) {
    return { error: "Write some JSON first." };
  }
  try {
    return { value: JSON.parse(text) };
  } catch (err) {
    return { error: err instanceof Error ? err.message : "Invalid JSON" };
  }
}

/**
 * JsonScriptModal edits a JSON script with Test (no changes made) and Run,
 * and keeps scripts by name so they can be loaded again from the list.
 */
export function JsonScriptModal({
  open,
  onClose,
  title,
  kind,
  template,
  help,
  onTest,
  onRun,
  runLabel = "Run",
  canRun = true,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  kind: "docker" | "uptime";
  template: string;
  help?: ReactNode;
  onTest: (value: unknown) => Promise<ReactNode>;
  onRun: (value: unknown) => Promise<ReactNode | void>;
  runLabel?: string;
  canRun?: boolean;
}) {
  const [text, setText] = useState(template);
  const [saved, setSaved] = useState<SavedScript[]>([]);
  const [selected, setSelected] = useState("");
  const [result, setResult] = useState<ReactNode>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"" | "test" | "run" | "save">("");
  const parsed = useMemo(() => parse(text), [text]);

  async function loadSaved() {
    try {
      setSaved((await listSavedScripts(kind)).items ?? []);
    } catch {
      setSaved([]);
    }
  }

  useEffect(() => {
    if (open) {
      setResult(null);
      setError(null);
      void loadSaved();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, kind]);

  async function act(which: "test" | "run") {
    if (parsed.error) {
      setError(`The JSON is not valid: ${parsed.error}`);
      return;
    }
    setBusy(which);
    setError(null);
    setResult(null);
    try {
      const out = which === "test" ? await onTest(parsed.value) : await onRun(parsed.value);
      setResult(out ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : `${which} failed`);
    } finally {
      setBusy("");
    }
  }

  async function save(asNew: boolean) {
    if (parsed.error) {
      setError(`The JSON is not valid: ${parsed.error}`);
      return;
    }
    const current = saved.find((s) => s.id === selected);
    let name = current?.name ?? "";
    if (asNew || !current) {
      const typed = window.prompt("Save this script as", current ? `${current.name} copy` : "");
      if (!typed?.trim()) {
        return;
      }
      name = typed.trim();
    }
    setBusy("save");
    setError(null);
    try {
      const row = await saveScript({ id: asNew ? undefined : current?.id, kind, name, body: text });
      await loadSaved();
      setSelected(row.id);
      setResult(<p className="banner banner-ok">Saved as {row.name}.</p>);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save");
    } finally {
      setBusy("");
    }
  }

  async function removeSaved() {
    const current = saved.find((s) => s.id === selected);
    if (!current || !confirmAction(`Delete the saved script ${current.name}?`)) {
      return;
    }
    try {
      await deleteSavedScript(current.id);
      setSelected("");
      await loadSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete");
    }
  }

  return (
    <Dialog
      open={open}
      title={title}
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
          <button className="btn btn-secondary" type="button" disabled={busy !== ""} onClick={() => void act("test")}>
            {busy === "test" ? "Testing" : "Test"}
          </button>
          {canRun ? (
            <button className="btn btn-primary" type="button" disabled={busy !== "" || Boolean(parsed.error)} onClick={() => void act("run")}>
              {busy === "run" ? "Running" : runLabel}
            </button>
          ) : null}
        </div>
      }
    >
      <div className="json-modal">
        <div className="json-toolbar">
          <select
            className="field-input"
            aria-label="Saved scripts"
            value={selected}
            onChange={(e) => {
              const id = e.target.value;
              setSelected(id);
              const row = saved.find((s) => s.id === id);
              if (row) {
                try {
                  setText(JSON.stringify(JSON.parse(row.body), null, 2));
                } catch {
                  setText(row.body);
                }
                setResult(null);
              }
            }}
          >
            <option value="">{saved.length ? "Load a saved script" : "No saved scripts yet"}</option>
            {saved.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
          <button className="btn btn-sm btn-secondary" type="button" disabled={busy !== ""} onClick={() => void save(false)}>
            {selected ? "Save" : "Save as"}
          </button>
          {selected ? (
            <>
              <button className="btn btn-sm btn-ghost" type="button" disabled={busy !== ""} onClick={() => void save(true)}>
                Save as new
              </button>
              <button className="btn btn-sm btn-ghost btn-danger-text" type="button" onClick={() => void removeSaved()}>
                Delete saved
              </button>
            </>
          ) : null}
          <span className="json-spacer" />
          <button
            className="btn btn-sm btn-ghost"
            type="button"
            disabled={Boolean(parsed.error)}
            onClick={() => setText(JSON.stringify(parsed.value, null, 2))}
          >
            Format
          </button>
          <button
            className="btn btn-sm btn-ghost"
            type="button"
            onClick={() => {
              setSelected("");
              setText(template);
            }}
          >
            Example
          </button>
        </div>
        <textarea
          className="field-input code-input json-editor"
          aria-label="JSON"
          spellCheck={false}
          value={text}
          rows={16}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Tab") {
              e.preventDefault();
              const el = e.currentTarget;
              const start = el.selectionStart;
              const next = text.slice(0, start) + "  " + text.slice(el.selectionEnd);
              setText(next);
              requestAnimationFrame(() => {
                el.selectionStart = el.selectionEnd = start + 2;
              });
            }
          }}
        />
        <p className={"field-hint" + (parsed.error ? " json-invalid" : "")} role="status">
          {parsed.error ? `Not valid JSON yet: ${parsed.error}` : "Valid JSON. Test shows what would happen without changing anything."}
        </p>
        {error ? <ErrorNotice error={error} /> : null}
        {result}
        {help ? <details className="json-help">{help}</details> : null}
      </div>
    </Dialog>
  );
}
