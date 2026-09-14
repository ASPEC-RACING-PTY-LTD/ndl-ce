import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { contentKind, familyTone, formatRam, statusLabel } from "../gameservers/caps";
import { GS_ROOT, gameServerHref, gameServerIdFromPath, gameServerSectionFromPath } from "../nav/gameServers";
import { gameServerTabList } from "../nav/gameServersNav";
import {
  createConsoleFav,
  createGameBackup,
  createGameSchedule,
  createGameUser,
  deleteConsoleFav,
  deleteGameBackup,
  deleteGameFile,
  deleteGameSchedule,
  deleteGameServer,
  deleteGameUser,
  diffGameConfig,
  gameActivity,
  gameConsole,
  gameConsoleHistory,
  gameDiagnostics,
  gamePower,
  gameTuning,
  getGameConfig,
  getGameNetwork,
  getGameNotes,
  getGameResources,
  getGameServer,
  getGameStartup,
  installGameContent,
  listGameBackups,
  listGameContent,
  listGameFiles,
  listGameSchedules,
  listGameUsers,
  mkdirGameFile,
  putGameConfig,
  putGameNetwork,
  putGameNotes,
  putGameResources,
  putGameStartup,
  readGameFile,
  reinstallGameServer,
  restoreGameBackup,
  revertGameConfig,
  searchGameContent,
  sendGameConsole,
  toggleGameContent,
  uninstallGameContent,
  updateGameContent,
  writeGameFile,
} from "../gameservers/api";
import type { GameContentItem, GameServer } from "../gameservers/types";
import { navigate, usePath } from "../router";
import { canMutate } from "../rbac";
import { useSession } from "../session";

export function GameServerDetailPage() {
  const path = usePath();
  const id = gameServerIdFromPath(path) || "";
  const tab = gameServerSectionFromPath(path) || "overview";
  const session = useSession();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const [server, setServer] = useState<GameServer | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const row = await getGameServer(id);
    setServer(row);
  }, [id]);

  useEffect(() => {
    void load().catch((err) => setError(err instanceof ApiError ? err.message : "Could not load server"));
    const t = window.setInterval(() => {
      void load().catch(() => undefined);
    }, 4000);
    return () => window.clearInterval(t);
  }, [load]);

  const tabs = useMemo(() => gameServerTabList(server), [server]);

  useEffect(() => {
    if (!server || !id) {
      return;
    }
    if (!tabs.some((item) => item.id === tab)) {
      navigate(gameServerHref(id), { replace: true });
    }
  }, [id, server, tab, tabs]);

  async function power(action: "start" | "stop" | "restart" | "kill") {
    if (action === "kill" && !window.confirm("Force stop now? Unsaved world data may be lost.")) {
      return;
    }
    setBusy(true);
    try {
      await gamePower(id, action);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Power action failed");
    } finally {
      setBusy(false);
    }
  }

  if (error && !server) {
    return <ErrorState>{error}</ErrorState>;
  }
  if (!server) {
    return <LoadingState label="Loading server" />;
  }

  return (
    <section className={"page gs-page gs-detail " + familyTone(server.family)} aria-labelledby="gs-detail-title">
      <PageHeader
        id="gs-detail-title"
        title={server.name}
        kicker={`${server.template_name || server.game} · ${statusLabel(server.status)} · ${formatRam(server.memory_bytes)}`}
        actions={
          <div className="gs-mobile-controls">
            {mutate && server.status !== "running" ? (
              <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void power("start")}>
                Start
              </button>
            ) : null}
            {mutate && server.status === "running" ? (
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void power("restart")}>
                Restart
              </button>
            ) : null}
            {mutate && server.status === "running" ? (
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void power("stop")}>
                Stop
              </button>
            ) : null}
            {mutate ? (
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void power("kill")}>
                Force stop
              </button>
            ) : null}
            <Link className="btn btn-ghost" href={GS_ROOT}>
              All servers
            </Link>
          </div>
        }
      />
      {error ? <p className="banner banner-error">{error}</p> : null}
      {server.status === "installing" || server.status === "pending" ? (
        <p className="gs-progress" role="status">
          Installing{server.install_phase ? ` · ${server.install_phase}` : ""}. This page refreshes itself.
        </p>
      ) : null}
      {server.error_human ? <p className="banner banner-error">{server.error_human}</p> : null}

      <nav className="gs-tabs" aria-label="Server sections">
        {tabs.map((item) => (
          <Link key={item.id} href={gameServerHref(id, item.id)} className={"gs-tab" + (tab === item.id ? " is-on" : "")}>
            {item.label}
          </Link>
        ))}
      </nav>

      {tab === "overview" ? <OverviewPane server={server} onReload={load} /> : null}
      {tab === "console" ? <ConsolePane id={id} /> : null}
      {tab === "files" ? <FilesPane id={id} /> : null}
      {tab === "config" ? <ConfigPane id={id} /> : null}
      {tab === "startup" ? <StartupPane id={id} /> : null}
      {tab === "network" ? <NetworkPane id={id} /> : null}
      {tab === "resources" ? <ResourcesPane id={id} /> : null}
      {tab === "backups" ? <BackupsPane id={id} /> : null}
      {tab === "schedules" ? <SchedulesPane id={id} /> : null}
      {tab === "mods" || tab === "plugins" || tab === "workshop" ? <ContentPane id={id} kind={contentKind(server) || tab} /> : null}
      {tab === "players" ? (
        <p className="lede">
          Player administration uses the console. Recognised names in the log can be copied into commands. No-DAL does not speak the
          game protocol itself.
        </p>
      ) : null}
      {tab === "databases" ? (
        <p className="lede">
          This template advertises a game database. Open Files or Configuration to edit it. No-DAL does not run a separate database
          manager for this server.
        </p>
      ) : null}
      {tab === "activity" ? <ActivityPane id={id} /> : null}
      {tab === "diagnostics" ? <DiagnosticsPane id={id} /> : null}
      {tab === "settings" ? (
        <div className="gs-settings">
          <NotesPane id={id} />
          <UsersPane id={id} />
          <DangerRow id={id} name={server.name} onReload={load} />
        </div>
      ) : null}
    </section>
  );
}

