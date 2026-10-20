import { confirmAction } from "../confirm";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError } from "../api/client";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { FilesBrowser } from "../components/FilesBrowser";
import { GameConsoleTerminal } from "../components/GameConsoleTerminal";
import { contentKind, familyTone, formatRam, hasCap, statusLabel } from "../gameservers/caps";
import { GS_ROOT, gameServerAnchorFromPath, gameServerHref, gameServerIdFromPath, gameServerSectionFromPath } from "../nav/gameServers";
import { gameServerQueryKey } from "../nav/GameServerFrame";
import { gameServerTabList } from "../nav/gameServersNav";
import {
  createGameBackup,
  createGameSchedule,
  createGameUser,
  deleteGameBackup,
  deleteGameSchedule,
  deleteGameServer,
  deleteGameUser,
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
  installGameContent,
  listGameBackups,
  listGameContent,
  listGameSchedules,
  listGameUsers,
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
import { formatBytes, formatWhen } from "../format";
import { useQuery } from "../query";
import { navigate, usePath } from "../router";
import { ErrorNotice } from "../components/ErrorNotice";

const errText = (err: unknown, fallback: string) => (err instanceof ApiError || err instanceof Error ? err.message : fallback);

export function GameServerDetailPage() {
  const path = usePath();
  const id = gameServerIdFromPath(path) || "";
  const tab = gameServerSectionFromPath(path) || "overview";
  const anchor = gameServerAnchorFromPath(path);
  // Shares the bar's query, so the page and GameServerFrame show one copy of the server.
  const q = useQuery(gameServerQueryKey(id), () => getGameServer(id), 4000);
  const server = q.data?.id === id ? q.data : undefined;
  const tabs = useMemo(() => gameServerTabList(server), [server]);

  useEffect(() => {
    if (server && id && !tabs.some((item) => item.id === tab)) {
      navigate(gameServerHref(id), { replace: true });
    }
  }, [id, server, tab, tabs]);

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

      {tab === "overview" ? <OverviewPane server={server} onReload={q.reload} /> : null}
      {tab === "console" ? <GameConsoleTerminal key={id} id={id} name={server.name} /> : null}
      {tab === "files" ? <FilesBrowser key={id} kind="game" id={id} embedded onTerminalHere={() => navigate(gameServerHref(id, "console"))} /> : null}
      {tab === "mods" || tab === "plugins" || tab === "workshop" ? <ContentPane id={id} kind={contentKind(server) || tab} /> : null}
      {tab === "players" ? (
        <article className="gs-widget">
          <h2>Players</h2>
          <p className="muted">
            Player administration uses the console: whitelist, op and ban commands run there. No-DAL does not speak the game protocol itself.
          </p>
        </article>
      ) : null}
      {tab === "backups" ? <BackupsAndSchedules server={server} /> : null}
      {tab === "settings" ? <SettingsPane server={server} anchor={anchor} /> : null}
    </section>
  );
}

/* ---------------- Overview ---------------- */

function OverviewPane({ server, onReload }: { server: GameServer; onReload: () => Promise<unknown> }) {
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
      <DangerRow server={server} onReload={onReload} />
    </div>
  );
}

function DangerRow({ server, onReload }: { server: GameServer; onReload: () => Promise<unknown> }) {
  return (
    <article className="gs-widget gs-wide gs-danger">
      <div>
        <h2>Danger zone</h2>
        <p className="muted">Reinstall takes a rollback point first. Delete removes the container and its files.</p>
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
      </div>
    </article>
  );
}

/* ---------------- Backups & Schedules ---------------- */

function BackupsAndSchedules({ server }: { server: GameServer }) {
  return (
    <div className="gs-split">
      {hasCap(server, "backups") ? <BackupsPane id={server.id} /> : null}
      {hasCap(server, "schedules") ? <SchedulesPane id={server.id} /> : null}
    </div>
  );
}

function BackupsPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ id: string; name: string; reason?: string; bytes: number; created_at: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(
    () =>
      listGameBackups(id)
        .then((r) => setItems(r.items ?? []))
        .catch((err) => setError(errText(err, "Could not load backups"))),
    [id],
  );
  useEffect(() => {
    void reload();
  }, [reload]);
  const act = (fn: () => Promise<unknown>) => {
    setError(null);
    void fn()
      .then(reload)
      .catch((err) => setError(errText(err, "Action failed")));
  };
  return (
    <article className="gs-widget" id="gs-sec-backups">
      <header className="gs-widget-head">
        <h2>Backups</h2>
        <button type="button" className="btn btn-sm btn-primary" onClick={() => act(() => createGameBackup(id, "Manual"))}>
          Create backup
        </button>
      </header>
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
                onClick={() => {
                  if (confirmAction(`Restore ${b.name}? The server's files are replaced.`)) {
                    act(() => restoreGameBackup(id, b.id));
                  }
                }}
              >
                Restore
              </button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => act(() => deleteGameBackup(id, b.id))}>
                Delete
              </button>
            </span>
          </li>
        ))}
      </ul>
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

