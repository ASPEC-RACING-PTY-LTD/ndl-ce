import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { mkdirFile, uploadFile } from "../api/client";
import { Link } from "./Link";
import { joinPath, relName, uploadDirFromCwd } from "../files/paths";
import { shellEscapeAll } from "../files/shell";
import {
  clampTermSize,
  layoutLimit,
  loadTermSize,
  observeTermLayout,
  saveTermSize,
  termStyle,
  type TermSizePref,
} from "../terminal/size";
import { statusLabel } from "../terminal/types";
import { useTerminalWorkspace } from "../terminal/workspace";
import type { TermTab } from "../terminal/types";

function eventPoint(ev: {
  clientX?: number;
  clientY?: number;
  nativeEvent?: { clientX?: number; clientY?: number };
}): { x: number; y: number } | null {
  const x = ev.clientX ?? ev.nativeEvent?.clientX;
  const y = ev.clientY ?? ev.nativeEvent?.clientY;
  if (typeof x !== "number" || typeof y !== "number" || !Number.isFinite(x) || !Number.isFinite(y)) {
    return null;
  }
  return { x, y };
}

function identityMeta(tab: TermTab): string {
  const bits = [tab.target.typeLabel];
  if (tab.target.nodeName && tab.target.kind !== "node") {
    bits.push(tab.target.nodeName);
  }
  if (tab.target.kind === "node") {
    bits.push(tab.state === "active" ? "Connected" : statusLabel(tab.state));
  } else {
    bits.push(tab.target.status === "running" ? "Running" : tab.target.status || statusLabel(tab.state));
  }
  return bits.join(" | ");
}

