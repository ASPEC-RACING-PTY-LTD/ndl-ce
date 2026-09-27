import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ActionMenu } from "../components/ActionMenu";
import { Icon } from "../components/Icon";
import { QuickSwitch } from "../components/QuickSwitch";
import { TerminalPane } from "../components/TerminalPane";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { statusLabel } from "../terminal/types";
import { useTerminalWorkspace } from "../terminal/workspace";

export function TerminalWorkspacePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const allowed = canMutate(roles);
  const {
    tabs,
    activeId,
    recents,
    active,
    setActive,
    openNew,
    newHere,
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
  } = useTerminalWorkspace();
  const [qs, setQs] = useState(false);
  const [menu, setMenu] = useState<{ tabId: string; x: number; y: number } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const qsOpen = useRef(false);
  qsOpen.current = qs;

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (!event.altKey || event.ctrlKey || event.metaKey) {
        return;
      }
      const tag = (event.target as HTMLElement | null)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
        if (event.key !== "n" && event.key !== "N") {
          return;
        }
      }
      if (event.key === "n" || event.key === "N") {
        event.preventDefault();
        setQs(true);
        return;
      }
      if (qsOpen.current) {
        return;
      }
      if (event.key === "]") {
        event.preventDefault();
        nextTab();
      }
      if (event.key === "[") {
        event.preventDefault();
        prevTab();
      }
      if (event.key === "w" || event.key === "W") {
        event.preventDefault();
        if (activeId) {
          closeTab(activeId);
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [activeId, closeTab, nextTab, prevTab]);

  useEffect(() => {
    if (!menu) {
      return;
    }
    function onDoc() {
      setMenu(null);
    }
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [menu]);

  useLayoutEffect(() => {
    const el = menuRef.current;
    if (!menu || !el) {
      return;
    }
    const margin = 8;
    const rect = el.getBoundingClientRect();
    el.style.left = `${Math.max(margin, Math.min(menu.x, window.innerWidth - rect.width - margin))}px`;
    el.style.top = `${Math.max(margin, Math.min(menu.y, window.innerHeight - rect.height - margin))}px`;
  }, [menu]);

  function renameTab(tabId: string, current: string) {
    const next = window.prompt("Session name", current);
    if (next) {
      rename(tabId, next);
    }
  }

  if (!allowed) {
    return (
      <section className="page" aria-labelledby="term-heading">
        <h1 id="term-heading">Terminal</h1>
        <p className="banner banner-error" role="alert">
          Terminal requires operator or admin.
        </p>
      </section>
    );
  }

  const menuTab = menu ? tabs.find((t) => t.tabId === menu.tabId) : null;
  const menuIndex = menuTab ? tabs.indexOf(menuTab) : -1;

  function menuAction(action: () => void) {
    action();
    setMenu(null);
  }

  return (
    <section className="page page-wide page-term" aria-labelledby="term-heading">
      <div className="term-toolbar">
        <h1 id="term-heading" className="term-page-title">
          Terminal
        </h1>
        <div className="term-tabs" role="tablist" aria-label="Terminal sessions">
          {tabs.map((tab) => (
            <button
              key={tab.tabId}
              type="button"
              role="tab"
              aria-selected={tab.tabId === activeId}
              className={"term-tab" + (tab.tabId === activeId ? " is-active" : "") + (tab.target.kind === "node" ? " is-host" : "")}
              data-tab-id={tab.tabId}
              data-io-session={tab.ioSessionId || ""}
              data-session-target={`${tab.target.kind}:${tab.target.id}`}
              title={`${tab.title} · ${tab.target.typeLabel} · ${statusLabel(tab.state)}`}
              onClick={() => setActive(tab.tabId)}
              onMouseDown={(event) => {
                if (event.button === 1) {
                  event.preventDefault();
                }
              }}
              onAuxClick={(event) => {
                if (event.button === 1) {
                  event.preventDefault();
                  closeTab(tab.tabId);
                }
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                setMenu({ tabId: tab.tabId, x: event.clientX, y: event.clientY });
              }}
              onDoubleClick={() => renameTab(tab.tabId, tab.title)}
            >
              {tab.target.kind === "node" ? <span className="term-host-dot">H</span> : null}
              <span className="term-tab-title">{tab.title}</span>
              <span className={"term-tab-state is-" + tab.state}>{statusLabel(tab.state)}</span>
            </button>
          ))}
          <button
            className="btn btn-sm btn-secondary term-add"
            type="button"
            aria-label="New terminal"
            title="New terminal"
            aria-keyshortcuts="Alt+N"
            onClick={() => setQs(true)}
          >
            <Icon name="create" size={14} />
            +
          </button>
        </div>
        {tabs.length > 6 ? (
          <label className="field-hint term-tab-overflow">
            Sessions
            <select
              className="field-input"
              value={activeId ?? ""}
              onChange={(event) => setActive(event.target.value)}
              aria-label="All terminal sessions"
            >
              {tabs.map((tab) => (
                <option key={tab.tabId} value={tab.tabId}>
                  {tab.title} ({statusLabel(tab.state)})
                </option>
              ))}
            </select>
          </label>
        ) : null}
        {active ? (
          <ActionMenu
            label="Session actions"
            items={[
              { label: "Rename", onClick: () => renameTab(active.tabId, active.title) },
              { label: "New Terminal Here", onClick: () => newHere(active.tabId) },
              ...(active.state === "disconnected" || active.state === "closed"
                ? [{ label: "Reconnect", onClick: () => reconnect(active.tabId) }]
                : []),
              { label: "Close others", onClick: () => closeOthers(active.tabId) },
              { label: "Close disconnected", onClick: () => closeDisconnected() },
              { label: "Close Session", onClick: () => closeTab(active.tabId), danger: true },
              {
                label: "Close all",
                danger: true,
                onClick: () => {
                  const live = tabs.some((tab) => tab.state === "active" || tab.state === "connecting");
                  if (live && !window.confirm("Close all terminal sessions? Connected sessions will disconnect.")) {
                    return;
                  }
                  closeAll();
                },
              },
            ]}
          />
        ) : null}
        <button className="btn btn-sm btn-ghost" type="button" onClick={() => setQs(true)}>
          Quick Switch
        </button>
      </div>
      <p className="term-hint muted">
        Alt+N new terminal · Alt+[ Alt+] tabs · Alt+W close. Shortcuts skip when a dialog is open. They do not bind Ctrl
        combinations used by shells, tmux, or Vim.
      </p>
      <TerminalPane />
      {menu && menuTab ? (
        <div
          ref={menuRef}
          className="menu-panel term-ctx"
          role="menu"
          aria-label={`${menuTab.title} actions`}
          style={{ left: menu.x, top: menu.y }}
          onMouseDown={(event) => event.stopPropagation()}
        >
          <button type="button" role="menuitem" onClick={() => menuAction(() => renameTab(menuTab.tabId, menuTab.title))}>
            Rename
          </button>
          <button type="button" role="menuitem" onClick={() => menuAction(() => newHere(menuTab.tabId))}>
            New Terminal Here
          </button>
          {menuTab.state === "disconnected" || menuTab.state === "closed" ? (
            <button type="button" role="menuitem" onClick={() => menuAction(() => reconnect(menuTab.tabId))}>
              Reconnect
            </button>
          ) : null}
          <hr />
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            onClick={() => menuAction(() => closeTab(menuTab.tabId))}
          >
            Close Session
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={tabs.length < 2}
            onClick={() => menuAction(() => closeOthers(menuTab.tabId))}
          >
            Close Other Sessions
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={menuIndex <= 0}
            onClick={() => menuAction(() => closeToSide(menuTab.tabId, "left"))}
          >
            Close Sessions to the Left
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={menuIndex < 0 || menuIndex >= tabs.length - 1}
            onClick={() => menuAction(() => closeToSide(menuTab.tabId, "right"))}
          >
            Close Sessions to the Right
          </button>
        </div>
      ) : null}
      <QuickSwitch
        open={qs}
        recents={recents}
        hasCurrent={Boolean(active)}
        currentLive={active?.state === "active"}
        onClose={() => setQs(false)}
        onOpenNew={(target) => openNew(target)}
        onReplace={(target) => replaceCurrent(target)}
      />
    </section>
  );
}