function SettingsPane({ server, anchor }: { server: GameServer; anchor: string }) {
  const id = server.id;
  const sections: SettingsSection[] = [
    ...(hasCap(server, "config") ? [{ id: "config", title: "Configuration", render: () => <ConfigPane id={id} /> }] : []),
    ...(hasCap(server, "startup") ? [{ id: "startup", title: "Startup", render: () => <StartupPane id={id} /> }] : []),
    ...(hasCap(server, "resources") ? [{ id: "resources", title: "Resources", render: () => <ResourcesPane id={id} /> }] : []),
    ...(hasCap(server, "network") ? [{ id: "network", title: "Networking", render: () => <NetworkPane id={id} /> }] : []),
    ...(hasCap(server, "databases")
      ? [
          {
            id: "databases",
            title: "Databases",
            render: () => (
              <p className="muted">
                This template advertises a game database. Edit it from Files or Configuration. No-DAL does not run a separate database manager for this server.
              </p>
            ),
          },
        ]
      : []),
    { id: "users", title: "Access", render: () => <UsersPane id={id} /> },
    { id: "notes", title: "Notes", render: () => <NotesPane id={id} /> },
    { id: "activity", title: "Activity", render: () => <ActivityPane id={id} /> },
    { id: "diagnostics", title: "Diagnostics", render: () => <DiagnosticsPane id={id} /> },
  ];

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

function ConfigPane({ id }: { id: string }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [settings, setSettings] = useState<{ id: string; label: string; help?: string; kind: string; options?: string[]; restart?: boolean; advanced?: boolean }[]>([]);
  const [diff, setDiff] = useState<{ label: string; from: string; to: string; restart?: boolean }[]>([]);
  const [advanced, setAdvanced] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => {
    void getGameConfig(id).then((r) => {
      setValues(r.values ?? {});
      setSettings(r.settings ?? []);
    });
  }, [id]);
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
                <input className="field-input" value={values[s.id] ?? ""} onChange={(e) => setValues((cur) => ({ ...cur, [s.id]: e.target.value }))} />
              )}
              {s.help ? <span className="field-hint">{s.help}</span> : null}
            </label>
          ))}
      </div>
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => void diffGameConfig(id, values).then((r) => setDiff(r.changes ?? []))}>
          Preview changes
        </button>
        <button type="button" className="btn btn-sm btn-primary" onClick={() => run(() => putGameConfig(id, values), "Settings saved.")}>
          Save settings
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => run(() => revertGameConfig(id), "Last save undone.")}>
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

function StartupPane({ id }: { id: string }) {
  const [startup, setStartup] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  const [vars, setVars] = useState<{ env: string; name: string; description?: string; secret?: boolean }[]>([]);
  const [note, setNote] = useState<string | null>(null);
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
        <textarea className="field-input gs-editor is-short" value={startup} onChange={(e) => setStartup(e.target.value)} />
      </label>
      <div className="gs-form-grid">
        {vars.map((v) => (
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
          onClick={() =>
            void putGameStartup(id, { startup, env })
              .then(() => setNote("Startup saved."))
              .catch((err) => setNote(errText(err, "Save failed")))
          }
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

function UsersPane({ id }: { id: string }) {
  const [items, setItems] = useState<{ user_id: string; username: string; grants: string[] }[]>([]);
  const [username, setUsername] = useState("");
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(() => listGameUsers(id).then((r) => setItems(r.items ?? [])), [id]);
  useEffect(() => {
    void reload().catch(() => undefined);
  }, [reload]);
  return (
    <div className="gs-form">
      {error ? <ErrorNotice error={error} /> : null}
      <form
        className="gs-inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          setError(null);
          void createGameUser(id, username, ["view", "console", "power"])
            .then(() => {
              setUsername("");
              return reload();
            })
            .catch((err) => setError(errText(err, "Could not grant access")));
        }}
      >
        <label>
          Username
          <input className="field-input" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </label>
        <button type="submit" className="btn btn-sm btn-primary">
          Grant console and power
        </button>
      </form>
      <ul className="gs-list">
        {items.map((u) => (
          <li key={u.user_id}>
            <span>
              <strong>{u.username}</strong>
              <span className="meta"> · {(u.grants || []).join(", ")}</span>
            </span>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void deleteGameUser(id, u.user_id).then(reload)}>
              Revoke
            </button>
          </li>
        ))}
      </ul>
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

const PROVIDER_LABELS: Record<string, string> = { modrinth: "Modrinth", steam: "Steam Workshop", local: "uploaded files" };

function ContentPane({ id, kind }: { id: string; kind: string }) {
  const [installed, setInstalled] = useState<GameContentItem[]>([]);
  const [hits, setHits] = useState<GameContentItem[] | null>(null);
  const [source, setSource] = useState<{ supported: boolean; provider?: string; loader?: string }>({ supported: true });
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
        const r = await searchGameContent(id, query);
        setHits(r.items ?? []);
        setSource({ supported: r.supported !== false, provider: r.provider, loader: r.loader });
      } catch (err) {
        setHits([]);
        setWarn(errText(err, "Search failed"));
      } finally {
        setSearching(false);
      }
    },
    [id],
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
                        const r = await installGameContent(id, hit.external_id || "");
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
