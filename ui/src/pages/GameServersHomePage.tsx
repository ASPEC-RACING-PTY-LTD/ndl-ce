import { useEffect, useMemo, useState } from "react";
import { ApiError } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { familyTone, formatRam, hasCap, statusLabel } from "../gameservers/caps";
import {
  deleteGameServer,
  favoriteGameServer,
  fleetGameServers,
  getGamePrefs,
  listGameServers,
  putGamePrefs,
  gamePower,
  searchGameServers,
} from "../gameservers/api";
import type { GameServer, GameView } from "../gameservers/types";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { gameServerHref, gameServersHomeFilter, gameServersHomeHref, GS_ROOT } from "../nav/gameServers";
import { navigate, usePath } from "../router";

function portText(server: GameServer): string {
  const ports = server.ports ?? [];
  if (ports.length === 0) {
    return "No ports yet";
  }
  return ports
    .slice(0, 2)
    .map((p) => `${p.host_port || p.container_port}/${p.protocol || "tcp"}`)
    .join(" · ");
}

export function GameServersHomePage() {
  const path = usePath();
  const session = useSession();
  const { featureEnabled } = useNavDisclosure();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const [items, setItems] = useState<GameServer[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [view, setView] = useState<GameView>("grid");
  const [statusFilter, setStatusFilter] = useState("all");
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const filter = gameServersHomeFilter(path);

  useEffect(() => {
    if (featureEnabled.gameservers === false) {
      return;
    }
    void (async () => {
      try {
        const prefs = await getGamePrefs();
        if (prefs.view) {
          setView(prefs.view);
        }
        const list = await listGameServers();
        setItems(list.items ?? []);
      } catch (err) {
        setError(err instanceof ApiError ? err.message : "Could not load game servers");
      }
    })();
  }, [featureEnabled.gameservers]);

  const visible = useMemo(() => {
    const rows = items ?? [];
    const filtered = rows.filter((row) => {
      if (filter === "favorites" && !row.pinned) {
        return false;
      }
      if (statusFilter === "running" && row.status !== "running") {
        return false;
      }
      if (statusFilter === "failed" && row.status !== "failed") {
        return false;
      }
      return true;
    });
    if (filter === "recent") {
      return [...filtered].sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at)));
    }
    return filtered;
  }, [filter, items, statusFilter]);

  async function changeView(next: GameView) {
    setView(next);
    try {
      await putGamePrefs({ view: next });
    } catch {
      // prefs are best-effort
    }
  }

  async function runSearch(value: string) {
    setQ(value);
    try {
      const list = value.trim() ? await searchGameServers(value) : await listGameServers();
      setItems(list.items ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Search failed");
    }
  }

  async function refresh() {
    const list = q.trim() ? await searchGameServers(q) : await listGameServers();
    setItems(list.items ?? []);
  }

  async function onPower(id: string, action: "start" | "stop" | "restart") {
    setBusy(true);
    try {
      await gamePower(id, action);
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Power action failed");
    } finally {
      setBusy(false);
    }
  }

  async function onFavorite(id: string) {
    await favoriteGameServer(id);
    await refresh();
  }

  async function onDelete(id: string, name: string) {
    if (!window.confirm(`Delete ${name}? Files and the container are removed. This cannot be undone.`)) {
      return;
    }
    setBusy(true);
    try {
      await deleteGameServer(id);
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  async function onFleet(action: string) {
    if (selected.length === 0) {
      return;
    }
    setBusy(true);
    try {
      await fleetGameServers(action, selected);
      setSelected([]);
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Fleet action failed");
    } finally {
      setBusy(false);
    }
  }

  if (featureEnabled.gameservers === false) {
    return (
      <section className="page gs-page" aria-labelledby="gs-home-title">
        <PageHeader id="gs-home-title" title="Game Servers" kicker="This feature is turned off." />
        <EmptyState
          title="Game Servers is disabled"
          action={
            <Link className="btn btn-primary" href="/settings/features">
              Add Features
            </Link>
          }
        >
          Enable it from Add Features. Existing servers stay on disk until you delete them.
        </EmptyState>
      </section>
    );
  }

  if (error && !items) {
    return <ErrorState>{error}</ErrorState>;
  }
  if (!items) {
    return <LoadingState label="Loading game servers" />;
  }

  const favs = items.filter((s) => s.pinned);
  const recent = [...items].sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at))).slice(0, 4);

  return (
    <section className="page gs-page" aria-labelledby="gs-home-title">
      <PageHeader
        id="gs-home-title"
        title="Game Servers"
        kicker="Create and operate dedicated servers without touching Linux by hand."
        actions={
          mutate ? (
            <Link className="btn btn-primary" href={`${GS_ROOT}/create`}>
              Create server
            </Link>
          ) : null
        }
      />

      <div className="gs-toolbar">
        <label className="search-field gs-search">
          <span className="visually-hidden">Search servers</span>
          <input
            className="field-input"
            type="search"
            value={q}
            placeholder="Search name, game, status"
            onChange={(event) => void runSearch(event.target.value)}
          />
        </label>
        <div className="gs-filters" role="tablist" aria-label="Filter servers">
          {[
            ["all", "All"],
            ["favorites", "Favourites"],
            ["recent", "Recent"],
            ["running", "Running"],
            ["failed", "Failed"],
          ].map(([id, label]) => {
            const on =
              id === "running" || id === "failed" ? statusFilter === id && filter === "all" : filter === id || (id === "all" && filter === "fleet");
            return (
              <button
                key={id}
                type="button"
                className={"gs-chip" + (on ? " is-on" : "")}
                onClick={() => {
                  if (id === "running" || id === "failed") {
                    setStatusFilter((cur) => (cur === id ? "all" : id));
                    if (filter !== "all" && filter !== "fleet") {
                      navigate(GS_ROOT);
                    }
                    return;
                  }
                  setStatusFilter("all");
                  navigate(gameServersHomeHref(id));
                }}
              >
                {label}
              </button>
            );
          })}
        </div>
        <div className="gs-views" role="group" aria-label="Layout">
          {(["grid", "compact", "list", "large"] as GameView[]).map((id) => (
            <button key={id} type="button" className={"gs-chip" + (view === id ? " is-on" : "")} onClick={() => void changeView(id)}>
              {id[0].toUpperCase() + id.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {error ? <p className="banner-danger">{error}</p> : null}

      {items.length === 0 ? (
        <EmptyState
          title="No game servers yet"
          action={
            mutate ? (
              <Link className="btn btn-primary" href={`${GS_ROOT}/create`}>
                Create your first server
              </Link>
            ) : null
          }
        >
          Pick a game, accept the obvious defaults, and No-DAL installs the runtime for you.
        </EmptyState>
      ) : (
        <>
          {favs.length > 0 && filter === "all" ? (
            <section className="gs-rail" aria-label="Favourites">
              <h2>Favourites</h2>
              <div className="gs-rail-row">
                {favs.map((server) => (
                  <Link key={`fav-${server.id}`} className="gs-pill" href={gameServerHref(server.id)}>
                    {server.name}
                  </Link>
                ))}
              </div>
            </section>
          ) : null}
          {recent.length > 0 && filter === "all" && !q ? (
            <section className="gs-rail" aria-label="Recent">
              <h2>Recent</h2>
              <div className="gs-rail-row">
                {recent.map((server) => (
                  <Link key={`rec-${server.id}`} className="gs-pill" href={gameServerHref(server.id)}>
                    {server.name}
                  </Link>
                ))}
              </div>
            </section>
          ) : null}

          {filter === "fleet" ? (
            <p className="lede">Select servers, then start, stop, restart, or backup them together.</p>
          ) : null}

          {mutate && selected.length > 0 ? (
            <div className="gs-fleet">
              <span>{selected.length} selected</span>
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onFleet("start")}>
                Start
              </button>
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onFleet("stop")}>
                Stop
              </button>
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onFleet("restart")}>
                Restart
              </button>
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onFleet("backup")}>
                Backup
              </button>
            </div>
          ) : null}

          <div className={"gs-board is-" + view}>
            {visible.map((server) => (
              <article key={server.id} className={"gs-card " + familyTone(server.family)}>
                <label className="gs-select">
                  <input
                    type="checkbox"
                    checked={selected.includes(server.id)}
                    onChange={(event) =>
                      setSelected((cur) => (event.target.checked ? [...cur, server.id] : cur.filter((id) => id !== server.id)))
                    }
                    aria-label={`Select ${server.name}`}
                  />
                </label>
                <button type="button" className={"gs-pin" + (server.pinned ? " is-on" : "")} onClick={() => void onFavorite(server.id)} aria-label="Favourite">
                  ★
                </button>
                <Link className="gs-card-main" href={gameServerHref(server.id)}>
                  <span className={"gs-status is-" + server.status}>{statusLabel(server.status)}</span>
                  <h3>{server.name}</h3>
                  <p className="gs-meta">
                    {server.template_name || server.game} · {formatRam(server.memory_bytes)} · {server.cpus} CPU
                  </p>
                  <p className="gs-meta">{portText(server)}</p>
                  {server.error_human ? <p className="gs-error">{server.error_human}</p> : null}
                </Link>
                <div className="gs-quick">
                  {mutate && server.status !== "running" ? (
                    <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onPower(server.id, "start")}>
                      Start
                    </button>
                  ) : null}
                  {mutate && server.status === "running" ? (
                    <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onPower(server.id, "restart")}>
                      Restart
                    </button>
                  ) : null}
                  {hasCap(server, "console") ? (
                    <Link className="btn btn-ghost" href={gameServerHref(server.id, "console")}>
                      Console
                    </Link>
                  ) : null}
                  {hasCap(server, "files") ? (
                    <Link className="btn btn-ghost" href={gameServerHref(server.id, "files")}>
                      Files
                    </Link>
                  ) : null}
                  {hasCap(server, "backups") ? (
                    <Link className="btn btn-ghost" href={gameServerHref(server.id, "backups")}>
                      Backups
                    </Link>
                  ) : null}
                  {mutate ? (
                    <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onDelete(server.id, server.name)}>
                      Delete
                    </button>
                  ) : null}
                </div>
              </article>
            ))}
          </div>
        </>
      )}
    </section>
  );
}
