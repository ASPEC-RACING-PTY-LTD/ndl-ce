import { confirmAction } from "../confirm";
import { Fragment, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError } from "../api/client";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { FilesBrowser } from "../components/FilesBrowser";
import { GameConsoleTerminal } from "../components/GameConsoleTerminal";
import { contentKind, familyTone, formatRam, hasCap, statusLabel } from "../gameservers/caps";
import { GS_ROOT, gameServerAnchorFromPath, gameServerHref, gameServerIdFromPath, gameServerSectionFromPath } from "../nav/gameServers";
import { gameServerQueryKey } from "../nav/GameServerFrame";
import { gameServerTabList, gameTabAllow } from "../nav/gameServersNav";
import {
  createGameBackup,
  createGameSchedule,
  deleteGameBackup,
  deleteGameSchedule,
  deleteGameServer,
  diffGameConfig,
  gameActivity,
  gameDiagnostics,
  gameTuning,
  getGameConfig,
  getGameNetwork,
  getGameNotes,
  getGameResources,
  getGameServer,
  getGameStartup,
  getWorkshopSync,
  putWorkshopSync,
  checkWorkshopSync,
  type GameBackupJob,
  type GameBuildState,
  type GameDatabase,
  type GameBackupScope,
  type WorkshopSync,
  installGameContent,
  listGameBackups,
  getGameBuild,
  putGameBuild,
  getGameDatabase,
  getGameDatabaseCredentials,
  putGameDatabase,
  gameDatabaseAction,
  removeGameDatabase,
  getGameBackupScope,
  putGameBackupScope,
  listGameContent,
  listGameSchedules,
  putGameConfig,
  putGameNetwork,
  putGameNotes,
  putGameResources,
  putGameStartup,
  reinstallGameServer,
  restoreGameBackup,
  revertGameConfig,
  searchGameContent,
  toggleGameContent,
  uninstallGameContent,
  updateGameContent,
} from "../gameservers/api";
import type { GameContentItem, GameServer } from "../gameservers/types";
import { formatBytes, formatRelative, formatWhen } from "../format";
import { useQuery } from "../query";
import { navigate, usePath } from "../router";
import { ErrorNotice } from "../components/ErrorNotice";
import { CurseForgeKey } from "../gameservers/CurseForgeKey";
import { DatabaseManager } from "../gameservers/DatabaseManager";
import { GameDDoSPane } from "../gameservers/GameDDoSPane";
import { GameEdgePane } from "../gameservers/GameEdgePane";
import { GamePlayersPane } from "../gameservers/GamePlayersPane";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { canOnItem, hasRoleAccess } from "../rbac";
import { useSession } from "../session";
import { Segmented } from "../ui/Segmented";

const errText = (err: unknown, fallback: string) => (err instanceof ApiError || err instanceof Error ? err.message : fallback);

export function GameServerDetailPage() {
  const path = usePath();
  const id = gameServerIdFromPath(path) || "";
  const tab = gameServerSectionFromPath(path) || "overview";
  const anchor = gameServerAnchorFromPath(path);
  // Shares the bar's query, so the page and GameServerFrame show one copy of the server.
  const q = useQuery(gameServerQueryKey(id), () => getGameServer(id), 4000);
  const server = q.data?.id === id ? q.data : undefined;
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const roleAccess = hasRoleAccess(user);
  const itemPerm = useCallback((perm: string) => canOnItem(user, "gameserver", id, perm), [user, id]);
  const tabs = useMemo(() => gameServerTabList(server, gameTabAllow(roleAccess, itemPerm)), [server, roleAccess, itemPerm]);

  useEffect(() => {
    if (server && id && !tabs.some((item) => item.id === tab)) {
      navigate(gameServerHref(id), { replace: true });
    }
  }, [id, server, tab, tabs]);

  // What this person may do here: everything a role allows (the server
  // checks it), or what their grant on this one server holds.
  const access: GameAccess = { role: roleAccess, can: (perm) => roleAccess || itemPerm(perm) };

  if (q.error && !server) {
    return <ErrorState>{q.error}</ErrorState>;
  }
  if (!server) {
    return <LoadingState label="Loading server" />;
  }

  return (
    <section className={"page gs-page gs-detail " + familyTone(server.family)} aria-labelledby="gs-detail-title">
      {/* The server bar above names the server; the heading stays for screen readers. */}
      <h1 id="gs-detail-title" className="visually-hidden">
        {server.name}
      </h1>
      {server.status === "installing" || server.status === "pending" ? (
        <p className="gs-progress" role="status">
          Installing{server.install_phase ? ` · ${server.install_phase}` : ""}. This page refreshes itself.
        </p>
      ) : null}
      {server.error_human ? <ErrorNotice error={server.error_human} /> : null}

      {tab === "overview" ? <OverviewPane server={server} onReload={q.reload} access={access} /> : null}
      {tab === "console" ? <GameConsoleTerminal key={id} id={id} name={server.name} /> : null}
      {tab === "files" ? <FilesBrowser key={id} kind="game" id={id} embedded onTerminalHere={() => navigate(gameServerHref(id, "console"))} /> : null}
      {tab === "mods" || tab === "plugins" || tab === "workshop" ? <ContentPane id={id} kind={contentKind(server) || tab} /> : null}
      {tab === "players" ? <GamePlayersPane key={id} id={id} /> : null}
      {tab === "backups" ? <BackupsAndSchedules server={server} access={access} /> : null}
      {tab === "databases" ? <DatabasePane key={id} id={id} serverRunning={server.status === "running"} /> : null}
      {tab === "settings" ? <SettingsPane server={server} anchor={anchor} access={access} /> : null}
    </section>
  );
}

/** GameAccess says whether the person has a role, and what they may do on this server. */
type GameAccess = { role: boolean; can: (perm: string) => boolean };

/* ---------------- Overview ---------------- */

