import { registerTerminal } from "../components/ContextMenu";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ApiError, attachTerminalSession, createTerminalSession, endTerminalSession } from "../api/client";
import { canMutate, isAdmin } from "../rbac";
import { storageGet, storageSet } from "../storage";
import { useSession } from "../session";
import "@xterm/xterm/css/xterm.css";
import { pushRecent } from "./catalog";
import { decodeFrame, encodeFrame, encodeResize } from "./frames";
import { defaultTabTitle } from "./names";
import { isTermCopyChord, isTermPasteChord, normalizePasteText, shouldConfirmPaste } from "./paste";
import type { TermConnState, TermTab, TermTarget } from "./types";

const STORE_KEY = "ndl-term-workspace";

// A dropped socket (a tunnel reconnect, Wi-Fi blip, laptop sleep) does not end
// the shell: the server keeps it for 10 minutes, so the page keeps trying to
// reattach for that long before giving up.
export const TERM_RETRY_WINDOW_MS = 10 * 60 * 1000;
const RETRY_DELAYS_MS = [500, 1000, 2000, 4000, 8000, 10000];

type Runtime = {
  term: Terminal;
  fit: FitAddon;
  ws: WebSocket | null;
  send: (data: string) => void;
  holder: HTMLDivElement;
  closed: boolean;
  abort: AbortController | null;
  lastCols: number;
  lastRows: number;
  resizeTimer: number;
  // ended is set when the server says the shell exited, so the close that
  // follows is not retried.
  ended: boolean;
  retryTimer: number;
  retryCount: number;
  retrySince: number;
};

type StoredWorkspace = {
  userId?: string;
  tabs?: Array<Pick<TermTab, "tabId" | "title" | "customTitle" | "target" | "startCwd" | "ioSessionId">>;
  activeId?: string | null;
  recents?: TermTarget[];
};

export type OpenTermOpts = {
  cwd?: string;
  title?: string;
  forceNew?: boolean;
};

type WorkspaceValue = {
  tabs: TermTab[];
  activeId: string | null;
  recents: TermTarget[];
  active: TermTab | null;
  openNew: (target: TermTarget, opts?: OpenTermOpts) => string;
  openOrFocus: (target: TermTarget, opts?: OpenTermOpts) => string;
  newHere: (tabId: string) => string;
  duplicate: (tabId: string) => string;
  moveTab: (tabId: string, toIndex: number) => void;
  rename: (tabId: string, title: string) => void;
  closeTab: (tabId: string) => void;
  closeAll: () => void;
  closeOthers: (tabId: string) => void;
  closeToSide: (tabId: string, side: "left" | "right") => void;
  closeDisconnected: () => void;
  reconnect: (tabId: string) => void;
  replaceCurrent: (target: TermTarget) => string;
  setActive: (tabId: string) => void;
  nextTab: () => void;
  prevTab: () => void;
  attach: (tabId: string, el: HTMLElement) => void;
  detach: (tabId: string) => void;
  send: (tabId: string, data: string) => void;
  fit: (tabId: string) => void;
  dropAbort: (tabId: string) => AbortController;
  setTabError: (tabId: string, error: string | null) => void;
};

const WorkspaceContext = createContext<WorkspaceValue | null>(null);
const runtimes = new Map<string, Runtime>();

