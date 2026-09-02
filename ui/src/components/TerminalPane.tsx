import { useEffect, useRef, useState } from "react";
import { mkdirFile, uploadFile } from "../api/client";
import { Link } from "./Link";
import { joinPath, uploadDirFromCwd } from "../files/paths";
import { shellEscapeAll } from "../files/shell";
import { statusLabel } from "../terminal/types";
import { useTerminalWorkspace } from "../terminal/workspace";
import type { TermTab } from "../terminal/types";

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
  const tab = tabs.find((t) => t.tabId === activeId) ?? null;

  useEffect(() => {
    const el = holder.current;
    if (!el || !activeId) {
      return;
    }
    attach(activeId, el);
    function onResize() {
      if (activeId) {
        fit(activeId);
      }
    }
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      detach(activeId);
    };
  }, [activeId, tab?.state, attach, detach, fit]);

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
        const dest = joinPath(destDir, file.name);
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
  const backHref = host ? "/node" : `/workloads/${encodeURIComponent(tab.target.id)}`;

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
            {host ? "Node" : "Workload"}
          </Link>
          {tab.state === "disconnected" || tab.state === "closed" ? (
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => reconnect(tab.tabId)}>
              Reconnect
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
        ref={holder}
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
      </div>
    </div>
  );
}