function OverviewPane({ server, onReload, access }: { server: GameServer; onReload: () => Promise<unknown>; access: GameAccess }) {
  const [tuning, setTuning] = useState<{ title: string; detail: string }[]>([]);
  useEffect(() => {
    void gameTuning(server.id)
      .then((r) => setTuning(r.items ?? []))
      .catch(() => undefined);
  }, [server.id]);
  const port = server.ports?.find((p) => p.primary) ?? server.ports?.[0];
  return (
    <div className="gs-overview">
      <article className="gs-widget">
        <h2>Status</h2>
        <p className={"gs-status is-" + server.status}>{statusLabel(server.status)}</p>
        <dl className="gs-facts">
          <dt>Template</dt>
          <dd>{server.template_name || server.game}</dd>
          <dt>CPU</dt>
          <dd>{server.cpus}</dd>
          <dt>RAM</dt>
          <dd>{formatRam(server.memory_bytes)}</dd>
          {server.disk_bytes ? (
            <>
              <dt>Disk</dt>
              <dd>{formatBytes(server.disk_bytes)}</dd>
            </>
          ) : null}
          {port ? (
            <>
              <dt>Port</dt>
              <dd>
                {port.host_port || port.container_port}/{port.protocol || "tcp"}
              </dd>
            </>
          ) : null}
        </dl>
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
      {access.can("gameserver.reinstall") ? <DangerRow server={server} onReload={onReload} canDelete={access.role} /> : null}
    </div>
  );
}

function DangerRow({ server, onReload, canDelete }: { server: GameServer; onReload: () => Promise<unknown>; canDelete: boolean }) {
  return (
    <article className="gs-widget gs-wide gs-danger">
      <div>
        <h2>Danger zone</h2>
        <p className="muted">
          {canDelete ? "Reinstall takes a rollback point first. Delete removes the container and its files." : "Reinstall takes a rollback point first."}
        </p>
      </div>
      <div className="btn-row is-flush">
        <button
          type="button"
          className="btn btn-sm btn-ghost"
          onClick={() => {
            if (confirmAction("Reinstall this server? A rollback point is taken first.")) {
              void reinstallGameServer(server.id).then(onReload);
            }
          }}
        >
          Reinstall
        </button>
        {canDelete ? (
          <button
            type="button"
            className="btn btn-sm btn-danger"
            onClick={() => {
              if (confirmAction(`Delete ${server.name}? This removes the container and files.`)) {
                void deleteGameServer(server.id).then(() => navigate(GS_ROOT));
              }
            }}
          >
            Delete server
          </button>
        ) : null}
      </div>
    </article>
  );
}

/* ---------------- Backups & Schedules ---------------- */

function BackupsAndSchedules({ server, access }: { server: GameServer; access: GameAccess }) {
  return (
    <div className="gs-split">
      {hasCap(server, "backups") && access.can("gameserver.backup") ? <BackupsPane id={server.id} /> : null}
      {hasCap(server, "backups") && access.can("gameserver.backup") ? <BackupScopePane id={server.id} /> : null}
      {hasCap(server, "schedules") && access.can("gameserver.schedule") ? <SchedulesPane id={server.id} /> : null}
    </div>
  );
}

function BackupsPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id: string; name: string; reason?: string; bytes: number; created_at: string }[]>([]);
  const [job, setJob] = useState<GameBackupJob | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(
    () =>
      listGameBackups(id)
        .then((r) => {
          setItems(r.items ?? []);
          setJob(r.job ?? null);
        })
        .catch((err) => setError(errText(err, "Could not load backups"))),
    [id],
  );
  useEffect(() => {
    void reload();
  }, [reload]);
  // A backup of a large server takes many minutes and runs in the
  // background; check on it until it finishes.
  const running = Boolean(job?.running);
  useEffect(() => {
    if (!running) {
      return;
    }
    const t = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(t);
  }, [running, reload]);
  const busy = running || starting;
  const act = (fn: () => Promise<unknown>) => {
    setError(null);
    setStarting(true);
    void fn()
      .then(reload)
      .catch((err) => setError(errText(err, "Action failed")))
      .finally(() => setStarting(false));
  };
  const jobVerb = job?.op === "restore" ? "Restoring" : job?.op === "database" ? "Installing the database" : "Backing up";
  return (
    <article className="gs-widget" id="gs-sec-backups">
      <header className="gs-widget-head">
        <h2>Backups</h2>
        <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={() => act(() => createGameBackup(id, "Manual"))}>
          {running ? `${jobVerb}…` : starting ? "Starting…" : "Create backup"}
        </button>
      </header>
      {job?.running ? (
        <p className="muted" role="status">
          {jobVerb}
          {job.op === "restore" ? ` ${job.name}` : job.op === "backup" ? " the server" : ""}, started {formatRelative(job.started_at)}. A large server
          can take 15 minutes or more. You can leave this page.
        </p>
      ) : null}
      {job && !job.running && job.error ? (
        <ErrorNotice error={`${job.op === "restore" ? "Restore" : job.op === "database" ? "Database install" : "Backup"} failed: ${job.error}`} />
      ) : null}
      {job && !job.running && !job.error && job.op === "restore" ? (
        <p className="muted" role="status">
          Restored {job.name} {formatRelative(job.finished_at)}. Start the server when you are ready.
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {items.length === 0 ? <p className="muted">No backups yet.</p> : null}
      <ul className="gs-list">
        {items.map((b) => (
          <li key={b.id}>
            <span>
              <strong>{b.name}</strong>
              <span className="meta">
                {" "}
                · {formatBytes(b.bytes)} · {formatWhen(b.created_at)}
              </span>
            </span>
            <span className="btn-row is-flush">
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={busy}
                onClick={() => {
                  if (confirmAction(`Restore ${b.name}? The server's files are replaced.`)) {
                    act(() => restoreGameBackup(id, b.id));
                  }
                }}
              >
                Restore
              </button>
              <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => act(() => deleteGameBackup(id, b.id))}>
                Delete
              </button>
            </span>
          </li>
        ))}
      </ul>
    </article>
  );
}

/* ---------------- Game build ---------------- */