export function TerminalPane({
  workspaceLink = false,
  heading,
}: {
  workspaceLink?: boolean;
  heading?: string;
}) {
  const {
    tabs,
    activeId,
    attach,
    detach,
    reconnect,
    closeTab,
    send,
    fit,
    dropAbort,
    setTabError,
  } = useTerminalWorkspace();
  const holder = useRef<HTMLDivElement>(null);
  const [dropActive, setDropActive] = useState(false);
  const [uploadNote, setUploadNote] = useState<string | null>(null);
  const [pref, setPref] = useState<TermSizePref>(() => loadTermSize());
  const prefRef = useRef(pref);
  const dragging = useRef(false);
  const drag = useRef<{
    startW: number;
    startH: number;
    originX: number;
    originY: number;
  } | null>(null);
  const [limit, setLimit] = useState({ width: 0, height: 0 });
  const tab = tabs.find((t) => t.tabId === activeId) ?? null;
  const lastFit = useRef({ w: 0, h: 0, id: "" });
  const size = termStyle(pref, limit);
  const slotEls = useRef(new Map<string, HTMLElement>());
  const attachKey = tabs.map((t) => `${t.tabId}:${t.state}:${t.ioSessionId ?? ""}`).join("|");

  const measureLimit = useCallback(() => {
    const el = holder.current;
    if (!el) {
      return;
    }
    const next = layoutLimit(el);
    setLimit((cur) => (cur.width === next.width && cur.height === next.height ? cur : next));
  }, []);

  const applyAndFit = useCallback(() => {
    if (dragging.current) {
      return;
    }
    measureLimit();
    const el = holder.current;
    if (!el || !activeId) {
      return;
    }
    const w = el.clientWidth;
    const h = el.clientHeight;
    if (lastFit.current.id === activeId && lastFit.current.w === w && lastFit.current.h === h) {
      return;
    }
    lastFit.current = { w, h, id: activeId };
    fit(activeId);
  }, [measureLimit, activeId, fit]);

  useLayoutEffect(() => {
    if (!dragging.current) {
      prefRef.current = pref;
    }
    measureLimit();
  }, [pref, measureLimit, activeId]);

  const bindSlot = useCallback(
    (tabId: string, el: HTMLElement | null) => {
      if (el) {
        slotEls.current.set(tabId, el);
        attach(tabId, el);
        return;
      }
      slotEls.current.delete(tabId);
      detach(tabId);
    },
    [attach, detach],
  );

  useLayoutEffect(() => {
    for (const [tabId, el] of slotEls.current) {
      attach(tabId, el);
    }
  }, [attachKey, attach]);

  useLayoutEffect(() => {
    if (!activeId) {
      return;
    }
    const el = holder.current;
    const w = el?.clientWidth ?? 0;
    const h = el?.clientHeight ?? 0;
    if (lastFit.current.id === activeId && lastFit.current.w === w && lastFit.current.h === h) {
      return;
    }
    lastFit.current = { w, h, id: activeId };
    fit(activeId);
  }, [activeId, size.width, size.height, fit, tab?.state]);

  useEffect(() => {
    const el = holder.current;
    if (!el || !activeId) {
      return;
    }
    lastFit.current = { w: 0, h: 0, id: "" };
    applyAndFit();
    const stop = observeTermLayout(el, applyAndFit);
    return () => {
      stop();
    };
  }, [activeId, applyAndFit]);

  async function onDropFiles(list: FileList | null) {
    const files = list ? Array.from(list) : [];
    if (!tab || files.length === 0) {
      return;
    }
    if (tab.state !== "active") {
      setTabError(tab.tabId, "Connect the terminal before dropping files.");
      return;
    }
    const located = uploadDirFromCwd(tab.cwd, tab.jailRoot);
    const destDir = located.path;
    const abort = dropAbort(tab.tabId);
    setTabError(tab.tabId, null);
    setUploadNote(`Uploading 0/${files.length} to ${destDir}`);
    try {
      if (located.fallback) {
        try {
          await mkdirFile(tab.target.kind, tab.target.id, destDir);
        } catch {
          // directory may already exist
        }
      }
      const uploaded: string[] = [];
      for (let i = 0; i < files.length; i += 1) {
        const file = files[i];
        setUploadNote(`Uploading ${i + 1}/${files.length}: ${file.name}`);
        const dest = joinPath(destDir, relName(file.name));
        await uploadFile(tab.target.kind, tab.target.id, dest, file, { signal: abort.signal });
        uploaded.push(dest.startsWith("/") ? dest : `/${dest}`);
      }
      if (located.fallback) {
        setUploadNote(`Uploaded to ${destDir} because the shell cwd is not available.`);
      } else {
        setUploadNote(`Uploaded ${uploaded.length} file(s) to ${destDir}`);
      }
      send(tab.tabId, shellEscapeAll(uploaded));
    } catch (err) {
      setUploadNote(null);
      setTabError(tab.tabId, err instanceof Error ? err.message : "Upload failed");
    }
  }

  function endResize() {
    const wrap = holder.current;
    dragging.current = false;
    drag.current = null;
    wrap?.classList.remove("is-resizing");
    document.body.classList.remove("is-term-resizing");
    document.removeEventListener("selectstart", preventSelect);
    const current = prefRef.current;
    if (current.mode === "manual" && wrap) {
      const max = layoutLimit(wrap);
      const next = clampTermSize(current.width, current.height, max.width, max.height);
      const manual: TermSizePref = { mode: "manual", width: next.width, height: next.height };
      prefRef.current = manual;
      setPref(manual);
      saveTermSize(manual);
    }
    lastFit.current = { w: 0, h: 0, id: "" };
    if (activeId) {
      fit(activeId);
    }
  }

  function preventSelect(ev: Event) {
    ev.preventDefault();
  }

  function resetSize() {
    dragging.current = false;
    drag.current = null;
    document.body.classList.remove("is-term-resizing");
    document.removeEventListener("selectstart", preventSelect);
    holder.current?.classList.remove("is-resizing");
    const next: TermSizePref = { mode: "auto" };
    prefRef.current = next;
    setPref(next);
    saveTermSize(next);
    lastFit.current = { w: 0, h: 0, id: "" };
    measureLimit();
  }

  function onResizePointerDown(event: ReactPointerEvent<HTMLButtonElement>) {
    if (event.button != null && event.button !== 0) {
      return;
    }
    if (event.detail > 1) {
      event.preventDefault();
      resetSize();
      return;
    }
    const wrap = holder.current;
    if (!wrap) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const start = wrap.getBoundingClientRect();
    const origin = eventPoint(event) ?? { x: start.right, y: start.bottom };
    drag.current = { startW: start.width, startH: start.height, originX: origin.x, originY: origin.y };
    dragging.current = true;
    wrap.classList.add("is-resizing");
    document.body.classList.add("is-term-resizing");
    document.addEventListener("selectstart", preventSelect);
    if (event.pointerId != null && event.currentTarget.setPointerCapture) {
      try {
        event.currentTarget.setPointerCapture(event.pointerId);
      } catch {
        // jsdom may not implement capture
      }
    }
  }

  function onResizePointerMove(event: ReactPointerEvent<HTMLButtonElement>) {
    const session = drag.current;
    const wrap = holder.current;
    const point = eventPoint(event);
    if (!session || !wrap || !dragging.current || !point) {
      return;
    }
    event.preventDefault();
    const max = layoutLimit(wrap);
    const next = clampTermSize(session.startW + (point.x - session.originX), session.startH + (point.y - session.originY), max.width, max.height);
    const manual: TermSizePref = { mode: "manual", width: next.width, height: next.height };
    prefRef.current = manual;
    setPref(manual);
    lastFit.current = { w: 0, h: 0, id: "" };
    if (activeId) {
      fit(activeId);
    }
  }

  function onResizePointerUp() {
    if (!dragging.current) {
      return;
    }
    endResize();
  }

  if (!tab) {
    return (
      <div className="term-empty">
        <p>No terminal session open.</p>
        <p className="muted">Use + or Quick Switch to open a session against any authorized target.</p>
      </div>
    );
  }

  const host = tab.target.kind === "node";
  const filesHref = host
    ? `/nodes/${encodeURIComponent(tab.target.id)}/files`
    : `/workloads/${encodeURIComponent(tab.target.id)}/files`;
  const filesHere = `${filesHref}?path=${encodeURIComponent(tab.cwd || "/")}`;
  const backHref = host
    ? `/nodes/${encodeURIComponent(tab.target.id)}`
    : `/workloads/${encodeURIComponent(tab.target.id)}`;

  return (
    <div className={"term-pane" + (host ? " is-host" : "")}>
      {heading ? (
        <h1 id="term-heading" className="term-page-title">
          {heading}
        </h1>
      ) : null}
      <div className={"term-id" + (host ? " is-host" : "")} data-testid="term-identity" data-target-kind={tab.target.kind}>
        <div className="term-id-main">
          {host ? <span className="term-host-badge">Host</span> : null}
          <strong>{tab.target.name}</strong>
          <span className="term-id-kind">{identityMeta(tab)}</span>
        </div>
        <div className="term-id-meta">
          <span className={"term-conn is-" + tab.state}>{statusLabel(tab.state)}</span>
          {tab.cwd ? <code className="term-cwd">{tab.cwd}</code> : null}
          {workspaceLink ? (
            <Link className="btn btn-ghost btn-sm" href="/terminal">
              Workspace
            </Link>
          ) : null}
          <Link className="btn btn-ghost btn-sm" href={backHref}>
            {host ? "Host" : "Workload"}
          </Link>
          {tab.state === "disconnected" || tab.state === "closed" ? (
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => reconnect(tab.tabId)}>
              Reconnect
            </button>
          ) : null}
          {pref.mode === "manual" ? (
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => resetSize()}>
              Reset size
            </button>
          ) : null}
          <Link className="btn btn-ghost btn-sm" href={filesHere}>
            Files here
          </Link>
        </div>
      </div>
      {tab.error ? (
        <p className="banner banner-error" role="alert">
          {tab.error}{" "}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => closeTab(tab.tabId)}>
            Close
          </button>
        </p>
      ) : null}
      {uploadNote ? (
        <p className="banner" role="status">
          {uploadNote}{" "}
          <button
            className="btn btn-sm btn-ghost"
            type="button"
            onClick={() => {
              dropAbort(tab.tabId).abort();
              setUploadNote(null);
              setTabError(tab.tabId, "Upload cancelled");
            }}
          >
            Cancel upload
          </button>
        </p>
      ) : null}
      <div
        className={"term-wrap" + (dropActive ? " is-drop" : "")}
        data-testid="term-wrap"
        data-term-size={pref.mode}
        ref={holder}
        style={{ width: size.width, height: size.height }}
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
        {dropActive ? <div className="term-drop">Drop files to upload into this session directory</div> : null}
        <div className="term-slots">
          {tabs.map((item) => (
            <div
              key={item.tabId}
              className={"term-slot" + (item.tabId === activeId ? " is-active" : "")}
              data-term-slot={item.tabId}
              data-io-session={item.ioSessionId || ""}
              data-active={item.tabId === activeId ? "true" : "false"}
              ref={(el) => bindSlot(item.tabId, el)}
            />
          ))}
        </div>
        <button
          className="term-resize"
          type="button"
          aria-label="Resize terminal. Double-click to fill available space."
          title="Drag to resize. Double-click to fill available space."
          onPointerDown={onResizePointerDown}
          onPointerMove={onResizePointerMove}
          onPointerUp={onResizePointerUp}
          onPointerCancel={onResizePointerUp}
          onDoubleClick={(event) => {
            event.preventDefault();
            resetSize();
          }}
        />
      </div>
    </div>
  );
}