function OverviewPane({ server, onReload }: { server: GameServer; onReload: () => Promise<void> }) {
  const [tuning, setTuning] = useState<{ title: string; detail: string }[]>([]);
  useEffect(() => {
    void gameTuning(server.id).then((r) => setTuning(r.items ?? [])).catch(() => undefined);
  }, [server.id]);
  return (
    <div className="gs-overview">
      <article className="gs-widget">
        <h2>Status</h2>
        <p className={"gs-status is-" + server.status}>{statusLabel(server.status)}</p>
        <p>{server.template_name}</p>
        <p>
          {server.cpus} CPU · {formatRam(server.memory_bytes)}
        </p>
      </article>
      <article className="gs-widget">
        <h2>Advice</h2>
        {tuning.length === 0 ? <p className="muted">No changes recommended.</p> : null}
        {tuning.map((item) => (
          <p key={item.title}>
            <strong>{item.title}.</strong> {item.detail}
          </p>
        ))}
      </article>
      <article className="gs-widget gs-wide">
        <h2>Diagnostics</h2>
        <p className="muted">Sanitized runtime facts stay on the Diagnostics page.</p>
        <Link className="btn btn-ghost" href={gameServerHref(server.id, "diagnostics")}>
          Open diagnostics
        </Link>
      </article>
      <DangerRow id={server.id} name={server.name} onReload={onReload} />
    </div>
  );
}

function DiagnosticsPane({ id }: { id: string }) {
  const [diag, setDiag] = useState<string>("");
  useEffect(() => {
    void gameDiagnostics(id)
      .then((r) => setDiag(JSON.stringify(r, null, 2)))
      .catch(() => undefined);
  }, [id]);
  return (
    <article className="gs-widget gs-wide">
      <h2>Sanitized diagnostics</h2>
      <pre className="gs-log">{diag || "Collecting"}</pre>
    </article>
  );
}

function DangerRow({ id, name, onReload }: { id: string; name: string; onReload: () => Promise<void> }) {
  return (
    <article className="gs-widget gs-wide">
      <h2>Danger zone</h2>
      <div className="btn-row">
        <button
          type="button"
          className="btn btn-ghost"
          onClick={() => {
            if (window.confirm("Reinstall this server? A rollback point is taken first.")) {
              void reinstallGameServer(id).then(onReload);
            }
          }}
        >
          Reinstall
        </button>
        <button
          type="button"
          className="btn btn-ghost"
          onClick={() => {
            if (window.confirm(`Delete ${name}? This removes the container and files.`)) {
              void deleteGameServer(id).then(() => navigate(GS_ROOT));
            }
          }}
        >
          Delete server
        </button>
      </div>
    </article>
  );
}

