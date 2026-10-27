import { useCallback, useEffect, useMemo, useState } from "react";
import {
  claudeAgentLogin,
  getClaudeAgent,
  putClaudeAgentGrants,
  putClaudeAgentSettings,
  removeClaudeAgent,
  restartClaudeAgent,
  setupClaudeAgent,
  type ClaudeAgentFolder,
  type ClaudeAgentGrant,
  type ClaudeAgentOverview,
  type ClaudeAgentServer,
} from "../api/client";
import { confirmAction } from "../confirm";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { listGameFiles } from "../gameservers/api";

const modeLabel: Record<string, string> = {
  default: "Ask before edits and commands",
  acceptEdits: "Edit files without asking, ask before commands",
  plan: "Plan only, change nothing",
};

const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);

/** ClaudeAgentPage sets up the Claude Agent container, signs it in to Claude and chooses what it may reach. */
export function ClaudeAgentPage() {
  const [data, setData] = useState<ClaudeAgentOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const reload = useCallback(async () => {
    try {
      setData(await getClaudeAgent());
      setError(null);
    } catch (err) {
      setError(errText(err, "Claude Agent could not be loaded"));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const working = data?.state === "creating" || data?.state === "installing" || data?.status?.service === "activating";
  useEffect(() => {
    if (!working) {
      return;
    }
    const timer = window.setInterval(() => void reload(), 4000);
    return () => window.clearInterval(timer);
  }, [working, reload]);

  async function run(label: string, fn: () => Promise<unknown>, done?: string) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await fn();
      if (done) {
        setNotice(done);
      }
      await reload();
    } catch (err) {
      setError(errText(err, `${label} failed`));
    } finally {
      setBusy(false);
    }
  }

  if (!data) {
    return <section className="page">{error ? <ErrorNotice error={error} /> : <LoadingState label="Reading Claude Agent" />}</section>;
  }
  const st = data.status;
  const ready = data.state === "ready";
  return (
    <section className="page page-wide" aria-labelledby="ca-heading">
      <PageHeader
        id="ca-heading"
        title="Claude Agent"
        kicker="Claude Code in its own system container. You chat with it on claude.ai or in the Claude app; it only reaches the game server folders and actions you grant here."
        actions={
          <button className="btn btn-secondary" type="button" onClick={() => void reload()}>
            Refresh
          </button>
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {!data.enabled ? (
        <EmptyState title="Claude Agent is off">
          Turn it on in <Link href="/settings/features">Add Features</Link>, then come back here to set it up.
        </EmptyState>
      ) : (
        <>
          <div className="status-strip">
            <div className="status-tile">
              <span className="label">Container</span>
              <span className="value">
                <StatusBadge status={ready ? "healthy" : data.state === "failed" ? "critical" : data.state ? "warning" : "unknown"} label={data.state || "not set up"} />
              </span>
              <span className="meta">{data.workload_id ? <Link href={`/workloads/${data.workload_id}`}>claude-agent</Link> : "2 vCPU, 2 GB, 16 GB"}</span>
            </div>
            <div className="status-tile">
              <span className="label">Claude account</span>
              <span className="value">{st?.logged_in ? st.account || "signed in" : "not signed in"}</span>
              <span className="meta">{st?.plan ? `${st.plan} plan` : st?.version ? `Claude Code ${st.version}` : ""}</span>
            </div>
            <div className="status-tile">
              <span className="label">Remote Control</span>
              <span className="value">
                <StatusBadge
                  status={st?.service === "active" ? "healthy" : st?.logged_in ? "warning" : "unknown"}
                  label={st?.service === "active" ? "running" : st?.logged_in ? st?.service || "stopped" : "waiting for sign-in"}
                />
              </span>
              <span className="meta">{data.name}</span>
            </div>
            <div className="status-tile">
              <span className="label">Access</span>
              <span className="value">{data.grants.length} server(s)</span>
              <span className="meta">{data.grants.reduce((n, g) => n + g.folders.length, 0)} folder(s)</span>
            </div>
          </div>
          {data.status_error ? <ErrorNotice error={`The agent's container did not answer: ${data.status_error}`} /> : null}

          {!ready ? <SetupPanel data={data} busy={busy} onSetup={(body) => run("Setup", () => setupClaudeAgent(body))} /> : null}
          {ready ? <SignInPanel data={data} busy={busy} run={run} /> : null}
          <GrantsPanel data={data} busy={busy} run={run} />
          {ready ? <SettingsPanel data={data} busy={busy} run={run} /> : null}
          {data.workload_id ? (
            <article className="panel">
              <h2>Remove the agent</h2>
              <p className="muted">
                Takes the agent's access to every folder away, then deletes its container and the Claude sign-in stored in it. Your game server files are not touched.
              </p>
              <button
                className="btn btn-danger"
                type="button"
                disabled={busy}
                onClick={() => {
                  if (confirmAction("Remove the Claude Agent? Its container and Claude sign-in are deleted.")) {
                    void run("Removing the agent", () => removeClaudeAgent(), "The agent was removed.");
                  }
                }}
              >
                Remove agent
              </button>
            </article>
          ) : null}
        </>
      )}
    </section>
  );
}

function SetupPanel({ data, busy, onSetup }: { data: ClaudeAgentOverview; busy: boolean; onSetup: (body: { name: string; permission_mode: string }) => void }) {
  const [name, setName] = useState(data.name || "No-dal");
  const [mode, setMode] = useState(data.permission_mode || "default");
  const working = data.state === "creating" || data.state === "installing";
  return (
    <article className="panel">
      <h2>1. Set up</h2>
      {working ? (
        <LoadingState label={data.state === "creating" ? "Creating the agent's container" : "Installing Claude Code (a few minutes)"} />
      ) : (
        <form
          className="stack"
          onSubmit={(ev) => {
            ev.preventDefault();
            onSetup({ name: name.trim(), permission_mode: mode });
          }}
        >
          <p className="muted">Creates a system container named claude-agent (2 vCPU, 2 GB memory, 16 GB disk) and installs Claude Code in it.</p>
          {data.state === "failed" && data.error ? <ErrorNotice error={`Setup failed: ${data.error}`} /> : null}
          <ModeFields name={name} mode={mode} modes={data.permission_modes} onName={setName} onMode={setMode} />
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy || !name.trim()}>
              {data.state === "failed" ? "Try again" : "Set up agent"}
            </button>
          </div>
        </form>
      )}
      {data.log && data.log.length > 0 ? (
        <pre className="code-block" aria-label="Setup log">
          {data.log.join("\n")}
        </pre>
      ) : null}
    </article>
  );
}

function ModeFields({ name, mode, modes, onName, onMode }: { name: string; mode: string; modes: string[]; onName: (v: string) => void; onMode: (v: string) => void }) {
  return (
    <>
      <div className="field">
        <label className="field-label" htmlFor="ca-name">
          Name on claude.ai
        </label>
        <input id="ca-name" className="field-input" value={name} maxLength={40} onChange={(e) => onName(e.target.value)} />
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ca-mode">
          Permissions in chats
        </label>
        <select id="ca-mode" className="field-input" value={mode} onChange={(e) => onMode(e.target.value)}>
          {modes.map((m) => (
            <option key={m} value={m}>
              {modeLabel[m] ?? m}
            </option>
          ))}
        </select>
      </div>
    </>
  );
}

type Runner = (label: string, fn: () => Promise<unknown>, done?: string) => Promise<void>;

function SignInPanel({ data, busy, run }: { data: ClaudeAgentOverview; busy: boolean; run: Runner }) {
  const [link, setLink] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const st = data.status;
  if (st?.logged_in) {
    return (
      <article className="panel">
        <h2>2. Claude account</h2>
        <p>
          Signed in as <strong>{st.account || "your Claude account"}</strong>
          {st.plan ? ` (${st.plan})` : ""}. Start a chat on{" "}
          <a href="https://claude.ai/code" target="_blank" rel="noopener noreferrer">
            claude.ai/code
          </a>{" "}
          or in the Claude app and pick <strong>{data.name}</strong>.
        </p>
        <div className="btn-row">
          <button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void run("Restart", () => restartClaudeAgent(), "Remote Control restarted.")}>
            Restart Remote Control
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={busy}
            onClick={() => {
              if (confirmAction("Sign the agent out of Claude? Remote Control stops until you sign in again.")) {
                void run("Sign out", () => claudeAgentLogin({ action: "logout" }), "Signed out.");
              }
            }}
          >
            Sign out
          </button>
        </div>
      </article>
    );
  }
  return (
    <article className="panel">
      <h2>2. Sign in to Claude</h2>
      <p className="muted">Remote Control needs a Claude plan that includes Claude Code (Pro or Max).</p>
      {!link ? (
        <button
          className="btn btn-primary"
          type="button"
          disabled={busy || !st?.installed}
          onClick={() =>
            void run("Getting a sign-in link", async () => {
              const res = await claudeAgentLogin({ action: "start" });
              setLink(res.login_url ?? null);
            })
          }
        >
          Get sign-in link
        </button>
      ) : (
        <form
          className="stack"
          onSubmit={(ev) => {
            ev.preventDefault();
            void run(
              "Signing in",
              async () => {
                await claudeAgentLogin({ action: "code", code: code.trim() });
                setLink(null);
                setCode("");
              },
              "Signed in. Remote Control is starting.",
            );
          }}
        >
          <p>
            Open{" "}
            <a href={link} target="_blank" rel="noopener noreferrer">
              the sign-in page
            </a>
            , approve with your Claude account, then paste the code it shows.
          </p>
          <div className="field">
            <label className="field-label" htmlFor="ca-code">
              Code from the sign-in page
            </label>
            <input id="ca-code" className="field-input" autoComplete="off" value={code} onChange={(e) => setCode(e.target.value)} />
          </div>
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy || code.trim().length < 8}>
              Finish sign-in
            </button>
            <button className="btn btn-ghost" type="button" onClick={() => setLink(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </article>
  );
}

function sameGrants(a: ClaudeAgentGrant[], b: ClaudeAgentGrant[]): boolean {
  const norm = (gs: ClaudeAgentGrant[]) =>
    JSON.stringify(
      [...gs]
        .sort((x, y) => x.id.localeCompare(y.id))
        .map((g) => ({ id: g.id, f: [...g.folders].sort((x, y) => x.path.localeCompare(y.path)), l: !!g.log, c: !!g.console, r: !!g.restart })),
    );
  return norm(a) === norm(b);
}

function GrantsPanel({ data, busy, run }: { data: ClaudeAgentOverview; busy: boolean; run: Runner }) {
  const [draft, setDraft] = useState<ClaudeAgentGrant[]>(data.grants);
  useEffect(() => setDraft(data.grants), [data.grants]);
  const changed = !sameGrants(draft, data.grants);
  const servers = useMemo(() => [...data.servers].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" })), [data.servers]);

  function update(id: string, change: (g: ClaudeAgentGrant) => ClaudeAgentGrant | null) {
    setDraft((cur) => {
      const g = cur.find((x) => x.id === id);
      const next = change(g ?? { kind: "gameserver", id, folders: [] });
      const rest = cur.filter((x) => x.id !== id);
      return next ? [...rest, next] : rest;
    });
  }

  return (
    <article className="panel">
      <h2>{data.state === "ready" ? "3. Access" : "Access"}</h2>
      <p className="muted">
        Shared folders appear in the agent under /srv/&lt;server&gt;/. Everything else on the host is out of its reach.
        {data.state !== "ready" ? " Choices saved now take effect when setup finishes." : ""}
      </p>
      {servers.length === 0 ? <EmptyState title="No game servers yet." /> : null}
      {servers.map((srv) => {
        const g = draft.find((x) => x.id === srv.id);
        return (
          <fieldset key={srv.id} className="stack" aria-label={srv.name}>
            <label className="check-row">
              <input
                type="checkbox"
                checked={Boolean(g)}
                onChange={(e) => update(srv.id, () => (e.target.checked ? { kind: "gameserver", id: srv.id, folders: srv.default_folders ?? [] } : null))}
              />
              <strong>{srv.name}</strong> <span className="muted">({srv.game})</span>
            </label>
            {g ? <GrantEditor server={srv} grant={g} onChange={(next) => update(srv.id, () => next)} /> : null}
          </fieldset>
        );
      })}
      <div className="btn-row">
        <button className="btn btn-primary" type="button" disabled={busy || !changed} onClick={() => void run("Saving access", () => putClaudeAgentGrants(draft), "Access saved.")}>
          Save access
        </button>
        {changed ? (
          <button className="btn btn-ghost" type="button" onClick={() => setDraft(data.grants)}>
            Undo changes
          </button>
        ) : null}
      </div>
      {changed && data.state === "ready" ? <p className="muted">Changing folders restarts the agent's container for a few seconds, which ends open chats.</p> : null}
    </article>
  );
}

function GrantEditor({ server, grant, onChange }: { server: ClaudeAgentServer; grant: ClaudeAgentGrant; onChange: (g: ClaudeAgentGrant) => void }) {
  const [picking, setPicking] = useState(false);
  const setFolders = (folders: ClaudeAgentFolder[]) => onChange({ ...grant, folders });
  const folders = [...grant.folders].sort((a, b) => a.path.localeCompare(b.path));
  return (
    <div className="stack">
      {folders.length === 0 ? <p className="muted">No folders shared yet.</p> : null}
      <ul className="plain-list">
        {folders.map((f) => (
          <li key={f.path} className="btn-row">
            <code>{f.path}</code>
            <select
              className="field-input"
              aria-label={`Access to ${f.path}`}
              value={f.write ? "rw" : "ro"}
              onChange={(e) => setFolders(grant.folders.map((x) => (x.path === f.path ? { ...x, write: e.target.value === "rw" } : x)))}
            >
              <option value="rw">Read and write</option>
              <option value="ro">Read only</option>
            </select>
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setFolders(grant.folders.filter((x) => x.path !== f.path))}>
              Remove
            </button>
          </li>
        ))}
      </ul>
      {picking ? (
        <FolderPicker
          serverId={server.id}
          onCancel={() => setPicking(false)}
          onPick={(path) => {
            setPicking(false);
            if (!grant.folders.some((x) => x.path === path)) {
              setFolders([...grant.folders, { path, write: true }]);
            }
          }}
        />
      ) : (
        <div>
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setPicking(true)}>
            Add folder
          </button>
        </div>
      )}
      <div className="btn-row">
        <label className="check-row">
          <input type="checkbox" checked={Boolean(grant.log)} onChange={(e) => onChange({ ...grant, log: e.target.checked })} /> Read the server log
        </label>
        <label className="check-row">
          <input type="checkbox" checked={Boolean(grant.console)} onChange={(e) => onChange({ ...grant, console: e.target.checked })} /> Run console commands
        </label>
        <label className="check-row">
          <input type="checkbox" checked={Boolean(grant.restart)} onChange={(e) => onChange({ ...grant, restart: e.target.checked })} /> Restart the server
        </label>
      </div>
    </div>
  );
}

/** FolderPicker browses a game server's folders, A to Z. */
function FolderPicker({ serverId, onPick, onCancel }: { serverId: string; onPick: (path: string) => void; onCancel: () => void }) {
  const [path, setPath] = useState("");
  const [dirs, setDirs] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    setDirs(null);
    listGameFiles(serverId, path)
      .then((res) => {
        if (!cancelled) {
          setDirs(
            (res.items ?? [])
              .filter((i) => i.dir)
              .map((i) => i.name)
              .sort((a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" })),
          );
          setError(null);
        }
      })
      .catch((err) => !cancelled && setError(errText(err, "Folders could not be listed")));
    return () => {
      cancelled = true;
    };
  }, [serverId, path]);
  const parts = path ? path.split("/") : [];
  return (
    <div className="panel" role="group" aria-label="Choose a folder">
      <p>
        <button className="btn btn-ghost btn-sm" type="button" onClick={() => setPath("")}>
          server
        </button>
        {parts.map((p, i) => (
          <span key={i}>
            /
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setPath(parts.slice(0, i + 1).join("/"))}>
              {p}
            </button>
          </span>
        ))}
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {dirs === null && !error ? <LoadingState label="Listing folders" /> : null}
      {dirs && dirs.length === 0 ? <p className="muted">No folders in here.</p> : null}
      <ul className="plain-list">
        {(dirs ?? []).map((d) => (
          <li key={d}>
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setPath(path ? `${path}/${d}` : d)}>
              {d}/
            </button>
          </li>
        ))}
      </ul>
      <div className="btn-row">
        <button className="btn btn-primary btn-sm" type="button" disabled={!path} onClick={() => onPick(path)}>
          {path ? `Share ${path}` : "Open a folder to share it"}
        </button>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

function SettingsPanel({ data, busy, run }: { data: ClaudeAgentOverview; busy: boolean; run: Runner }) {
  const [name, setName] = useState(data.name);
  const [mode, setMode] = useState(data.permission_mode);
  useEffect(() => {
    setName(data.name);
    setMode(data.permission_mode);
  }, [data.name, data.permission_mode]);
  const changed = name.trim() !== data.name || mode !== data.permission_mode;
  return (
    <article className="panel">
      <h2>Settings</h2>
      <form
        className="stack"
        onSubmit={(ev) => {
          ev.preventDefault();
          void run("Saving settings", () => putClaudeAgentSettings({ name: name.trim(), permission_mode: mode }), "Settings saved; Remote Control restarted.");
        }}
      >
        <ModeFields name={name} mode={mode} modes={data.permission_modes} onName={setName} onMode={setMode} />
        <div className="btn-row">
          <button className="btn btn-primary" type="submit" disabled={busy || !changed || !name.trim()}>
            Save settings
          </button>
        </div>
      </form>
    </article>
  );
}
