import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";
import { createTerminalSession, mkdirFile, uploadFile } from "../api/client";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { uploadDirFromCwd, joinPath } from "../files/paths";
import { shellEscapeAll } from "../files/shell";
import { workloadGuestIOReason } from "../guestIO";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";

function idsFromPath(): { kind: "node" | "workload"; id: string } {
  const parts = currentPath().split("/").filter(Boolean);
  if (parts[0] === "nodes") {
    return { kind: "node", id: parts[1] ?? "" };
  }
  return { kind: "workload", id: parts[1] ?? "" };
}

function cwdFromQuery(): string {
  return new URLSearchParams(window.location.search).get("cwd") || "/";
}

export function TerminalPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const { kind, id } = idsFromPath();
  const host = kind === "node";
  const canOpen = host ? roles?.includes("admin") : Boolean(roles?.includes("admin") || roles?.includes("operator"));
  const wrap = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const sendRef = useRef<(data: string) => void>(() => {});
  const cwdRef = useRef("/");
  const jailRef = useRef("");
  const abortRef = useRef<AbortController | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState("Connecting");
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [ticketKey, setTicketKey] = useState(0);
  const [cwd, setCwd] = useState(cwdFromQuery);
  const [ready, setReady] = useState(kind === "node");
  const [dropActive, setDropActive] = useState(false);
  const [uploadNote, setUploadNote] = useState<string | null>(null);

  cwdRef.current = cwd;

  useEffect(() => {
    if (kind !== "workload") {
      setReady(true);
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
          setReady(false);
          return;
        }
        setUnsupported(null);
        setReady(true);
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

  useEffect(() => {
    if (!canOpen || !ready || unsupported || !wrap.current) {
      return;
    }
    const term = new Terminal({ cursorBlink: true, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace" });
    const fit = new FitAddon();
    term.loadAddon(fit);
    try {
      term.open(wrap.current);
      fit.fit();
    } catch {
      setError("Terminal cannot start in this browser");
      term.dispose();
      return;
    }
    termRef.current = term;
    let ws: WebSocket | null = null;
    let closed = false;

    void (async () => {
      try {
        const created = await createTerminalSession(kind, id, cwd);
        if (!created.ticket || !created.id) {
          throw new Error("session ticket was not returned");
        }
        jailRef.current = created.jail_root ?? "";
        const proto = window.location.protocol === "https:" ? "wss" : "ws";
        ws = new WebSocket(`${proto}://${window.location.host}/api/v1/io/sessions/${created.id}/ws`, [
          `ndl.ticket.${created.ticket}`,
        ]);
        ws.binaryType = "arraybuffer";
        ws.onopen = () => setStatus("Connected");
        ws.onclose = () => {
          if (!closed) {
            setStatus("Session ended");
          }
        };
        ws.onerror = () => setError("Terminal socket failed");
        const encoder = new TextEncoder();
        sendRef.current = (data: string) => {
          ws?.send(encodeFrame(1, encoder.encode(data)));
        };
        ws.onmessage = (ev) => {
          const bytes = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array();
          const frame = decodeFrame(bytes);
          if (!frame) {
            return;
          }
          if (frame.type === 2) {
            term.write(frame.payload);
          }
          if (frame.type === 6) {
            setCwd(new TextDecoder().decode(frame.payload) || "/");
          }
          if (frame.type === 8) {
            setStatus("Session ended");
          }
          if (frame.type === 7) {
            setError(new TextDecoder().decode(frame.payload));
          }
        };
        term.onData((data) => {
          sendRef.current(data);
        });
        term.attachCustomKeyEventHandler((ev) => {
          if (ev.type === "paste" || (ev.ctrlKey && ev.key === "v")) {
            return true;
          }
          return true;
        });
        term.element?.addEventListener("paste", (ev) => {
          const text = ev.clipboardData?.getData("text") ?? "";
          const lines = text.split(/\r?\n/);
          if (lines.length >= 3 && !window.confirm(`Paste ${lines.length} lines into the terminal?`)) {
            ev.preventDefault();
          }
        });
        term.onResize((size) => {
          ws?.send(encodeFrame(3, encodeResize(size.rows, size.cols)));
        });
        window.addEventListener("resize", () => fit.fit());
      } catch (err) {
        setError(err instanceof Error ? err.message : "Could not open terminal");
        setStatus("Failed");
      }
    })();

    return () => {
      closed = true;
      ws?.close();
      term.dispose();
      termRef.current = null;
    };
    // cwd is the start directory for a new ticket, not a live effect input.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canOpen, ready, unsupported, kind, id, ticketKey]);

  async function onDropFiles(list: FileList | null) {
    const files = list ? Array.from(list) : [];
    if (files.length === 0) {
      return;
    }
    const located = uploadDirFromCwd(cwdRef.current, jailRef.current);
    const destDir = located.path;
    abortRef.current?.abort();
    const abort = new AbortController();
    abortRef.current = abort;
    setError(null);
    setUploadNote(`Uploading 0/${files.length} to ${destDir}`);
    try {
      if (located.fallback) {
        try {
          await mkdirFile(kind, id, destDir);
        } catch {
          // directory may already exist
        }
      }
      const uploaded: string[] = [];
      for (let i = 0; i < files.length; i += 1) {
        const file = files[i];
        setUploadNote(`Uploading ${i + 1}/${files.length}: ${file.name}`);
        const dest = joinPath(destDir, file.name);
        await uploadFile(kind, id, dest, file, { signal: abort.signal });
        uploaded.push(dest.startsWith("/") ? dest : `/${dest}`);
      }
      if (located.fallback) {
        setUploadNote(`Uploaded to ${destDir} because the shell cwd is not available.`);
      } else {
        setUploadNote(`Uploaded ${uploaded.length} file(s) to ${destDir}`);
      }
      sendRef.current(shellEscapeAll(uploaded));
    } catch (err) {
      setUploadNote(null);
      setError(err instanceof Error ? err.message : "Upload failed");
    }
  }

  if (unsupported) {
    return (
      <section className="page" aria-labelledby="term-heading">
        <PageHeader id="term-heading" title="Terminal" />
        <p className="banner banner-warn" role="status">
          {unsupported}
        </p>
        <Link href={`/workloads/${id}`}>Back to workload</Link>
      </section>
    );
  }

  if (!canOpen) {
    return (
      <section className="page" aria-labelledby="term-heading">
        <PageHeader id="term-heading" title="Terminal" />
        <p className="banner banner-error" role="alert">
          {host ? "Host terminal requires admin." : "Terminal requires operator or admin."}
        </p>
      </section>
    );
  }

  const filesHref = host ? `/nodes/${id}/files` : `/workloads/${id}/files`;
  const backHref = host ? "/node" : `/workloads/${id}`;

  return (
    <section className="page page-wide" aria-labelledby="term-heading">
      <PageHeader id="term-heading" title="Terminal" kicker={`${status} · cwd ${cwd}`} />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {uploadNote ? (
        <p className="banner" role="status">
          {uploadNote}{" "}
          <button
            className="btn btn-sm btn-ghost"
            type="button"
            onClick={() => {
              abortRef.current?.abort();
              setUploadNote(null);
            }}
          >
            Cancel upload
          </button>
        </p>
      ) : null}
      <nav className="subnav" aria-label="IO">
        <Link href={backHref}>Back</Link>
        <Link href={filesHref}>Open Files</Link>
      </nav>
      <div className="btn-row is-flush">
        <button className="btn btn-sm btn-secondary" type="button" onClick={() => setTicketKey((n) => n + 1)}>
          Reconnect
        </button>
        <button className="btn btn-sm btn-ghost" type="button" onClick={() => navigate(`${filesHref}?path=${encodeURIComponent(cwd)}`)}>
          <Icon name="files" size={14} />
          Open Files here
        </button>
      </div>
      <div
        className="term-wrap"
        data-testid="term-wrap"
        ref={wrap}
        onDragOver={(e) => {
          e.preventDefault();
          if (e.dataTransfer) {
            e.dataTransfer.dropEffect = "copy";
          }
          setDropActive(true);
        }}
        onDragLeave={() => setDropActive(false)}
        onDrop={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setDropActive(false);
          void onDropFiles(e.dataTransfer.files);
        }}
      >
        {dropActive ? <div className="term-drop">Drop files to upload into the current directory</div> : null}
      </div>
    </section>
  );
}

function encodeFrame(type: number, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(5 + payload.length);
  out[0] = type;
  const view = new DataView(out.buffer);
  view.setUint32(1, payload.length);
  out.set(payload, 5);
  return out;
}

function decodeFrame(raw: Uint8Array): { type: number; payload: Uint8Array } | null {
  if (raw.length < 5) {
    return null;
  }
  const view = new DataView(raw.buffer, raw.byteOffset, raw.byteLength);
  const n = view.getUint32(1);
  return { type: raw[0], payload: raw.slice(5, 5 + n) };
}

function encodeResize(rows: number, cols: number): Uint8Array {
  const out = new Uint8Array(4);
  const view = new DataView(out.buffer);
  view.setUint16(0, rows);
  view.setUint16(2, cols);
  return out;
}
