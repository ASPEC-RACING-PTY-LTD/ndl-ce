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
import { createTerminalSession } from "../api/client";
import { canMutate, isAdmin } from "../rbac";
import { storageGet, storageSet } from "../storage";
import { useSession } from "../session";
import "@xterm/xterm/css/xterm.css";
import { pushRecent } from "./catalog";
import { decodeFrame, encodeFrame, encodeResize } from "./frames";
import { defaultTabTitle } from "./names";
import type { TermConnState, TermTab, TermTarget } from "./types";

const STORE_KEY = "ndl-term-workspace";

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
};

type StoredWorkspace = {
  userId?: string;
  tabs?: Array<Pick<TermTab, "tabId" | "title" | "customTitle" | "target" | "startCwd">>;
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
  rename: (tabId: string, title: string) => void;
  closeTab: (tabId: string) => void;
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
  return { term, fit, holder };
}

function sendPtySize(tabId: string): void {
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
  if (!cols || !rows || (cols === rt.lastCols && rows === rt.lastRows)) {
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
          ioSessionId: undefined,
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
    rt.ws?.close();
    rt.term.dispose();
    rt.holder.remove();
    runtimes.delete(tabId);
  }, []);

  const connect = useCallback(
    (tab: TermTab, mode: "connecting" | "reconnecting") => {
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
        };
        runtimes.set(tab.tabId, rt);
        rt.term.onData((data) => {
          rt?.send(data);
        });
        rt.term.attachCustomKeyEventHandler(() => true);
        rt.term.element?.addEventListener("paste", (ev) => {
          const text = ev.clipboardData?.getData("text") ?? "";
          const lines = text.split(/\r?\n/);
          if (lines.length >= 3 && !window.confirm(`Paste ${lines.length} lines into the terminal?`)) {
            ev.preventDefault();
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
      patch(tab.tabId, { state: mode, error: null });
      void (async () => {
        try {
          const created = await createTerminalSession(tab.target.kind, tab.target.id, tab.cwd || tab.startCwd || "/");
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
          const encoder = new TextEncoder();
          current.send = (data: string) => {
            ws.send(encodeFrame(1, encoder.encode(data)));
          };
          ws.onopen = () => {
            patch(tab.tabId, { state: "active" });
            try {
              current.fit.fit();
            } catch {
              // jsdom has no canvas
            }
            sendPtySize(tab.tabId);
          };
          ws.onerror = () => patch(tab.tabId, { error: "Terminal socket failed" });
          ws.onclose = () => {
            if (!current.closed) {
              patch(tab.tabId, { state: "disconnected" });
            }
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
              patch(tab.tabId, { state: "closed" });
            }
            if (frame.type === 7) {
              patch(tab.tabId, { error: new TextDecoder().decode(frame.payload) });
            }
          };
        } catch (err) {
          patch(tab.tabId, {
            state: "disconnected",
            error: err instanceof Error ? err.message : "Could not open terminal",
          });
        }
      })();
    },
    [patch],
  );

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

  const rename = useCallback((tabId: string, title: string) => {
    const next = title.trim();
    if (!next) {
      return;
    }
    patch(tabId, { title: next, customTitle: true });
  }, [patch]);

  const closeTab = useCallback(
    (tabId: string) => {
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
    [disposeRuntime],
  );

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
      el.appendChild(rt.term.element);
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
      rename,
      closeTab,
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
      rename,
      closeTab,
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
