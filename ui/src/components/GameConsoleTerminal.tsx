import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useCallback, useEffect, useRef, useState } from "react";
import { endTerminalSession } from "../api/client";
import { TERMINAL_FONT_KEY, clampTerminalFont } from "../appearance/state";
import {
  createConsoleFav,
  createGameConsoleSession,
  deleteConsoleFav,
  gameConsole,
  gameConsoleHistory,
  sendGameConsole,
} from "../gameservers/api";
import { getPref, subscribePrefs } from "../prefs";
import { decodeFrame, encodeFrame, encodeResize } from "../terminal/frames";
import { terminalOptions } from "../terminal/workspace";
import { terminalColors } from "../theme";
import { registerTerminal } from "./ContextMenu";
import { ErrorNotice } from "./ErrorNotice";

type Fav = { id: string; name: string; command: string };
type LinkState = "connecting" | "live" | "reconnecting";

const STATUS_MS = 5000;
/** Ctrl+U clears the line being typed (the console runs in a PTY). */
const KILL_LINE = "\x15";

/**
 * GameConsoleTerminal is the game server's live console. It streams over
 * the same socket as the workload terminal: the log as the server writes
 * it (with recent history first, and across restarts), and each line typed
 * goes to the server's stdin. Up and Down recall earlier commands; Ctrl+C
 * clears the line and never stops the server.
 */