function ConsolePane({ id }: { id: string }) {
  const [log, setLog] = useState("");
  const [cmd, setCmd] = useState("");
  const [hist, setHist] = useState<string[]>([]);
  const [favs, setFavs] = useState<{ id: string; name: string; command: string }[]>([]);
  const [q, setQ] = useState("");
  const reload = useCallback(async () => {
    const [con, history] = await Promise.all([gameConsole(id), gameConsoleHistory(id, q)]);
    setLog(con.log);
    setFavs(con.favorites ?? []);
    setHist(history.items ?? []);
  }, [id, q]);
  useEffect(() => {
    void reload().catch(() => undefined);
    const t = window.setInterval(() => void reload().catch(() => undefined), 2500);
    return () => window.clearInterval(t);
  }, [reload]);
  return (
    <div className="gs-console">
      <pre className="gs-log" aria-live="polite">
        {log || "No live output yet. Start the server to see the console. Install notes stay under What changed."}
      </pre>
      <form
        className="gs-console-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (!cmd.trim()) {
            return;
          }
          void sendGameConsole(id, cmd).then(() => {
            setCmd("");
            return reload();
          });
        }}
      >
        <input className="field-input" value={cmd} onChange={(e) => setCmd(e.target.value)} placeholder="Type a command" aria-label="Console command" />
        <button className="btn btn-primary" type="submit">
          Send
        </button>
        <button
          className="btn btn-ghost"
          type="button"
          onClick={() => {
            const name = window.prompt("Name this command");
            if (name && cmd) {
              void createConsoleFav(id, name, cmd).then(reload);
            }
          }}
        >
          Save
        </button>
      </form>
      <div className="gs-favs">
        {favs.map((f) => (
          <span key={f.id} className="gs-fav">
            <button type="button" className="gs-chip" onClick={() => void sendGameConsole(id, f.command).then(reload)}>
              {f.name}
            </button>
            <button type="button" className="gs-x" onClick={() => void deleteConsoleFav(id, f.id).then(reload)} aria-label="Remove favourite">
              ×
            </button>
          </span>
        ))}
      </div>
      <label>
        Search history
        <input className="field-input" value={q} onChange={(e) => setQ(e.target.value)} />
      </label>
      <ul className="gs-hist">
        {hist.slice().reverse().map((line, i) => (
          <li key={i}>
            <button type="button" onClick={() => setCmd(line)}>
              {line}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function FilesPane({ id }: { id: string }) {
  const [cwd, setCwd] = useState("");
  const [items, setItems] = useState<{ name: string; dir: boolean; size: number }[]>([]);
  const [file, setFile] = useState("");
  const [content, setContent] = useState("");
  const [dirty, setDirty] = useState(false);
  const loadDir = useCallback(
    async (path: string) => {
      const list = await listGameFiles(id, path);
      setCwd(path);
      setItems(list.items ?? []);
    },
    [id],
  );
  useEffect(() => {
    void loadDir("").catch(() => undefined);
  }, [loadDir]);
  return (
    <div className="gs-files">
      <p className="muted">{cwd || "/"}</p>
      <div className="btn-row">
        <button type="button" className="btn btn-ghost" onClick={() => void loadDir(cwd.includes("/") ? cwd.replace(/\/[^/]+$/, "") : "")}>
          Up
        </button>
        <button
          type="button"
          className="btn btn-ghost"
          onClick={() => {
            const name = window.prompt("Folder name");
            if (name) {
              void mkdirGameFile(id, cwd ? `${cwd}/${name}` : name).then(() => loadDir(cwd));
            }
          }}
        >
          New folder
        </button>
      </div>
      <ul className="gs-file-list">
        {items.map((item) => (
          <li key={item.name}>
            <button
              type="button"
              onClick={() => {
                const next = cwd ? `${cwd}/${item.name}` : item.name;
                if (item.dir) {
                  void loadDir(next);
                  return;
                }
                if (dirty && !window.confirm("Discard unsaved file changes?")) {
                  return;
                }
                void readGameFile(id, next).then((r) => {
                  setFile(next);
                  setContent(r.content);
                  setDirty(false);
                });
              }}
            >
              {item.dir ? "📁" : "📄"} {item.name}
            </button>
            {!item.dir ? (
              <button type="button" className="gs-x" onClick={() => void deleteGameFile(id, cwd ? `${cwd}/${item.name}` : item.name).then(() => loadDir(cwd))}>
                ×
              </button>
            ) : null}
          </li>
        ))}
      </ul>
      {file ? (
        <label>
          {file}
          <textarea
            className="field-input gs-editor"
            value={content}
            onChange={(e) => {
              setContent(e.target.value);
              setDirty(true);
            }}
          />
          <button
            type="button"
            className="btn btn-primary"
            onClick={() =>
              void writeGameFile(id, file, content).then(() => {
                setDirty(false);
              })
            }
          >
            Save file
          </button>
        </label>
      ) : null}
    </div>
  );
}

function ConfigPane({ id }: { id: string }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [settings, setSettings] = useState<{ id: string; label: string; help?: string; kind: string; options?: string[]; restart?: boolean; advanced?: boolean }[]>([]);
  const [diff, setDiff] = useState<{ label: string; from: string; to: string; restart?: boolean }[]>([]);
  const [advanced, setAdvanced] = useState(false);
  useEffect(() => {
    void getGameConfig(id).then((r) => {
      setValues(r.values ?? {});
      setSettings(r.settings ?? []);
    });
  }, [id]);
  return (
    <div className="gs-form">
      <label className="gs-check">
        <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} />
        Show advanced
      </label>
      {settings
        .filter((s) => advanced || !s.advanced)
        .map((s) => (
          <label key={s.id}>
            {s.label}
            {s.help ? <span className="field-hint">{s.help}</span> : null}
            {s.restart ? <span className="gs-restart">Needs restart</span> : null}
            {s.kind === "select" ? (
              <select className="field-input" value={values[s.id] ?? ""} onChange={(e) => setValues((cur) => ({ ...cur, [s.id]: e.target.value }))}>
                {(s.options ?? []).map((opt) => (
                  <option key={opt}>{opt}</option>
                ))}
              </select>
            ) : s.kind === "toggle" ? (
              <select className="field-input" value={values[s.id] ?? ""} onChange={(e) => setValues((cur) => ({ ...cur, [s.id]: e.target.value }))}>
                <option value="true">Yes</option>
                <option value="false">No</option>
              </select>
            ) : (
              <input className="field-input" value={values[s.id] ?? ""} onChange={(e) => setValues((cur) => ({ ...cur, [s.id]: e.target.value }))} />
            )}
          </label>
        ))}
      <div className="btn-row">
        <button type="button" className="btn btn-ghost" onClick={() => void diffGameConfig(id, values).then((r) => setDiff(r.changes ?? []))}>
          Preview changes
        </button>
        <button type="button" className="btn btn-primary" onClick={() => void putGameConfig(id, values)}>
          Save settings
        </button>
        <button type="button" className="btn btn-ghost" onClick={() => void revertGameConfig(id)}>
          Undo last save
        </button>
      </div>
      {diff.length > 0 ? (
        <ul>
          {diff.map((c) => (
            <li key={c.label}>
              {c.label}: {c.from || "(empty)"} → {c.to || "(empty)"}
              {c.restart ? " (restart)" : ""}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function StartupPane({ id }: { id: string }) {
  const [startup, setStartup] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  const [vars, setVars] = useState<{ env: string; name: string; description?: string; secret?: boolean }[]>([]);
  useEffect(() => {
    void getGameStartup(id).then((r) => {
      setStartup(r.startup);
      setVars(r.variables ?? []);
      const next: Record<string, string> = {};
      for (const v of r.variables ?? []) {
        next[v.env] = v.value;
      }
      setEnv(next);
    });
  }, [id]);
  return (
    <div className="gs-form">
      <label>
        Startup command
        <textarea className="field-input gs-editor" value={startup} onChange={(e) => setStartup(e.target.value)} />
      </label>
      {vars.map((v) => (
        <label key={v.env}>
          {v.name}
          <span className="field-hint">{v.description}</span>
          <input
            className="field-input"
            type={v.secret ? "password" : "text"}
            value={env[v.env] ?? ""}
            onChange={(e) => setEnv((cur) => ({ ...cur, [v.env]: e.target.value }))}
          />
        </label>
      ))}
      <button type="button" className="btn btn-primary" onClick={() => void putGameStartup(id, { startup, env })}>
        Save startup
      </button>
    </div>
  );
}

function NetworkPane({ id }: { id: string }) {
  const [ports, setPorts] = useState<{ name?: string; container_port: number; host_port?: number; protocol?: string }[]>([]);
  useEffect(() => {
    void getGameNetwork(id).then((r) => setPorts(r.ports ?? []));
  }, [id]);
  return (
    <div className="gs-form">
      {ports.map((p, i) => (
        <label key={i}>
          {p.name || "Port"} ({p.protocol || "tcp"})
          <input
            className="field-input"
            type="number"
            value={p.host_port || p.container_port}
            onChange={(e) =>
              setPorts((cur) => cur.map((row, idx) => (idx === i ? { ...row, host_port: Number(e.target.value) } : row)))
            }
          />
        </label>
      ))}
      <button type="button" className="btn btn-primary" onClick={() => void putGameNetwork(id, ports)}>
        Save ports
      </button>
    </div>
  );
}

function ResourcesPane({ id }: { id: string }) {
  const [cpus, setCpus] = useState(1);
  const [mem, setMem] = useState(1024);
  const [disk, setDisk] = useState(4096);
  useEffect(() => {
    void getGameResources(id).then((r) => {
      setCpus(r.cpus);
      setMem(Math.round(r.memory_bytes / (1024 * 1024)));
      setDisk(Math.round(r.disk_bytes / (1024 * 1024)));
    });
  }, [id]);
  return (
    <div className="gs-form">
      <label>
        CPUs
        <input className="field-input" type="number" value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
      </label>
      <label>
        RAM (MiB)
        <input className="field-input" type="number" value={mem} onChange={(e) => setMem(Number(e.target.value))} />
      </label>
      <label>
        Disk (MiB)
        <input className="field-input" type="number" value={disk} onChange={(e) => setDisk(Number(e.target.value))} />
      </label>
      <button
        type="button"
        className="btn btn-primary"
        onClick={() => void putGameResources(id, { cpus, memory_bytes: mem * 1024 * 1024, disk_bytes: disk * 1024 * 1024 })}
      >
        Save resources
      </button>
    </div>
  );
}

function BackupsPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id: string; name: string; bytes: number; created_at: string }[]>([]);
  const reload = useCallback(() => listGameBackups(id).then((r) => setItems(r.items ?? [])), [id]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return (
    <div>
      <button type="button" className="btn btn-primary" onClick={() => void createGameBackup(id, "Manual").then(reload)}>
        Create backup
      </button>
      <ul className="gs-list">
        {items.map((b) => (
          <li key={b.id}>
            <span>
              {b.name} · {Math.round(b.bytes / 1024)} KiB
            </span>
            <button type="button" className="btn btn-ghost" onClick={() => void restoreGameBackup(id, b.id)}>
              Restore
            </button>
            <button type="button" className="btn btn-ghost" onClick={() => void deleteGameBackup(id, b.id).then(reload)}>
              Delete
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function SchedulesPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id: string; name: string; action: string; cron: string }[]>([]);
  const [name, setName] = useState("Nightly backup");
  const reload = useCallback(() => listGameSchedules(id).then((r) => setItems(r.items ?? [])), [id]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return (
    <div className="gs-form">
      <label>
        Name
        <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <button type="button" className="btn btn-primary" onClick={() => void createGameSchedule(id, { name, action: "backup", cron: "nightly" }).then(reload)}>
        Add nightly backup
      </button>
      <ul className="gs-list">
        {items.map((s) => (
          <li key={s.id}>
            {s.name} · {s.action} · {s.cron}
            <button type="button" className="btn btn-ghost" onClick={() => void deleteGameSchedule(id, s.id).then(reload)}>
              Delete
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ContentPane({ id, kind }: { id: string; kind: string }) {
  const [installed, setInstalled] = useState<GameContentItem[]>([]);
  const [hits, setHits] = useState<GameContentItem[]>([]);
  const [q, setQ] = useState("");
  const [warn, setWarn] = useState("");
  const reload = useCallback(() => listGameContent(id).then((r) => setInstalled(r.items ?? [])), [id]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return (
    <div className="gs-content">
      <h2>{kind[0].toUpperCase() + kind.slice(1)}</h2>
      {kind === "addons" ? (
        <p className="lede">Search by Steam Workshop numeric ID. Steam does not offer a public full-text catalogue without an API key.</p>
      ) : null}
      <form
        className="gs-toolbar"
        onSubmit={(event) => {
          event.preventDefault();
          void searchGameContent(id, q).then((r) => setHits(r.items ?? []));
        }}
      >
        <input className="field-input" value={q} onChange={(e) => setQ(e.target.value)} placeholder={kind === "addons" ? "Workshop ID" : `Search ${kind}`} />
        <button className="btn btn-primary" type="submit">
          Search
        </button>
      </form>
      {warn ? <p className="banner banner-error">{warn}</p> : null}
      <div className="gs-board is-grid">
        {hits.map((hit) => (
          <article key={hit.external_id || hit.name} className="gs-card">
            <h3>{hit.name}</h3>
            <p className="gs-meta">{hit.summary}</p>
            <button
              type="button"
              className="btn btn-primary"
              onClick={() =>
                void installGameContent(id, hit.external_id || "").then((r) => {
                  if (r.missing_dependencies?.length) {
                    setWarn("Also needed: " + r.missing_dependencies.join(", "));
                  }
                  return reload();
                })
              }
            >
              Install
            </button>
          </article>
        ))}
      </div>
      <h3>Installed</h3>
      <ul className="gs-list">
        {installed.map((item) => (
          <li key={item.id}>
            <span>
              {item.name} {item.version ? `· ${item.version}` : ""} {item.enabled === false ? "(off)" : ""}
            </span>
            <button type="button" className="btn btn-ghost" onClick={() => void toggleGameContent(id, item.id || "", item.enabled === false).then(reload)}>
              {item.enabled === false ? "Enable" : "Disable"}
            </button>
            <button
              type="button"
              className="btn btn-ghost"
              onClick={() =>
                void updateGameContent(id, item.id || "").then((r) => {
                  setWarn(r.warning || "");
                  return reload();
                })
              }
            >
              Update
            </button>
            <button type="button" className="btn btn-ghost" onClick={() => void uninstallGameContent(id, item.id || "").then(reload)}>
              Remove
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function UsersPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ user_id: string; username: string; grants: string[] }[]>([]);
  const [username, setUsername] = useState("");
  const reload = useCallback(() => listGameUsers(id).then((r) => setItems(r.items ?? [])), [id]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return (
    <div className="gs-form">
      <label>
        Username
        <input className="field-input" value={username} onChange={(e) => setUsername(e.target.value)} />
      </label>
      <button type="button" className="btn btn-primary" onClick={() => void createGameUser(id, username, ["view", "console", "power"]).then(reload)}>
        Grant console and power
      </button>
      <ul className="gs-list">
        {items.map((u) => (
          <li key={u.user_id}>
            {u.username} · {(u.grants || []).join(", ")}
            <button type="button" className="btn btn-ghost" onClick={() => void deleteGameUser(id, u.user_id).then(reload)}>
              Revoke
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ActivityPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ summary: string; created_at: string; detail?: string }[]>([]);
  useEffect(() => {
    void gameActivity(id).then((r) => setItems(r.items ?? []));
  }, [id]);
  return (
    <ul className="gs-list">
      {items.map((e, i) => (
        <li key={i}>
          <span>
            {e.summary}
            {e.detail ? ` · ${e.detail}` : ""}
          </span>
          <span className="muted">{e.created_at}</span>
        </li>
      ))}
    </ul>
  );
}

function NotesPane({ id }: { id: string }) {
  const [notes, setNotes] = useState("");
  useEffect(() => {
    void getGameNotes(id).then((r) => setNotes(r.notes || ""));
  }, [id]);
  return (
    <div className="gs-form">
      <label>
        Private operator notes
        <textarea className="field-input gs-editor" value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>
      <button type="button" className="btn btn-primary" onClick={() => void putGameNotes(id, notes)}>
        Save notes
      </button>
    </div>
  );
}
