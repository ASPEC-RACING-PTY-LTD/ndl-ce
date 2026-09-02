import { useEffect, useRef, useState } from "react";
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
    reconnect,
    replaceCurrent,
    nextTab,
    prevTab,
  } = useTerminalWorkspace();
  const [qs, setQs] = useState(false);
  const [menu, setMenu] = useState<{ tabId: string; x: number; y: number } | null>(null);
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
              title={`${tab.title} · ${tab.target.typeLabel} · ${statusLabel(tab.state)}`}
              onClick={() => setActive(tab.tabId)}
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
        {active ? (
          <ActionMenu
            label="Session actions"
            items={[
              { label: "Rename", onClick: () => renameTab(active.tabId, active.title) },
              { label: "New Terminal Here", onClick: () => newHere(active.tabId) },
              ...(active.state === "disconnected" || active.state === "closed"
                ? [{ label: "Reconnect", onClick: () => reconnect(active.tabId) }]
                : []),
              { label: "Close Session", onClick: () => closeTab(active.tabId), danger: true },
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
          className="menu-panel term-ctx"
          role="menu"
          style={{ left: menu.x, top: menu.y }}
          onMouseDown={(event) => event.stopPropagation()}
        >
          <button type="button" role="menuitem" onClick={() => { renameTab(menuTab.tabId, menuTab.title); setMenu(null); }}>
            Rename
          </button>
          <button type="button" role="menuitem" onClick={() => { newHere(menuTab.tabId); setMenu(null); }}>
            New Terminal Here
          </button>
          {menuTab.state === "disconnected" || menuTab.state === "closed" ? (
            <button type="button" role="menuitem" onClick={() => { reconnect(menuTab.tabId); setMenu(null); }}>
              Reconnect
            </button>
          ) : null}
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            onClick={() => {
              closeTab(menuTab.tabId);
              setMenu(null);
            }}
          >
            Close Session
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