// GameBuildPane switches a server between its builds (Garry's Mod 32-bit
// and 64-bit). The server must be stopped; files that are missing download
// the next time it starts.
function GameBuildPane({ id }: { id: string }) {
  const [state, setState] = useState<GameBuildState | null>(null);
  const [choice, setChoice] = useState("");
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    getGameBuild(id)
      .then((s) => {
        setState(s);
        setChoice(s.current);
      })
      .catch((err) => setError(errText(err, "Could not load the build")));
  }, [id]);
  if (error && !state) {
    return <ErrorNotice error={error} />;
  }
  if (!state) {
    return <p className="muted">Loading</p>;
  }
  if (!state.supported) {
    return <p className="muted">This game has one build.</p>;
  }
  const picked = state.options.find((o) => o.id === choice);
  const save = () => {
    setSaving(true);
    setError(null);
    setNote(null);
    putGameBuild(id, choice)
      .then((s) => {
        setState(s);
        setChoice(s.current);
        const opt = s.options.find((o) => o.id === s.current);
        setNote(
          opt && !opt.installed
            ? `Saved. The ${opt.label} files download when you next start the server, then it starts by itself. That takes a few minutes.`
            : "Saved. It takes effect the next time the server starts.",
        );
      })
      .catch((err) => setError(errText(err, "Could not save")))
      .finally(() => setSaving(false));
  };
  return (
    <div className="gs-form">
      {state.custom ? (
        <p className="muted">The startup command starts with its own launcher, so the build is set there.</p>
      ) : (
        <>
          <label>
            Build
            <select className="field-input" value={choice} onChange={(e) => setChoice(e.target.value)} disabled={state.running || saving}>
              {state.options.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.label}
                  {o.installed ? "" : " (downloads on next start)"}
                </option>
              ))}
            </select>
            <span className="field-hint">
              {state.running
                ? "Stop the server to change its build."
                : "Your addons, data and config stay as they are. Only the game's own files change."}
            </span>
          </label>
          {error ? <ErrorNotice error={error} /> : null}
          {note ? (
            <p className="muted" role="status">
              {note}
            </p>
          ) : null}
          <div className="btn-row is-flush">
            <button type="button" className="btn btn-sm btn-primary" onClick={save} disabled={state.running || saving || !picked || choice === state.current}>
              {saving ? "Saving…" : "Save"}
            </button>
          </div>
        </>
      )}
    </div>
  );
}

/* ---------------- MariaDB ---------------- */

const DB_STATE_LABEL: Record<GameDatabase["state"], string> = {
  none: "Not set up",
  configured: "Ready to install",
  installing: "Installing",
  running: "Running",
  stopped: "Stopped",
  missing: "Installed, but its container is gone. Install again to bring it back with its data.",
};

// DatabasePane gives the server its own MariaDB. The owner picks the
// database name; the user and password are generated and kept here.
function DatabasePane({ id, serverRunning }: { id: string; serverRunning: boolean }) {
  const [db, setDb] = useState<GameDatabase | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState("");
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(
    () =>
      getGameDatabase(id)
        .then((d) => {
          setDb(d);
          setName((cur) => cur || d.database || "");
        })
        .catch((err) => setError(errText(err, "Could not load the database"))),
    [id],
  );
  useEffect(() => {
    void reload();
  }, [reload]);
  const installing = Boolean(db?.job?.running && db.job.op === "database");
  useEffect(() => {
    if (!installing) {
      return;
    }
    const t = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(t);
  }, [installing, reload]);
  const act = (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    void fn()
      .then(reload)
      .catch((err) => setError(errText(err, "Action failed")))
      .finally(() => setBusy(false));
  };
  if (!db) {
    return error ? <ErrorNotice error={error} /> : <LoadingState label="Loading the database" />;
  }
  const installed = Boolean(db.installed_at);
  const state = installing ? "installing" : db.state;
  const lastJob = db.job && !db.job.running && db.job.op === "database" ? db.job : null;
  const rows: [string, ReactNode][] = db.database
    ? [
        ["Host", db.host],
        ["Port", String(db.port ?? "")],
        ...(db.socket ? ([["Socket", <code key="sock">{db.socket}</code>]] as [string, ReactNode][]) : []),
        ["Database", db.database],
        ["Username", db.user],
        [
          "Password",
          password ? (
            <span className="btn-row is-flush">
              <code>{password}</code>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => void navigator.clipboard?.writeText(password)}>
                Copy
              </button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setPassword(null)}>
                Hide
              </button>
            </span>
          ) : (
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() =>
                getGameDatabaseCredentials(id)
                  .then((c) => setPassword(c.password ?? ""))
                  .catch((err) => setError(errText(err, "Could not read the password")))
              }
            >
              Show
            </button>
          ),
        ],
      ]
    : [];
  return (
    <div className="gs-settings">
      <article className="gs-widget" id="gs-sec-database">
        <header className="gs-widget-head">
          <h2>MariaDB</h2>
          <span className="meta">{DB_STATE_LABEL[state]}</span>
        </header>
        <p className="muted">
          This server's own database. It runs beside the game on a private network only the two share, so the game reaches it at host{" "}
          <code>mariadb</code>, port 3306, or through the Unix socket below, and nothing else can. Backups of this server include it.
        </p>
        {error ? <ErrorNotice error={error} /> : null}
        {lastJob?.error ? <ErrorNotice error={`Install failed: ${lastJob.error}`} /> : null}
        {installing ? (
          <p className="muted" role="status">
            Installing MariaDB, started {formatRelative(db.job?.started_at)}. The first install downloads it, which takes a minute or two.
          </p>
        ) : null}
        {!installed ? (
          <div className="gs-form">
            <label>
              Database name
              <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} disabled={busy || installing} spellCheck={false} />
              <span className="field-hint">Letters, digits and _ only. The username and password are generated for you.</span>
            </label>
            <div className="btn-row is-flush">
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy || installing || !name.trim() || name.trim() === db.database}
                onClick={() => act(() => putGameDatabase(id, name.trim()))}
              >
                {db.database ? "Rename" : "Save and generate credentials"}
              </button>
              {db.database ? (
                <button type="button" className="btn btn-sm btn-primary" disabled={busy || installing} onClick={() => act(() => gameDatabaseAction(id, "install"))}>
                  Install
                </button>
              ) : null}
            </div>
          </div>
        ) : null}
        {rows.length ? (
          <dl className="kv-grid">
            {rows.map(([k, v]) => (
              <Fragment key={k}>
                <dt>{k}</dt>
                <dd>{v}</dd>
              </Fragment>
            ))}
          </dl>
        ) : null}
        {installed ? (
          <>
            {state === "running" && !serverRunning ? <p className="muted">The game joins the database's network when it starts.</p> : null}
            <div className="btn-row is-flush">
              {state === "stopped" ? (
                <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={() => act(() => gameDatabaseAction(id, "start"))}>
                  Start database
                </button>
              ) : null}
              {state === "running" ? (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  disabled={busy}
                  onClick={() => {
                    if (confirmAction("Stop the database? The game loses it until it starts again, and backups fail while it is stopped.")) {
                      act(() => gameDatabaseAction(id, "stop"));
                    }
                  }}
                >
                  Stop database
                </button>
              ) : null}
              {state === "missing" ? (
                <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={() => act(() => gameDatabaseAction(id, "install"))}>
                  Install again
                </button>
              ) : null}
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={busy}
                onClick={() => {
                  if (confirmAction("Uninstall the database? Its data and credentials are kept, and installing again brings it back.")) {
                    act(() => removeGameDatabase(id, false));
                  }
                }}
              >
                Uninstall
              </button>
            </div>
          </>
        ) : null}
      </article>
      {installed && state === "running" ? <DatabaseManager id={id} /> : null}
      {db.database ? (
        <article className="gs-widget" id="gs-sec-database-delete">
          <header className="gs-widget-head">
            <h2>Delete the database</h2>
          </header>
          <p className="muted">
            Deletes the database, its data and its credentials for good. Backups taken before keep their copy. Type <code>{db.database}</code> to
            confirm.
          </p>
          <div className="gs-form">
            <input className="field-input" value={confirmDelete} onChange={(e) => setConfirmDelete(e.target.value)} aria-label="Database name to confirm" />
            <div className="btn-row is-flush">
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={busy || installing || confirmDelete !== db.database}
                onClick={() =>
                  act(() =>
                    removeGameDatabase(id, true, confirmDelete).then(() => {
                      setConfirmDelete("");
                      setName("");
                      setPassword(null);
                    }),
                  )
                }
              >
                Delete database
              </button>
            </div>
          </div>
        </article>
      ) : null}
    </div>
  );
}