function newId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `tab-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function loadStore(userId: string): StoredWorkspace {
  try {
    const raw = storageGet(STORE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as StoredWorkspace;
    if (parsed.userId && parsed.userId !== userId) {
      return {};
    }
    return parsed;
  } catch {
    return {};
  }
}

function makeTerm(): { term: Terminal; fit: FitAddon; holder: HTMLDivElement } {
  const holder = document.createElement("div");
  holder.className = "term-hold";
  holder.setAttribute("aria-hidden", "true");
  document.body.appendChild(holder);
  const term = new Terminal({ cursorBlink: true, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace" });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(holder);
  registerTerminal(term);
  return { term, fit, holder };
}

// sendPtySize tells the remote shell the terminal's size. A shell that
// believes in another width redraws recalled history lines over the text
// above them, so every new socket starts from "size unknown" (force) and the
// size is sent again shortly after the shell starts.
function sendPtySize(tabId: string, force = false): void {
  const rt = runtimes.get(tabId);
  const socket = rt?.ws;
  const term = rt?.term;
  if (!rt || !term || !socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const parent = term.element?.parentElement;
  if (!parent || parent === rt.holder || parent.classList.contains("term-hold")) {
    return;
  }
  const cols = term.cols;
  const rows = term.rows;
  if (!cols || !rows || (!force && cols === rt.lastCols && rows === rt.lastRows)) {
    return;
  }
  rt.lastCols = cols;
  rt.lastRows = rows;
  socket.send(encodeFrame(3, encodeResize(rows, cols)));
}

export function TerminalWorkspaceProvider({ children }: { children: ReactNode }) {
  const session = useSession();
  const userId = session.status === "ready" ? session.user?.user_id ?? "" : "";
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const seeded = useRef(false);
  const [tabs, setTabs] = useState<TermTab[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [recents, setRecents] = useState<TermTarget[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;

  useEffect(() => {
    if (!userId || seeded.current) {
      return;
    }
    seeded.current = true;
    const stored = loadStore(userId);
    if (stored.recents?.length) {
      setRecents(
        stored.recents.filter((t) => {
          if (!canMutate(roles)) {
            return false;
          }
          if (t.kind === "node") {
            return isAdmin(roles);
          }
          return true;
        }),
      );
    }
    if (stored.tabs?.length) {
      const restored = stored.tabs
        .filter((t) => {
          if (!canMutate(roles)) {
            return false;
          }
          if (t.target.kind === "node") {
            return isAdmin(roles);
          }
          return true;
        })
        .map((t) => ({
          ...t,
          state: "disconnected" as TermConnState,
          cwd: t.startCwd || "/",
          jailRoot: "",
          error: null,
        }));
      setTabs(restored);
      setActiveId(stored.activeId && restored.some((t) => t.tabId === stored.activeId) ? stored.activeId : restored[0]?.tabId ?? null);
    }
    setHydrated(true);
  }, [userId, roles]);

  useEffect(() => {
    if (!userId || !hydrated) {
      return;
    }
    storageSet(
      STORE_KEY,
      JSON.stringify({
        userId,
        recents,
        activeId,
        tabs: tabs.map((t) => ({
          tabId: t.tabId,
          title: t.title,
          customTitle: t.customTitle,
          target: t.target,
          startCwd: t.startCwd,
          ioSessionId: t.ioSessionId,
        })),
      }),
    );
  }, [userId, hydrated, tabs, activeId, recents]);

  const patch = useCallback((tabId: string, partial: Partial<TermTab>) => {
    setTabs((cur) => cur.map((t) => (t.tabId === tabId ? { ...t, ...partial } : t)));
  }, []);

  const disposeRuntime = useCallback((tabId: string) => {
    const rt = runtimes.get(tabId);
    if (!rt) {
      return;
    }
    rt.closed = true;
    rt.abort?.abort();
    window.clearTimeout(rt.resizeTimer);
    window.clearTimeout(rt.retryTimer);
    rt.ws?.close();
    rt.term.dispose();
    rt.holder.remove();
    runtimes.delete(tabId);
  }, []);

  const connect = useCallback(
    (tab: TermTab, mode: "connecting" | "reconnecting", auto = false) => {
      let rt = runtimes.get(tab.tabId);
      if (!rt) {
        const made = makeTerm();
        rt = {
          term: made.term,
          fit: made.fit,
          ws: null,
          send: () => {},
          holder: made.holder,
          closed: false,
          abort: null,
          lastCols: 0,
          lastRows: 0,
          resizeTimer: 0,
          ended: false,
          retryTimer: 0,
          retryCount: 0,
          retrySince: 0,
        };
        runtimes.set(tab.tabId, rt);
        const created = rt;
        created.term.onData((data) => {
          created.send(data);
        });
        created.term.attachCustomKeyEventHandler((ev) => {
          if (ev.type !== "keydown") {
            return true;
          }
          if (isTermCopyChord(ev)) {
            const sel = created.term.getSelection();
            if (sel) {
              void navigator.clipboard?.writeText(sel);
            }
            ev.preventDefault();
            return false;
          }
          if (isTermPasteChord(ev)) {
            return true;
          }
          return true;
        });
        created.term.element?.addEventListener("paste", (ev) => {
          const text = normalizePasteText(ev.clipboardData?.getData("text") ?? "");
          if (!text) {
            ev.preventDefault();
            return;
          }
          if (shouldConfirmPaste(text) && !window.confirm(`Paste ${text.split("\n").length} lines into the terminal?`)) {
            ev.preventDefault();
            return;
          }
          ev.preventDefault();
          try {
            created.term.paste(text);
          } catch {
            created.send(text);
          }
        });
        rt.term.onResize(() => {
          const current = runtimes.get(tab.tabId);
          if (!current) {
            return;
          }
          window.clearTimeout(current.resizeTimer);
          current.resizeTimer = window.setTimeout(() => sendPtySize(tab.tabId), 32);
        });
      }
      rt.closed = false;
      rt.ended = false;
      // Forget the old socket first so its close is not taken for a drop.
      const stale = rt.ws;
      rt.ws = null;
      stale?.close();
      window.clearTimeout(rt.retryTimer);
      if (!auto) {
        rt.retryCount = 0;
        rt.retrySince = 0;
      }
      patch(tab.tabId, { state: mode, error: null });
      const owner = rt;
      // retry schedules another reattach while the server still holds the
      // shell; after the window it leaves the tab disconnected.
      const retry = (why: string) => {
        if (owner.closed || owner.ended || runtimes.get(tab.tabId) !== owner) {
          return;
        }
        const now = Date.now();
        if (!owner.retrySince) {
          owner.retrySince = now;
        }
        if (now - owner.retrySince >= TERM_RETRY_WINDOW_MS) {
          patch(tab.tabId, { state: "disconnected", error: why });
          return;
        }
        const delay = RETRY_DELAYS_MS[Math.min(owner.retryCount, RETRY_DELAYS_MS.length - 1)];
        owner.retryCount += 1;
        patch(tab.tabId, { state: "reconnecting", error: null });
        window.clearTimeout(owner.retryTimer);
        owner.retryTimer = window.setTimeout(() => {
          const latest = tabsRef.current.find((t) => t.tabId === tab.tabId);
          if (latest && runtimes.get(tab.tabId) === owner && !owner.closed) {
            connectRef.current(latest, "reconnecting", true);
          }
        }, delay);
      };
      void (async () => {
        try {
          // A shell outlives its socket for a while on the server, so a
          // refresh or reconnect resumes it instead of starting a new one.
          const prior = tabsRef.current.find((t) => t.tabId === tab.tabId)?.ioSessionId ?? tab.ioSessionId;
          let created = null;
          if (prior && auto) {
            // An automatic retry only ever resumes the same shell.
            try {
              created = await attachTerminalSession(prior);
            } catch (err) {
              if (err instanceof ApiError && (err.status === 410 || err.status === 404)) {
                owner.ended = true;
                patch(tab.tabId, { state: "closed", error: "The shell ended while the connection was down." });
              } else {
                retry("Could not reach the server");
              }
              return;
            }
          } else if (prior) {
            created = await attachTerminalSession(prior).catch(() => null);
          }
          if (created && created.ticket && created.id === prior) {
            // The server replays recent output; clear what is on screen first.
            try {
              runtimes.get(tab.tabId)?.term.reset();
            } catch {
              // jsdom has no canvas
            }
          } else {
            created = await createTerminalSession(tab.target.kind, tab.target.id, tab.cwd || tab.startCwd || "/");
          }
          if (!created.ticket || !created.id) {
            throw new Error("session ticket was not returned");
          }
          const current = runtimes.get(tab.tabId);
          if (!current || current.closed) {
            return;
          }
          patch(tab.tabId, { ioSessionId: created.id, jailRoot: created.jail_root ?? "" });
          const proto = window.location.protocol === "https:" ? "wss" : "ws";
          const ws = new WebSocket(`${proto}://${window.location.host}/api/v1/io/sessions/${created.id}/ws`, [
            `ndl.ticket.${created.ticket}`,
          ]);
          ws.binaryType = "arraybuffer";
          current.ws = ws;
          // A new shell knows nothing about the size the old one had.
          current.lastCols = 0;
          current.lastRows = 0;
          const encoder = new TextEncoder();
          current.send = (data: string) => {
            ws.send(encodeFrame(1, encoder.encode(data)));
          };
          ws.onopen = () => {
            current.retryCount = 0;
            current.retrySince = 0;
            patch(tab.tabId, { state: "active", error: null });
            try {
              current.fit.fit();
            } catch {
              // jsdom has no canvas
            }
            sendPtySize(tab.tabId, true);
            for (const delay of [400, 1500]) {
              window.setTimeout(() => {
                if (runtimes.get(tab.tabId)?.ws === ws) {
                  sendPtySize(tab.tabId, true);
                }
              }, delay);
            }
          };
          ws.onclose = () => {
            if (current.closed || current.ws !== ws) {
              return;
            }
            if (current.ended) {
              patch(tab.tabId, { state: "closed" });
              return;
            }
            retry("Lost the connection to the terminal");
          };
          ws.onmessage = (ev) => {
            const bytes = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array();
            const frame = decodeFrame(bytes);
            if (!frame) {
              return;
            }
            if (frame.type === 2) {
              current.term.write(frame.payload);
            }
            if (frame.type === 6) {
              patch(tab.tabId, { cwd: new TextDecoder().decode(frame.payload) || "/" });
            }
            if (frame.type === 8) {
              current.ended = true;
              patch(tab.tabId, { state: "closed" });
            }
            if (frame.type === 7) {
              patch(tab.tabId, { error: new TextDecoder().decode(frame.payload) });
            }
          };
        } catch (err) {
          if (auto) {
            retry(err instanceof Error ? err.message : "Could not open terminal");
            return;
          }
          patch(tab.tabId, {
            state: "disconnected",
            error: err instanceof Error ? err.message : "Could not open terminal",
          });
        }
      })();
    },
    [patch],
  );
  const connectRef = useRef(connect);
  connectRef.current = connect;

  // Coming back online or back to the tab retries at once instead of
  // waiting out the backoff.
  useEffect(() => {
    function kick() {
      if (document.visibilityState === "hidden") {
        return;
      }
      for (const tab of tabsRef.current) {
        const rt = runtimes.get(tab.tabId);
        if (rt && rt.retryTimer && tab.state === "reconnecting" && !rt.closed && !rt.ended) {
          window.clearTimeout(rt.retryTimer);
          rt.retryTimer = 0;
          connectRef.current(tab, "reconnecting", true);
        }
      }
    }
    window.addEventListener("online", kick);
    document.addEventListener("visibilitychange", kick);
    return () => {
      window.removeEventListener("online", kick);
      document.removeEventListener("visibilitychange", kick);
    };
  }, []);

  const openNew = useCallback(
    (target: TermTarget, opts?: OpenTermOpts) => {
      const tabId = newId();
      const title =
        opts?.title?.trim() ||
        defaultTabTitle(
          target.name,
          tabsRef.current.map((t) => t.title),
        );
      const tab: TermTab = {
        tabId,
        title,
        customTitle: Boolean(opts?.title?.trim()),
        target,
        state: "connecting",
        cwd: opts?.cwd || "/",
        jailRoot: "",
        error: null,
        startCwd: opts?.cwd || "/",
      };
      setTabs((cur) => [...cur, tab]);
      setActiveId(tabId);
      setRecents((cur) => pushRecent(cur, target));
      connect(tab, "connecting");
      return tabId;
    },
    [connect],
  );

  const openOrFocus = useCallback(
    (target: TermTarget, opts?: OpenTermOpts) => {
      const cwd = opts?.cwd || "/";
      if (opts?.forceNew) {
        const pending = tabsRef.current.find(
          (t) =>
            t.target.kind === target.kind &&
            t.target.id === target.id &&
            t.startCwd === cwd &&
            (t.state === "connecting" || t.state === "reconnecting"),
        );
        if (pending) {
          setActiveId(pending.tabId);
          setRecents((cur) => pushRecent(cur, target));
          return pending.tabId;
        }
      } else if (!(opts?.cwd && opts.cwd !== "/")) {
        const live = [...tabsRef.current]
          .reverse()
          .find(
            (t) =>
              t.target.kind === target.kind &&
              t.target.id === target.id &&
              (t.state === "active" || t.state === "connecting" || t.state === "reconnecting"),
          );
        if (live) {
          setActiveId(live.tabId);
          setRecents((cur) => pushRecent(cur, target));
          return live.tabId;
        }
      }
      return openNew(target, opts);
    },
    [openNew],
  );

  const newHere = useCallback(
    (tabId: string) => {
      const tab = tabsRef.current.find((t) => t.tabId === tabId);
      if (!tab) {
        return tabId;
      }
      return openNew(tab.target);
    },
    [openNew],
  );

  // duplicate opens a second, independent shell on the same target in the
  // same directory, placed right after the original tab.
  const duplicate = useCallback(
    (tabId: string) => {
      const tab = tabsRef.current.find((t) => t.tabId === tabId);
      if (!tab) {
        return tabId;
      }
      const next = openNew(tab.target, { cwd: tab.cwd || tab.startCwd || "/" });
      setTabs((cur) => {
        const from = cur.findIndex((t) => t.tabId === next);
        const at = cur.findIndex((t) => t.tabId === tabId);
        if (from < 0 || at < 0) {
          return cur;
        }
        const out = [...cur];
        const [moved] = out.splice(from, 1);
        out.splice(at + 1, 0, moved);
        return out;
      });
      return next;
    },
    [openNew],
  );

  const moveTab = useCallback((tabId: string, toIndex: number) => {
    setTabs((cur) => {
      const from = cur.findIndex((t) => t.tabId === tabId);
      const to = Math.max(0, Math.min(cur.length - 1, toIndex));
      if (from < 0 || from === to) {
        return cur;
      }
      const out = [...cur];
      const [moved] = out.splice(from, 1);
      out.splice(to, 0, moved);
      return out;
    });
  }, []);

  const rename = useCallback((tabId: string, title: string) => {
    const next = title.trim();
    if (!next) {
      return;
    }
    patch(tabId, { title: next, customTitle: true });
  }, [patch]);

  // endShell ends a tab's shell on the server when the user closes the tab;
  // leaving the page only detaches it, so a refresh can resume it.
  const endShell = useCallback((tabId: string) => {
    const id = tabsRef.current.find((t) => t.tabId === tabId)?.ioSessionId;
    if (id) {
      void endTerminalSession(id).catch(() => undefined);
    }
  }, []);

  const closeTab = useCallback(
    (tabId: string) => {
      endShell(tabId);
      disposeRuntime(tabId);
      setTabs((cur) => {
        const next = cur.filter((t) => t.tabId !== tabId);
        setActiveId((id) => {
          if (id !== tabId) {
            return id;
          }
          const idx = cur.findIndex((t) => t.tabId === tabId);
          return next[idx]?.tabId ?? next[idx - 1]?.tabId ?? next[0]?.tabId ?? null;
        });
        return next;
      });
    },
    [disposeRuntime, endShell],
  );

  const closeAll = useCallback(() => {
    for (const tab of tabsRef.current) {
      endShell(tab.tabId);
      disposeRuntime(tab.tabId);
    }
    setTabs([]);
    setActiveId(null);
  }, [disposeRuntime, endShell]);

  const closeOthers = useCallback(
    (tabId: string) => {
      for (const tab of tabsRef.current) {
        if (tab.tabId !== tabId) {
          endShell(tab.tabId);
          disposeRuntime(tab.tabId);
        }
      }
      setTabs((cur) => cur.filter((t) => t.tabId === tabId));
      setActiveId(tabId);
    },
    [disposeRuntime, endShell],
  );

  const closeToSide = useCallback(
    (tabId: string, side: "left" | "right") => {
      const cur = tabsRef.current;
      const idx = cur.findIndex((t) => t.tabId === tabId);
      if (idx < 0) {
        return;
      }
      const gone = new Set((side === "left" ? cur.slice(0, idx) : cur.slice(idx + 1)).map((t) => t.tabId));
      if (gone.size === 0) {
        return;
      }
      for (const id of gone) {
        endShell(id);
        disposeRuntime(id);
      }
      setTabs((tabs) => tabs.filter((t) => !gone.has(t.tabId)));
      setActiveId((id) => (id && gone.has(id) ? tabId : id));
    },
    [disposeRuntime, endShell],
  );

  const closeDisconnected = useCallback(() => {
    const gone = tabsRef.current.filter((t) => t.state === "disconnected" || t.state === "closed");
    for (const tab of gone) {
      endShell(tab.tabId);
      disposeRuntime(tab.tabId);
    }
    setTabs((cur) => {
      const next = cur.filter((t) => t.state !== "disconnected" && t.state !== "closed");
      setActiveId((id) => (next.some((t) => t.tabId === id) ? id : next[0]?.tabId ?? null));
      return next;
    });
  }, [disposeRuntime, endShell]);

  const reconnect = useCallback(
    (tabId: string) => {
      const tab = tabsRef.current.find((t) => t.tabId === tabId);
      const rt = runtimes.get(tabId);
      if (!tab) {
        return;
      }
      rt?.ws?.close();
      connect(tab, "reconnecting");
    },
    [connect],
  );

  // After a refresh, resume the shells that are still running on the server.
  const resumed = useRef(false);
  useEffect(() => {
    if (!hydrated || resumed.current) {
      return;
    }
    resumed.current = true;
    for (const tab of tabsRef.current) {
      if (tab.ioSessionId && tab.state === "disconnected" && !runtimes.has(tab.tabId)) {
        connect(tab, "reconnecting");
      }
    }
  }, [hydrated, connect]);

  const replaceCurrent = useCallback(
    (target: TermTarget) => {
      if (activeId) {
        closeTab(activeId);
      }
      return openNew(target);
    },
    [activeId, closeTab, openNew],
  );

  const nextTab = useCallback(() => {
    setActiveId((id) => {
      const list = tabsRef.current;
      if (list.length === 0) {
        return id;
      }
      const idx = list.findIndex((t) => t.tabId === id);
      return list[(idx + 1) % list.length]?.tabId ?? list[0].tabId;
    });
  }, []);

  const prevTab = useCallback(() => {
    setActiveId((id) => {
      const list = tabsRef.current;
      if (list.length === 0) {
        return id;
      }
      const idx = list.findIndex((t) => t.tabId === id);
      return list[(idx - 1 + list.length) % list.length]?.tabId ?? list[0].tabId;
    });
  }, []);

  const attach = useCallback((tabId: string, el: HTMLElement) => {
    const rt = runtimes.get(tabId);
    if (!rt?.term.element) {
      return;
    }
    if (rt.term.element.parentElement !== el) {
      const keepFocus = document.activeElement instanceof Node && rt.term.element.contains(document.activeElement);
      el.appendChild(rt.term.element);
      if (keepFocus) {
        rt.term.focus();
      }
    }
    try {
      rt.fit.fit();
    } catch {
      // jsdom has no canvas
    }
    sendPtySize(tabId);
  }, []);

  const detach = useCallback((tabId: string) => {
    const rt = runtimes.get(tabId);
    if (!rt?.term.element) {
      return;
    }
    if (rt.term.element.parentElement !== rt.holder) {
      rt.holder.appendChild(rt.term.element);
    }
  }, []);

  const send = useCallback((tabId: string, data: string) => {
    runtimes.get(tabId)?.send(data);
  }, []);

  const fit = useCallback((tabId: string) => {
    const rt = runtimes.get(tabId);
    if (!rt) {
      return;
    }
    try {
      rt.fit.fit();
    } catch {
      // jsdom has no canvas
    }
    sendPtySize(tabId);
  }, []);

  const setTabError = useCallback(
    (tabId: string, error: string | null) => {
      patch(tabId, { error });
    },
    [patch],
  );

  const dropAbort = useCallback((tabId: string) => {
    const rt = runtimes.get(tabId);
    rt?.abort?.abort();
    const next = new AbortController();
    if (rt) {
      rt.abort = next;
    }
    return next;
  }, []);

  useEffect(() => {
    return () => {
      for (const id of [...runtimes.keys()]) {
        disposeRuntime(id);
      }
    };
  }, [disposeRuntime]);

  useEffect(() => {
    if (!userId) {
      for (const id of [...runtimes.keys()]) {
        disposeRuntime(id);
      }
      setTabs([]);
      setActiveId(null);
      setHydrated(false);
      seeded.current = false;
    }
  }, [userId, disposeRuntime]);

  const value = useMemo<WorkspaceValue>(
    () => ({
      tabs,
      activeId,
      recents,
      active: tabs.find((t) => t.tabId === activeId) ?? null,
      openNew,
      openOrFocus,
      newHere,
      duplicate,
      moveTab,
      rename,
      closeTab,
      closeAll,
      closeOthers,
      closeToSide,
      closeDisconnected,
      reconnect,
      replaceCurrent,
      setActive: setActiveId,
      nextTab,
      prevTab,
      attach,
      detach,
      send,
      fit,
      dropAbort,
      setTabError,
    }),
    [
      tabs,
      activeId,
      recents,
      openNew,
      openOrFocus,
      newHere,
      duplicate,
      moveTab,
      rename,
      closeTab,
      closeAll,
      closeOthers,
      closeToSide,
      closeDisconnected,
      reconnect,
      replaceCurrent,
      nextTab,
      prevTab,
      attach,
      detach,
      send,
      fit,
      dropAbort,
      setTabError,
    ],
  );

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>;
}

export function useTerminalWorkspace(): WorkspaceValue {
  const ctx = useContext(WorkspaceContext);
  if (!ctx) {
    throw new Error("Terminal workspace is unavailable");
  }
  return ctx;
}
