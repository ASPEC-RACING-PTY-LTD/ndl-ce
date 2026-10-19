import { useEffect, useRef, useState } from "react";
import { Dialog } from "../ui/Dialog";
import { formatBytes } from "../format";
import { Icon } from "./Icon";

type Item = { file: File; status: "ready" | "uploading" | "done" | "failed"; error?: string };

/**
 * UploadDialog collects files by click or drag and drop, then uploads them
 * one at a time into the folder, showing each file's progress.
 */
export function UploadDialog({
  open,
  destination,
  onClose,
  upload,
  onDone,
}: {
  open: boolean;
  destination: string;
  onClose: () => void;
  upload: (file: File) => Promise<void>;
  onDone: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [items, setItems] = useState<Item[]>([]);
  const [over, setOver] = useState(false);
  const [running, setRunning] = useState(false);

  useEffect(() => {
    if (open) {
      setItems([]);
      setOver(false);
    }
  }, [open]);

  function add(list: FileList | null) {
    const files = list ? Array.from(list) : [];
    if (files.length === 0) {
      return;
    }
    setItems((cur) => {
      const seen = new Set(cur.map((i) => i.file.name));
      return [...cur, ...files.filter((f) => !seen.has(f.name)).map((file) => ({ file, status: "ready" as const }))];
    });
  }

  async function start() {
    setRunning(true);
    let ok = 0;
    for (let i = 0; i < items.length; i += 1) {
      if (items[i].status === "done") {
        continue;
      }
      setItems((cur) => cur.map((it, j) => (j === i ? { ...it, status: "uploading", error: undefined } : it)));
      try {
        await upload(items[i].file);
        ok += 1;
        setItems((cur) => cur.map((it, j) => (j === i ? { ...it, status: "done" } : it)));
      } catch (err) {
        const msg = err instanceof Error ? err.message : "Upload failed";
        setItems((cur) => cur.map((it, j) => (j === i ? { ...it, status: "failed", error: msg } : it)));
      }
    }
    setRunning(false);
    if (ok > 0) {
      onDone();
    }
  }

  const pending = items.filter((i) => i.status !== "done").length;
  const allDone = items.length > 0 && pending === 0;

  return (
    <Dialog
      open={open}
      title="Upload files"
      onClose={() => {
        if (!running) {
          onClose();
        }
      }}
      footer={
        <div className="btn-row is-flush">
          <button className="btn" type="button" disabled={running} onClick={onClose}>
            {allDone ? "Close" : "Cancel"}
          </button>
          <button className="btn btn-primary" type="button" disabled={running || pending === 0} onClick={() => void start()}>
            {running ? "Uploading" : `Upload ${pending || ""}`.trim()}
          </button>
        </div>
      }
    >
      <p className="field-hint">
        To <code>{destination}</code>
      </p>
      <button
        type="button"
        className={"upload-drop" + (over ? " is-over" : "")}
        disabled={running}
        onClick={() => input.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setOver(false);
          add(e.dataTransfer.files);
        }}
      >
        <Icon name="transfer" size={22} />
        <span>Click here to upload or drag and drop files</span>
      </button>
      <input
        ref={input}
        className="visually-hidden"
        type="file"
        multiple
        aria-label="Choose files to upload"
        onChange={(e) => {
          add(e.target.files);
          e.target.value = "";
        }}
      />
      {items.length > 0 ? (
        <ul className="upload-list">
          {items.map((it, i) => (
            <li key={it.file.name} className={"is-" + it.status}>
              <span className="upload-name">{it.file.name}</span>
              <span className="muted">{formatBytes(it.file.size)}</span>
              <span className="upload-status">
                {it.status === "ready" ? "Ready" : it.status === "uploading" ? "Uploading" : it.status === "done" ? "Uploaded" : it.error}
              </span>
              {it.status === "ready" || it.status === "failed" ? (
                <button
                  type="button"
                  className="btn btn-ghost btn-sm btn-icon"
                  aria-label={`Remove ${it.file.name}`}
                  disabled={running}
                  onClick={() => setItems((cur) => cur.filter((_, j) => j !== i))}
                >
                  <Icon name="delete" size={14} />
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </Dialog>
  );
}
