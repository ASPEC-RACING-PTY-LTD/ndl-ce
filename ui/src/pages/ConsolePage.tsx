import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";
import { createConsoleSession, getWorkload } from "../api/client";
import { Link } from "../components/Link";
import { currentPath } from "../router";

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

export function ConsolePage() {
  const id = workloadIDFromPath();
  const [mode, setMode] = useState<"serial" | "vnc">("serial");
  const [status, setStatus] = useState("Connecting");
  const [error, setError] = useState<string | null>(null);
  const [ticketKey, setTicketKey] = useState(0);
  const wrap = useRef<HTMLDivElement | null>(null);
  const canvas = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    void getWorkload(id).catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
  }, [id]);

  useEffect(() => {
    if (mode !== "serial" || !wrap.current) {
      return;
    }
    const term = new Terminal({ cursorBlink: true, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace" });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(wrap.current);
    fit.fit();
    let ws: WebSocket | null = null;
    let closed = false;
    void (async () => {
      try {
        const created = await createConsoleSession(id, "serial");
        if (!created.ticket || !created.id) {
          throw new Error("session ticket was not returned");
        }
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
        ws.onerror = () => setError("Console socket failed");
        ws.onmessage = (ev) => {
          const bytes = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array();
          const frame = decodeFrame(bytes);
          if (frame?.type === 2) {
            term.write(frame.payload);
          }
          if (frame?.type === 7) {
            setError(new TextDecoder().decode(frame.payload));
          }
          if (frame?.type === 8) {
            setStatus("Session ended");
          }
        };
        term.onData((data) => {
          ws?.send(encodeFrame(1, new TextEncoder().encode(data)));
        });
      } catch (err) {
        setError(err instanceof Error ? err.message : "Could not open console");
        setStatus("Failed");
      }
    })();
    return () => {
      closed = true;
      ws?.close();
      term.dispose();
    };
  }, [id, mode, ticketKey]);

  useEffect(() => {
    if (mode !== "vnc" || !canvas.current) {
      return;
    }
    let ws: WebSocket | null = null;
    let closed = false;
    const ctx = canvas.current.getContext("2d");
    void (async () => {
      try {
        const created = await createConsoleSession(id, "vnc");
        if (!created.ticket || !created.id) {
          throw new Error("session ticket was not returned");
        }
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
        ws.onerror = () => setError("Console socket failed");
        let buf = new Uint8Array();
        ws.onmessage = (ev) => {
          const bytes = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array();
          const frame = decodeFrame(bytes);
          if (!frame || frame.type !== 2) {
            return;
          }
          const next = new Uint8Array(buf.length + frame.payload.length);
          next.set(buf);
          next.set(frame.payload, buf.length);
          buf = next;
          if (ctx && buf.length > 16) {
            ctx.fillStyle = "#111";
            ctx.fillRect(0, 0, canvas.current!.width, canvas.current!.height);
            ctx.fillStyle = "#ddd";
            ctx.fillText("Ticketed VNC session connected. Use serial for the interactive compatibility console.", 16, 24);
          }
        };
      } catch (err) {
        setError(err instanceof Error ? err.message : "Could not open graphical console");
        setStatus("Failed");
      }
    })();
    return () => {
      closed = true;
      ws?.close();
    };
  }, [id, mode, ticketKey]);

  return (
    <section className="page page-wide" aria-labelledby="console-heading">
      <header className="page-header">
        <h1 id="console-heading">Console</h1>
        <p className="page-kicker">
          Compatibility console. No guest agent required. Serial is the interactive text console. Graphical uses a
          ticketed unix VNC socket; this browser view confirms the authorized session rather than decoding a full RFB
          framebuffer. {status}
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <nav className="subnav" aria-label="Console mode">
        <Link href={`/workloads/${id}`}>Summary</Link>
        <Link href={`/workloads/${id}/console`} aria-current="page">
          Console
        </Link>
        <button className="btn" type="button" onClick={() => setMode("serial")}>
          Serial
        </button>
        <button className="btn" type="button" onClick={() => setMode("vnc")}>
          Graphical
        </button>
        <button className="btn" type="button" onClick={() => setTicketKey((n) => n + 1)}>
          Reconnect
        </button>
      </nav>
      {mode === "serial" ? <div className="term-wrap" ref={wrap} /> : <canvas ref={canvas} width={800} height={600} />}
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