const scopeLines = (text: string) =>
  text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

function BackupScopePane({ id }: { id: string }) {
  const [scope, setScope] = useState<GameBackupScope | null>(null);
  const [editing, setEditing] = useState(false);
  const [include, setInclude] = useState("");
  const [exclude, setExclude] = useState("");
  const [newest, setNewest] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    getGameBackupScope(id)
      .then(setScope)
      .catch((err) => setError(errText(err, "Could not load what backups take")));
  }, [id]);
  const edit = () => {
    setInclude((scope?.include ?? []).join("\n"));
    setExclude((scope?.exclude ?? []).join("\n"));
    setNewest((scope?.newest ?? []).join("\n"));
    setError(null);
    setEditing(true);
  };
  const save = () => {
    setSaving(true);
    setError(null);
    putGameBackupScope(id, { include: scopeLines(include), exclude: scopeLines(exclude), newest: scopeLines(newest) })
      .then((next) => {
        setScope(next);
        setEditing(false);
      })
      .catch((err) => setError(errText(err, "Could not save")))
      .finally(() => setSaving(false));
  };
  const scoped = (scope?.include ?? []).length > 0;
  return (
    <article className="gs-widget" id="gs-sec-backup-scope">
      <header className="gs-widget-head">
        <h2>What gets backed up</h2>
        {!editing ? (
          <button type="button" className="btn btn-sm btn-ghost" onClick={edit} disabled={!scope}>
            Change
          </button>
        ) : null}
      </header>
      {error ? <ErrorNotice error={error} /> : null}
      {!editing && scope ? (
        scoped ? (
          <dl className="kv-grid">
            <dt>Takes</dt>
            <dd>{scope.include.join(", ")}</dd>
            {scope.exclude.length ? (
              <>
                <dt>Leaves out</dt>
                <dd>{scope.exclude.join(", ")}</dd>
              </>
            ) : null}
            {scope.newest.length ? (
              <>
                <dt>Newest file only</dt>
                <dd>{scope.newest.join(", ")}</dd>
              </>
            ) : null}
          </dl>
        ) : (
          <p className="muted">The whole server folder, including the game install and download caches.</p>
        )
      ) : null}
      {editing ? (
        <div className="gs-form">
          <p className="muted">
            One path per line, relative to the server folder. A folder covers everything inside it, and * matches part of a name. Leave
            Takes empty to back up the whole server folder. Restoring a backup replaces only what it took.
          </p>
          <label>
            Takes
            <textarea className="field-input gs-editor is-short" rows={6} value={include} onChange={(e) => setInclude(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Leaves out
            <textarea className="field-input gs-editor is-short" rows={4} value={exclude} onChange={(e) => setExclude(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Newest file only
            <textarea className="field-input gs-editor is-short" rows={2} value={newest} onChange={(e) => setNewest(e.target.value)} spellCheck={false} />
          </label>
          <div className="btn-row is-flush">
            <button type="button" className="btn btn-sm btn-primary" onClick={save} disabled={saving}>
              {saving ? "Saving…" : "Save"}
            </button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setEditing(false)} disabled={saving}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
    </article>
  );
}

const SCHEDULE_ACTIONS = [
  ["backup", "Back up"],
  ["restart", "Restart"],
  ["command", "Run a console command"],
] as const;

function SchedulesPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id: string; name: string; action: string; cron: string; payload?: string; enabled?: boolean }[]>([]);
  const [name, setName] = useState("Nightly backup");
  const [action, setAction] = useState<string>("backup");
  const [cron, setCron] = useState("nightly");
  const [payload, setPayload] = useState("");
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(
    () =>
      listGameSchedules(id)
        .then((r) => setItems(r.items ?? []))
        .catch((err) => setError(errText(err, "Could not load schedules"))),
    [id],
  );
  useEffect(() => {
    void reload();
  }, [reload]);
  return (
    <article className="gs-widget" id="gs-sec-schedules">
      <header className="gs-widget-head">
        <h2>Schedules</h2>
      </header>
      {error ? <ErrorNotice error={error} /> : null}
      <form
        className="gs-inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          setError(null);
          void createGameSchedule(id, { name, action, cron, ...(action === "command" ? { payload } : {}) })
            .then(reload)
            .catch((err) => setError(errText(err, "Could not add the schedule")));
        }}
      >
        <label>
          Name
          <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label>
          Action
          <select className="field-input" value={action} onChange={(e) => setAction(e.target.value)}>
            {SCHEDULE_ACTIONS.map(([v, label]) => (
              <option key={v} value={v}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <label>
          When
          <input className="field-input" value={cron} onChange={(e) => setCron(e.target.value)} placeholder="nightly or 0 4 * * *" required />
        </label>
        {action === "command" ? (
          <label>
            Command
            <input className="field-input" value={payload} onChange={(e) => setPayload(e.target.value)} required />
          </label>
        ) : null}
        <button type="submit" className="btn btn-sm btn-primary">
          Add schedule
        </button>
      </form>
      {items.length === 0 ? <p className="muted">No schedules yet.</p> : null}
      <ul className="gs-list">
        {items.map((s) => (
          <li key={s.id}>
            <span>
              <strong>{s.name}</strong>
              <span className="meta">
                {" "}
                · {s.action}
                {s.payload ? ` "${s.payload}"` : ""} · <code>{s.cron}</code>
                {s.enabled === false ? " · paused" : ""}
              </span>
            </span>
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() =>
                void deleteGameSchedule(id, s.id)
                  .then(reload)
                  .catch((err) => setError(errText(err, "Delete failed")))
              }
            >
              Delete
            </button>
          </li>
        ))}
      </ul>
    </article>
  );
}

/* ---------------- Server Settings ---------------- */

type SettingsSection = { id: string; title: string; render: () => ReactNode };

function SettingsPane({ server, anchor, access }: { server: GameServer; anchor: string; access: GameAccess }) {
  const id = server.id;
  const { featureEnabled } = useNavDisclosure();
  const all: SettingsSection[] = [
    { id: "config", title: "Configuration", render: () => <ConfigurationPane server={server} access={access} /> },
    ...(featureEnabled.ddos ? [{ id: "ddos", title: "DDoS protection", render: () => <GameDDoSPane id={id} /> }] : []),
    { id: "notes", title: "Notes", render: () => <NotesPane id={id} /> },
    { id: "activity", title: "Activity", render: () => <ActivityPane id={id} /> },
    { id: "diagnostics", title: "Diagnostics", render: () => <DiagnosticsPane id={id} /> },
  ];
  // A grant on this one server opens its configuration only; the rest needs a role.
  const sections = access.role ? all : all.filter((s) => s.id === "config" && access.can("gameserver.config"));

  useEffect(() => {
    if (anchor) {
      document.getElementById(`gs-sec-${anchor}`)?.scrollIntoView({ block: "start" });
    }
  }, [anchor]);

  return (
    <div className="gs-settings-layout">
      <nav className="gs-section-index" aria-label="Server settings sections">
        {sections.map((s) => (
          <a
            key={s.id}
            href={`#gs-sec-${s.id}`}
            aria-current={anchor === s.id ? "true" : undefined}
            onClick={(e) => {
              e.preventDefault();
              const el = document.getElementById(`gs-sec-${s.id}`);
              if (el instanceof HTMLDetailsElement) {
                el.open = true;
              }
              el?.scrollIntoView({ behavior: "smooth", block: "start" });
            }}
          >
            {s.title}
          </a>
        ))}
      </nav>
      <div className="gs-settings">
        {sections.map((s, i) => (
          // The first few sections start open; the review sections start closed.
          <details key={s.id} id={`gs-sec-${s.id}`} className="gs-widget gs-section" open={anchor ? anchor === s.id : i < 3}>
            <summary>
              <h2>{s.title}</h2>
            </summary>
            <div className="gs-section-body">{s.render()}</div>
          </details>
        ))}
      </div>
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
  return <pre className="gs-log">{diag || "Collecting"}</pre>;
}

// ConfigurationPane is everything that configures the server, in one
// place: settings, the startup line, resources, ports and public address.
// Each part keeps its own Save.
function ConfigurationPane({ server, access }: { server: GameServer; access: GameAccess }) {
  const id = server.id;
  const parts: { key: string; title: string; body: ReactNode }[] = [
    ...(hasCap(server, "config") ? [{ key: "settings", title: "Server settings", body: <ConfigPane id={id} /> }] : []),
    ...(hasCap(server, "startup") ? [{ key: "startup", title: "Startup", body: <StartupPane id={id} /> }] : []),
    ...(hasCap(server, "startup") && server.game === "gmod" ? [{ key: "build", title: "Game build", body: <GameBuildPane id={id} /> }] : []),
    // A grant on this one server opens its settings only; the rest needs a role.
    ...(access.role && hasCap(server, "resources") ? [{ key: "resources", title: "Resources", body: <ResourcesPane id={id} /> }] : []),
    ...(access.role && hasCap(server, "network") ? [{ key: "network", title: "Networking", body: <NetworkPane id={id} /> }] : []),
    ...(access.role ? [{ key: "public-address", title: "Public address", body: <GameEdgePane id={id} /> }] : []),
    ...(access.role && server.game === "gmod" ? [{ key: "workshop-sync", title: "Live Workshop sync", body: <WorkshopSyncPane id={id} /> }] : []),
  ];
  return (
    <div className="gs-config-parts">
      {parts.map((p) => (
        <section key={p.key} id={`gs-part-${p.key}`} className="gs-config-part" aria-labelledby={`gs-part-${p.key}-title`}>
          <h3 id={`gs-part-${p.key}-title`}>{p.title}</h3>
          {p.body}
        </section>
      ))}
    </div>
  );
}

// WorkshopSyncPane turns live Workshop sync on or off for a Garry's Mod
// server and shows what it last delivered.
export function WorkshopSyncPane({ id }: { id: string }) {
  const [data, setData] = useState<WorkshopSync | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const load = useCallback(() => {
    void getWorkshopSync(id)
      .then(setData)
      .catch((err) => setNote(errText(err, "Could not load")));
  }, [id]);
  useEffect(() => {
    load();
    const timer = window.setInterval(load, 15000);
    return () => window.clearInterval(timer);
  }, [load]);
  if (!data) {
    return <p className="muted">{note ?? "Loading"}</p>;
  }
  const act = (fn: () => Promise<WorkshopSync>, done: string) =>
    void fn()
      .then((r) => {
        setData(r);
        setNote(done);
      })
      .catch((err) => setNote(errText(err, "Failed")));
  return (
    <div className="gs-form">
      <p className="muted">
        New and updated items in the Workshop collection go live without a restart: ndl-ce checks the collection every hour, or when the server asks, downloads what changed into
        garrysmod/data/cs_sync and tells the server. The server needs the addon that mounts them.
      </p>
      <label className="gs-check">
        <input type="checkbox" checked={data.enabled} onChange={(e) => act(() => putWorkshopSync(id, e.target.checked), e.target.checked ? "Live sync is on." : "Live sync is off.")} />
        Live Workshop sync
      </label>
      <dl className="gs-facts">
        <dt>Collection</dt>
        <dd>{data.collection || "None set"}</dd>
        <dt>Last check</dt>
        <dd>
          {data.checking ? "Checking now" : data.last_check ? formatWhen(data.last_check) : "Not yet"}
          {data.last_result ? ` · ${data.last_result}` : ""}
        </dd>
        {data.enabled ? (
          <>
            <dt>Next check</dt>
            <dd>{!data.running ? "When the server runs" : data.next_check ? formatWhen(data.next_check) : "Within a minute"}{data.pending_request ? " (requested)" : ""}</dd>
          </>
        ) : null}
      </dl>
      {data.last_error ? <ErrorNotice error={data.last_error} /> : null}
      {(data.items ?? []).length > 0 ? (
        <ul className="gs-list">
          {(data.items ?? []).map((it) => (
            <li key={it.wsid}>
              <span>
                <strong>{it.title || it.wsid}</strong>
                <span className="meta">
                  {" "}
                  · {it.wsid} · {it.state} · {it.kind === "lua" ? "has Lua" : "assets only"} · {formatBytes(it.size)}
                </span>
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      {(data.errors ?? []).length > 0 ? (
        <ul className="gs-diff">
          {(data.errors ?? []).map((e) => (
            <li key={e.wsid}>
              {e.wsid}: {e.error}
            </li>
          ))}
        </ul>
      ) : null}
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm" disabled={!data.enabled || data.checking} onClick={() => act(() => checkWorkshopSync(id), "Check requested.")}>
          Check now
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
    </div>
  );
}

export function ConfigPane({ id }: { id: string }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [settings, setSettings] = useState<{ id: string; label: string; help?: string; kind: string; options?: string[]; restart?: boolean; advanced?: boolean }[]>([]);
  const [diff, setDiff] = useState<{ label: string; from: string; to: string; restart?: boolean }[]>([]);
  const [advanced, setAdvanced] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  // What was loaded: a save sends only what changed here, so it cannot put
  // back old values over settings saved elsewhere (the Startup part) since,
  // nor write every default into the config file.
  const [loaded, setLoaded] = useState<Record<string, string>>({});
  const load = useCallback(() => {
    void getGameConfig(id).then((r) => {
      setValues(r.values ?? {});
      setLoaded(r.values ?? {});
      setSettings(r.settings ?? []);
    });
  }, [id]);
  useEffect(load, [load]);
  const changed = () => {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(values)) {
      if (loaded[k] !== v) {
        out[k] = v;
      }
    }
    return out;
  };
  const run = (fn: () => Promise<unknown>, done: string) =>
    void fn()
      .then(() => setNote(done))
      .catch((err) => setNote(errText(err, "Failed")));
  return (
    <div className="gs-form">
      <label className="gs-check">
        <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} />
        Show advanced
      </label>
      <div className="gs-form-grid">
        {settings
          .filter((s) => advanced || !s.advanced)
          .map((s) => (
            <label key={s.id}>
              {s.label}
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
                <input
                  className="field-input"
                  type={s.kind === "password" ? "password" : s.kind === "number" ? "number" : "text"}
                  autoComplete={s.kind === "password" ? "new-password" : undefined}
                  value={values[s.id] ?? ""}
                  onChange={(e) => setValues((cur) => ({ ...cur, [s.id]: e.target.value }))}
                />
              )}
              {s.help ? <span className="field-hint">{s.help}</span> : null}
            </label>
          ))}
      </div>
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => void diffGameConfig(id, changed()).then((r) => setDiff(r.changes ?? []))}>
          Preview changes
        </button>
        <button type="button" className="btn btn-sm btn-primary" onClick={() => run(() => putGameConfig(id, changed()).then(() => setLoaded({ ...values })), "Settings saved.")}>
          Save settings
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => run(() => revertGameConfig(id).then(load), "Last save undone.")}>
          Undo last save
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
      {diff.length > 0 ? (
        <ul className="gs-diff">
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

export function StartupPane({ id }: { id: string }) {
  const [startup, setStartup] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  // What was loaded: a save sends only what changed here, so it cannot put
  // back old values over settings saved elsewhere (Server settings) since.
  const [loaded, setLoaded] = useState<{ startup: string; env: Record<string, string> }>({ startup: "", env: {} });
  const [vars, setVars] = useState<{ env: string; name: string; description?: string; secret?: boolean }[]>([]);
  const [note, setNote] = useState<string | null>(null);
  // Variables that Server settings already edit (name, map, gamemode,
  // slots) are not repeated here.
  const [managed, setManaged] = useState<Set<string>>(new Set());
  useEffect(() => {
    void getGameConfig(id)
      .then((r) => setManaged(new Set((r.settings ?? []).map((s) => s.env ?? "").filter(Boolean))))
      .catch(() => undefined);
  }, [id]);
  useEffect(() => {
    void getGameStartup(id).then((r) => {
      setStartup(r.startup);
      setVars(r.variables ?? []);
      const next: Record<string, string> = {};
      for (const v of r.variables ?? []) {
        next[v.env] = v.value;
      }
      setEnv(next);
      setLoaded({ startup: r.startup, env: next });
    });
  }, [id]);
  function save() {
    const changed: Record<string, string> = {};
    for (const [k, v] of Object.entries(env)) {
      if (loaded.env[k] !== v) {
        changed[k] = v;
      }
    }
    const body: Record<string, unknown> = { env: changed };
    if (startup !== loaded.startup) {
      body.startup = startup;
    }
    void putGameStartup(id, body)
      .then(() => {
        setLoaded({ startup, env: { ...env } });
        setNote("Startup saved.");
      })
      .catch((err) => setNote(errText(err, "Save failed")));
  }
  return (
    <div className="gs-form">
      <label>
        Startup command
        <textarea className="field-input gs-editor is-short" value={startup} onChange={(e) => setStartup(e.target.value)} />
      </label>
      <div className="gs-form-grid">
        {vars.filter((v) => !managed.has(v.env)).map((v) => (
          <label key={v.env}>
            {v.name}
            <input
              className="field-input"
              type={v.secret ? "password" : "text"}
              value={env[v.env] ?? ""}
              onChange={(e) => setEnv((cur) => ({ ...cur, [v.env]: e.target.value }))}
            />
            {v.description ? <span className="field-hint">{v.description}</span> : null}
          </label>
        ))}
      </div>
      <div className="btn-row is-flush">
        <button
          type="button"
          className="btn btn-sm btn-primary"
          onClick={save}
        >
          Save startup
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
    </div>
  );
}

function NetworkPane({ id }: { id: string }) {
  const [ports, setPorts] = useState<{ name?: string; container_port: number; host_port?: number; protocol?: string }[]>([]);
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => {
    void getGameNetwork(id).then((r) => setPorts(r.ports ?? []));
  }, [id]);
  return (
    <div className="gs-form">
      <div className="gs-form-grid">
        {ports.map((p, i) => (
          <label key={i}>
            {p.name || "Port"} ({p.protocol || "tcp"}, container {p.container_port})
            <input
              className="field-input"
              type="number"
              value={p.host_port || p.container_port}
              onChange={(e) => setPorts((cur) => cur.map((row, idx) => (idx === i ? { ...row, host_port: Number(e.target.value) } : row)))}
            />
          </label>
        ))}
      </div>
      <div className="btn-row is-flush">
        <button
          type="button"
          className="btn btn-sm btn-primary"
          onClick={() =>
            void putGameNetwork(id, ports)
              .then(() => setNote("Ports saved."))
              .catch((err) => setNote(errText(err, "Save failed")))
          }
        >
          Save ports
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
    </div>
  );
}

function ResourcesPane({ id }: { id: string }) {
  const [cpus, setCpus] = useState(1);
  const [mem, setMem] = useState(1024);
  const [disk, setDisk] = useState(4096);
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => {
    void getGameResources(id).then((r) => {
      setCpus(r.cpus);
      setMem(Math.round(r.memory_bytes / (1024 * 1024)));
      setDisk(Math.round(r.disk_bytes / (1024 * 1024)));
    });
  }, [id]);
  return (
    <div className="gs-form">
      <div className="gs-form-grid">
        <label>
          CPUs
          <input className="field-input" type="number" min={1} value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
        </label>
        <label>
          RAM (MiB)
          <input className="field-input" type="number" min={256} value={mem} onChange={(e) => setMem(Number(e.target.value))} />
        </label>
        <label>
          Disk (MiB)
          <input className="field-input" type="number" min={512} value={disk} onChange={(e) => setDisk(Number(e.target.value))} />
        </label>
      </div>
      <div className="btn-row is-flush">
        <button
          type="button"
          className="btn btn-sm btn-primary"
          onClick={() =>
            void putGameResources(id, { cpus, memory_bytes: mem * 1024 * 1024, disk_bytes: disk * 1024 * 1024 })
              .then(() => setNote("Resources saved."))
              .catch((err) => setNote(errText(err, "Save failed")))
          }
        >
          Save resources
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
    </div>
  );
}

function ActivityPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id?: string; summary: string; created_at: string; detail?: string }[]>([]);
  useEffect(() => {
    void gameActivity(id)
      .then((r) => setItems(r.items ?? []))
      .catch(() => undefined);
  }, [id]);
  if (items.length === 0) {
    return <p className="muted">No activity yet.</p>;
  }
  return (
    <ul className="gs-list">
      {items.map((e, i) => (
        <li key={e.id ?? i}>
          <span>
            {e.summary}
            {e.detail ? <span className="meta"> · {e.detail}</span> : null}
          </span>
          <span className="meta">{formatWhen(e.created_at)}</span>
        </li>
      ))}
    </ul>
  );
}

function NotesPane({ id }: { id: string }) {
  const [notes, setNotes] = useState("");
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => {
    void getGameNotes(id).then((r) => setNotes(r.notes || ""));
  }, [id]);
  return (
    <div className="gs-form">
      <label>
        Private operator notes
        <textarea className="field-input gs-editor is-short" value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>
      <div className="btn-row is-flush">
        <button
          type="button"
          className="btn btn-sm btn-primary"
          onClick={() =>
            void putGameNotes(id, notes)
              .then(() => setNote("Notes saved."))
              .catch((err) => setNote(errText(err, "Save failed")))
          }
        >
          Save notes
        </button>
        {note ? <span className="meta">{note}</span> : null}
      </div>
    </div>
  );
}

/* ---------------- Mods / Plugins / Workshop ---------------- */

const PROVIDER_LABELS: Record<string, string> = { modrinth: "Modrinth", curseforge: "CurseForge", steam: "Steam Workshop", local: "uploaded files" };

function ContentPane({ id, kind }: { id: string; kind: string }) {
  const [installed, setInstalled] = useState<GameContentItem[]>([]);
  const [hits, setHits] = useState<GameContentItem[] | null>(null);
  const [source, setSource] = useState<{ supported: boolean; provider?: string; loader?: string; sources?: string[]; needsKey?: boolean }>({
    supported: true,
  });
  // The catalogue to browse; empty uses the template's own.
  const [catalogue, setCatalogue] = useState("");
  const [q, setQ] = useState("");
  const [searching, setSearching] = useState(false);
  const [warn, setWarn] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const label = kind[0].toUpperCase() + kind.slice(1);
  const workshop = kind === "addons" || kind === "workshop";

  const reload = useCallback(
    () =>
      listGameContent(id)
        .then((r) => setInstalled(r.items ?? []))
        .catch((err) => setWarn(errText(err, "Could not list installed content"))),
    [id],
  );

  const search = useCallback(
    async (query: string) => {
      setSearching(true);
      setWarn("");
      try {
        const r = await searchGameContent(id, query, catalogue);
        setHits(r.items ?? []);
        setSource({ supported: r.supported !== false, provider: r.provider, loader: r.loader, sources: r.sources, needsKey: r.needs_key });
      } catch (err) {
        setHits([]);
        setWarn(errText(err, "Search failed"));
      } finally {
        setSearching(false);
      }
    },
    [id, catalogue],
  );

  useEffect(() => {
    void reload();
    // An empty search browses the provider's most downloaded content (Steam needs an ID first).
    if (!workshop) {
      void search("");
    }
  }, [reload, search, workshop]);

  const installedIds = new Set(installed.map((i) => i.external_id).filter(Boolean));
  const from = [source.loader, PROVIDER_LABELS[source.provider ?? ""] ? `from ${PROVIDER_LABELS[source.provider ?? ""]}` : ""].filter(Boolean).join(" ");

  async function act(key: string, fn: () => Promise<unknown>) {
    setBusy(key);
    setWarn("");
    try {
      await fn();
      await reload();
    } catch (err) {
      setWarn(errText(err, "Action failed"));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="gs-content">
      <article className="gs-widget">
        <header className="gs-widget-head">
          <h2>
            Installed {kind} <span className="meta">{installed.length}</span>
          </h2>
        </header>
        {installed.length === 0 ? <p className="muted">Nothing installed yet. Browse below to add {kind}.</p> : null}
        <ul className="gs-list">
          {installed.map((item) => (
            <li key={item.id}>
              <span>
                <strong>{item.name}</strong>
                <span className="meta">
                  {item.version ? ` · ${item.version}` : ""}
                  {item.enabled === false ? " · off" : ""}
                  {item.incompatible ? " · may not match this server's version" : ""}
                </span>
              </span>
              <span className="btn-row is-flush">
                <button type="button" className="btn btn-sm btn-ghost" disabled={busy !== null} onClick={() => void act(`t:${item.id}`, () => toggleGameContent(id, item.id || "", item.enabled === false))}>
                  {item.enabled === false ? "Enable" : "Disable"}
                </button>
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  disabled={busy !== null}
                  onClick={() =>
                    void act(`u:${item.id}`, async () => {
                      const r = await updateGameContent(id, item.id || "");
                      if (r.warning) {
                        setWarn(r.warning);
                      }
                    })
                  }
                >
                  Update
                </button>
                <button type="button" className="btn btn-sm btn-ghost" disabled={busy !== null} onClick={() => void act(`r:${item.id}`, () => uninstallGameContent(id, item.id || ""))}>
                  Remove
                </button>
              </span>
            </li>
          ))}
        </ul>
      </article>

      <article className="gs-widget">
        <header className="gs-widget-head">
          <h2>
            Browse {label} {from ? <span className="meta">{from}</span> : null}
          </h2>
        </header>
        {(source.sources?.length ?? 0) > 1 ? (
          <Segmented<string>
            value={source.provider ?? ""}
            ariaLabel={`Where to find ${kind}`}
            options={(source.sources ?? []).map((s) => ({ id: s, label: PROVIDER_LABELS[s] ?? s }))}
            onChange={(next) => {
              setHits(null);
              setCatalogue(next);
            }}
          />
        ) : null}
        {source.needsKey ? <CurseForgeKey onSaved={() => void search(q)} /> : null}
        {workshop ? <p className="muted">Search by Steam Workshop numeric ID. Steam does not offer a public full-text catalogue without an API key.</p> : null}
        <form
          className="gs-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            void search(q);
          }}
        >
          <input
            className="field-input"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={workshop ? "Workshop ID" : `Search ${kind}`}
            aria-label={`Search ${kind}`}
          />
          <button className="btn btn-sm btn-primary" type="submit" disabled={searching}>
            {searching ? "Searching" : "Search"}
          </button>
        </form>
        {warn ? <ErrorNotice error={warn} /> : null}
        {!source.supported ? <p className="muted">This server's template has no {kind} source. Upload files from the Files tab instead.</p> : null}
        {searching && hits === null ? <LoadingState label={`Loading ${kind}`} /> : null}
        {hits !== null && hits.length === 0 && !searching && source.supported ? <p className="muted">No {kind} match. Try another search.</p> : null}
        <div className="gs-board is-grid">
          {(hits ?? []).map((hit) => {
            const have = Boolean(hit.external_id && installedIds.has(hit.external_id));
            return (
              <article key={hit.external_id || hit.name} className="gs-card">
                <h3>{hit.name}</h3>
                <p className="gs-meta">{hit.summary}</p>
                <div className="btn-row is-flush">
                  <button
                    type="button"
                    className="btn btn-sm btn-primary"
                    disabled={have || busy !== null}
                    onClick={() =>
                      void act(`i:${hit.external_id}`, async () => {
                        const r = await installGameContent(id, hit.external_id || "", true, source.provider ?? "");
                        if (r.missing_dependencies?.length) {
                          setWarn("Also needed: " + r.missing_dependencies.join(", "));
                        }
                      })
                    }
                  >
                    {have ? "Installed" : busy === `i:${hit.external_id}` ? "Installing" : "Install"}
                  </button>
                  {hit.source_url ? (
                    <a className="btn btn-sm btn-ghost" href={hit.source_url} target="_blank" rel="noreferrer noopener">
                      Details
                    </a>
                  ) : null}
                </div>
              </article>
            );
          })}
        </div>
      </article>
    </div>
  );
}