export function GameConsoleTerminal({ id, name }: { id: string; name: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const sendRef = useRef<(data: string) => boolean>(() => false);
  const line = useRef("");
  const history = useRef<string[]>([]);
  const historyAt = useRef(-1);
  const [link, setLink] = useState<LinkState>("connecting");
  const [running, setRunning] = useState<boolean | null>(null);
  const [favs, setFavs] = useState<Fav[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [epoch, setEpoch] = useState(0);

  const remember = useCallback((cmd: string) => {
    const c = cmd.trim();
    if (c) {
      history.current = [...history.current.filter((h) => h !== c), c].slice(-200);
    }
    historyAt.current = -1;
  }, []);

  // Typing a whole command: clear the line, type it, press Enter.
  const sendLine = useCallback(
    (cmd: string) => {
      if (!sendRef.current(KILL_LINE + cmd + "\r")) {
        // Not connected: send it the way the API does.
        void sendGameConsole(id, cmd).catch((err) => setError(err instanceof Error ? err.message : "The command was not sent"));
      }
      remember(cmd);
      line.current = "";
    },
    [id, remember],
  );

  const refreshStatus = useCallback(async () => {
    const con = await gameConsole(id);
    setRunning(con.running);
    setFavs(con.favorites ?? []);
  }, [id]);

  useEffect(() => {
    void refreshStatus().catch(() => undefined);
    const timer = window.setInterval(() => void refreshStatus().catch(() => undefined), STATUS_MS);
    void gameConsoleHistory(id)
      .then((r) => {
        history.current = r.items ?? [];
      })
      .catch(() => undefined);
    return () => window.clearInterval(timer);
  }, [id, refreshStatus]);

  useEffect(() => {
    const el = holder.current;
    if (!el) {
      return;
    }
    const term = new Terminal(terminalOptions());
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    registerTerminal(term);
    termRef.current = term;
    line.current = "";
    let ws: WebSocket | null = null;
    let sessionId = "";
    let closed = false;
    let retry = 0;
    let retryTimer = 0;
    const encoder = new TextEncoder();

    const sendResize = () => {
      if (ws?.readyState === WebSocket.OPEN && term.rows > 0 && term.cols > 0) {
        ws.send(encodeFrame(3, encodeResize(term.rows, term.cols)));
      }
    };
    const refit = () => {
      try {
        fit.fit();
      } catch {
        // jsdom has no canvas
      }
      sendResize();
    };
    sendRef.current = (data: string) => {
      if (ws?.readyState !== WebSocket.OPEN) {
        return false;
      }
      ws.send(encodeFrame(1, encoder.encode(data)));
      return true;
    };

    const connect = async () => {
      setLink(retry === 0 ? "connecting" : "reconnecting");
      try {
        const created = await createGameConsoleSession(id);
        if (closed) {
          void endTerminalSession(created.id).catch(() => undefined);
          return;
        }
        if (!created.ticket) {
          throw new Error("The console did not return a session");
        }
        sessionId = created.id;
        const proto = window.location.protocol === "https:" ? "wss" : "ws";
        const sock = new WebSocket(`${proto}://${window.location.host}/api/v1/io/sessions/${created.id}/ws`, [
          `ndl.ticket.${created.ticket}`,
        ]);
        sock.binaryType = "arraybuffer";
        ws = sock;
        sock.onopen = () => {
          retry = 0;
          setLink("live");
          setError(null);
          // The history is replayed on every connect; start clean.
          term.reset();
          refit();
        };
        sock.onmessage = (ev) => {
          const frame = decodeFrame(ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array());
          if (!frame) {
            return;
          }
          if (frame.type === 2) {
            term.write(frame.payload);
          }
          if (frame.type === 7) {
            setError(new TextDecoder().decode(frame.payload));
          }
        };
        sock.onclose = () => {
          if (closed || ws !== sock) {
            return;
          }
          scheduleRetry();
        };
      } catch (err) {
        if (closed) {
          return;
        }
        setError(err instanceof Error ? err.message : "The console is unavailable");
        scheduleRetry();
      }
    };
    const scheduleRetry = () => {
      setLink("reconnecting");
      window.clearTimeout(retryTimer);
      const delay = Math.min(15000, 1000 * 2 ** retry);
      retry += 1;
      retryTimer = window.setTimeout(() => void connect(), delay);
    };

    const input = term.onData((data) => {
      if (data === "\x1b[A" || data === "\x1b[B") {
        const list = history.current;
        if (list.length === 0) {
          return;
        }
        let at = historyAt.current < 0 ? list.length : historyAt.current;
        at = data === "\x1b[A" ? Math.max(0, at - 1) : at + 1;
        historyAt.current = at >= list.length ? -1 : at;
        line.current = historyAt.current < 0 ? "" : list[historyAt.current];
        sendRef.current(KILL_LINE + line.current);
        return;
      }
      if (data.startsWith("\x1b")) {
        // Other escape keys have no meaning on a game console line.
        return;
      }
      // Track the line for history; the PTY does the echo and editing.
      for (const ch of data.replace(/\r\n?/g, "\r")) {
        if (ch === "\r") {
          remember(line.current);
          line.current = "";
        } else if (ch === "\x7f" || ch === "\b") {
          line.current = line.current.slice(0, -1);
        } else if (ch === KILL_LINE || ch === "\x03") {
          line.current = "";
        } else if (ch >= " ") {
          line.current += ch;
        }
      }
      sendRef.current(data);
    });

    refit();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(refit);
    observer?.observe(el);
    if (!observer) {
      window.addEventListener("resize", refit);
    }
    const unsubscribe = subscribePrefs(() => {
      term.options.fontSize = clampTerminalFont(getPref(TERMINAL_FONT_KEY, null));
      term.options.theme = terminalColors();
      refit();
    });
    void connect();

    return () => {
      closed = true;
      window.clearTimeout(retryTimer);
      input.dispose();
      observer?.disconnect();
      window.removeEventListener("resize", refit);
      unsubscribe();
      ws?.close();
      if (sessionId) {
        void endTerminalSession(sessionId).catch(() => undefined);
      }
      sendRef.current = () => false;
      term.dispose();
      termRef.current = null;
    };
  }, [id, epoch, remember]);

  const status =
    link !== "live"
      ? link === "connecting"
        ? "Connecting"
        : "Reconnecting"
      : running === false
        ? "Server stopped"
        : "Live";

  return (
    <div className="term-pane gs-console-term">
      <div className="term-id">
        <div className="term-id-main">
          <strong>{name}</strong>
          <span className="term-id-kind">Game console</span>
        </div>
        <div className="term-id-meta">
          <span className={"term-conn is-" + (link === "live" && running !== false ? "active" : "closed")}>{status}</span>
          <button className="btn btn-ghost btn-sm" type="button" title="Clears this view; the server log is kept" onClick={() => termRef.current?.clear()}>
            Clear
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEpoch((n) => n + 1)}>
            Reconnect
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            title="Saves the command on the input line, or the last one sent"
            onClick={() => {
              const command = line.current.trim() || history.current[history.current.length - 1];
              const label = command ? window.prompt("Name this command", command.split(" ")[0]) : null;
              if (command && label) {
                void createConsoleFav(id, label, command).then(refreshStatus);
              }
            }}
          >
            Save command
          </button>
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="term-wrap gs-term-wrap">
        <div className="gs-term-host" ref={holder} onClick={() => termRef.current?.focus()} />
      </div>
      {favs.length > 0 ? (
        <div className="gs-favs" aria-label="Saved commands">
          {favs.map((f) => (
            <span key={f.id} className="gs-fav">
              <button type="button" className="gs-chip" title={f.command} onClick={() => sendLine(f.command)}>
                {f.name}
              </button>
              <button type="button" className="gs-x" aria-label={`Remove ${f.name}`} onClick={() => void deleteConsoleFav(id, f.id).then(refreshStatus)}>
                ×
              </button>
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}
