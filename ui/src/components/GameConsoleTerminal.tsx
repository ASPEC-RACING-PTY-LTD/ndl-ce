import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useCallback, useEffect, useRef, useState } from "react";
import { TERMINAL_FONT_KEY, clampTerminalFont } from "../appearance/state";
import { createConsoleFav, deleteConsoleFav, gameConsole, gameConsoleHistory, sendGameConsole } from "../gameservers/api";
import { getPref, subscribePrefs } from "../prefs";
import { terminalOptions } from "../terminal/workspace";
import { terminalColors } from "../theme";
import { registerTerminal } from "./ContextMenu";
import { ErrorNotice } from "./ErrorNotice";

type Fav = { id: string; name: string; command: string };

const PROMPT = "\x1b[38;5;150m>\x1b[0m ";
const POLL_MS = 2000;

/** toTerm turns log text into terminal text: CRLF line ends. */
const toTerm = (s: string) => s.replace(/\r?\n/g, "\r\n");

/** stripControl drops control characters (tabs, escapes) from typed or pasted input. */
const stripControl = (s: string) => [...s].filter((ch) => ch.charCodeAt(0) >= 0x20 && ch.charCodeAt(0) !== 0x7f).join("");

/**
 * GameConsoleTerminal is the workload terminal's look (same xterm setup,
 * text size, theme and frame) driven by the game console API. The game
 * API is line based and polled, not a PTY, so the terminal edits one line
 * locally: Enter sends it, Up and Down walk the history, Ctrl+C clears it.
 */
export function GameConsoleTerminal({ id, name }: { id: string; name: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const line = useRef("");
  const history = useRef<string[]>([]);
  const historyAt = useRef(-1);
  const shown = useRef("");
  const [running, setRunning] = useState<boolean | null>(null);
  const [favs, setFavs] = useState<Fav[]>([]);
  const [error, setError] = useState<string | null>(null);

  const redrawLine = useCallback(() => {
    termRef.current?.write("\r\x1b[2K" + PROMPT + line.current);
  }, []);

  const send = useCallback(
    async (command: string) => {
      const cmd = command.trim();
      if (!cmd) {
        return;
      }
      history.current = [...history.current.filter((h) => h !== cmd), cmd].slice(-200);
      historyAt.current = -1;
      try {
        await sendGameConsole(id, cmd);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : "The command was not sent");
      }
    },
    [id],
  );

  // Poll the log and append what is new above the input line.
  const poll = useCallback(async () => {
    const con = await gameConsole(id);
    setRunning(con.running);
    setFavs(con.favorites ?? []);
    const term = termRef.current;
    const log = con.log ?? "";
    if (!term || log === shown.current) {
      return;
    }
    let chunk: string;
    if (log.startsWith(shown.current)) {
      chunk = log.slice(shown.current.length);
    } else {
      // The server trimmed or restarted its buffer: redraw from the top.
      term.reset();
      chunk = log;
    }
    shown.current = log;
    if (chunk && !chunk.endsWith("\n")) {
      chunk += "\n";
    }
    term.write("\r\x1b[2K" + toTerm(chunk) + PROMPT + line.current);
  }, [id]);

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
    shown.current = "";
    line.current = "";
    const refit = () => {
      try {
        fit.fit();
      } catch {
        // jsdom has no canvas
      }
    };
    refit();
    term.write(PROMPT);
    // Follow the pane's size; fall back to the window where ResizeObserver is missing.
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

    const input = term.onData((data) => {
      if (data === "\r") {
        const cmd = line.current;
        line.current = "";
        term.write("\r\n" + PROMPT);
        void send(cmd);
        return;
      }
      if (data === "\x7f" || data === "\b") {
        line.current = line.current.slice(0, -1);
        redrawLine();
        return;
      }
      if (data === "\x03") {
        line.current = "";
        term.write("^C\r\n" + PROMPT);
        return;
      }
      if (data === "\x1b[A" || data === "\x1b[B") {
        const list = history.current;
        if (list.length === 0) {
          return;
        }
        let at = historyAt.current < 0 ? list.length : historyAt.current;
        at = data === "\x1b[A" ? Math.max(0, at - 1) : at + 1;
        historyAt.current = at >= list.length ? -1 : at;
        line.current = historyAt.current < 0 ? "" : list[historyAt.current];
        redrawLine();
        return;
      }
      if (data.startsWith("\x1b")) {
        return;
      }
      // Typed text or a paste: a pasted newline sends each line in turn.
      const parts = data.replace(/\r\n?/g, "\n").split("\n");
      for (let i = 0; i < parts.length; i += 1) {
        line.current += stripControl(parts[i]);
        if (i < parts.length - 1) {
          const cmd = line.current;
          line.current = "";
          term.write("\r\x1b[2K" + PROMPT + cmd + "\r\n");
          void send(cmd);
        }
      }
      redrawLine();
    });

    void gameConsoleHistory(id)
      .then((r) => {
        history.current = r.items ?? [];
      })
      .catch(() => undefined);
    void poll().catch((err) => setError(err instanceof Error ? err.message : "Console unavailable"));
    const timer = window.setInterval(() => void poll().catch(() => undefined), POLL_MS);
    return () => {
      window.clearInterval(timer);
      input.dispose();
      observer?.disconnect();
      window.removeEventListener("resize", refit);
      unsubscribe();
      term.dispose();
      termRef.current = null;
    };
  }, [id, poll, redrawLine, send]);

  return (
    <div className="term-pane gs-console-term">
      <div className="term-id">
        <div className="term-id-main">
          <strong>{name}</strong>
          <span className="term-id-kind">Game console</span>
        </div>
        <div className="term-id-meta">
          <span className={"term-conn is-" + (running ? "active" : "closed")}>{running === null ? "Connecting" : running ? "Live" : "Server stopped"}</span>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            title="Clears this view; the server log is kept"
            onClick={() => {
              termRef.current?.clear();
              redrawLine();
            }}
          >
            Clear
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            title="Saves the command on the input line, or the last one sent"
            onClick={() => {
              const command = line.current.trim() || history.current[history.current.length - 1];
              const label = command ? window.prompt("Name this command", command.split(" ")[0]) : null;
              if (command && label) {
                void createConsoleFav(id, label, command).then(poll);
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
              <button type="button" className="gs-chip" title={f.command} onClick={() => void send(f.command)}>
                {f.name}
              </button>
              <button type="button" className="gs-x" aria-label={`Remove ${f.name}`} onClick={() => void deleteConsoleFav(id, f.id).then(poll)}>
                ×
              </button>
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}
