import { useEffect, useMemo, useState } from "react";
import { getGameServer, listGameServers } from "../gameservers/api";
import type { GameServer } from "../gameservers/types";
import { statusLabel } from "../gameservers/caps";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { canMutate } from "../rbac";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { useQuery } from "../query";
import { ContextSidebar } from "./ContextSidebar";
import {
  GS_ROOT,
  gameServerHref,
  gameServerIdFromPath,
  gameServerSectionFromPath,
  gameServersHomeFilter,
  gameServersHomeHref,
} from "./gameServers";
import { buildGameServerNav } from "./gameServersNav";

function live(status?: string): boolean {
  return status === "running" || status === "starting";
}

export function GameServersNavigator() {
  const path = usePath();
  const session = useSession();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const [query, setQuery] = useState("");
  const selectedId = gameServerIdFromPath(path);
  const section = gameServerSectionFromPath(path) || "overview";
  const area = gameServersHomeFilter(path);
  const serversQ = useQuery("ctx-game-servers", () => listGameServers(), 8000);
  const catalog = useMemo(() => serversQ.data?.items ?? [], [serversQ.data]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return catalog;
    }
    return catalog.filter((row) =>
      `${row.name} ${row.game} ${row.template_name ?? ""} ${row.status}`.toLowerCase().includes(needle),
    );
  }, [catalog, query]);

  if (selectedId) {
    const current = catalog.find((row) => row.id === selectedId);
    return <GameServerContextNav id={selectedId} server={current} section={section} />;
  }

  return (
    <ContextSidebar title="Game Servers">
      <div className="ctx-actions">
        <Link
          href={GS_ROOT}
          className="nav-link"
          aria-label="All servers"
          title="All game servers"
          aria-current={path === GS_ROOT ? "page" : undefined}
        >
          <Icon name="game" />
          <span className="nav-link-label">All servers</span>
        </Link>
        <Link
          href={gameServersHomeHref("favourites")}
          className="nav-link"
          aria-label="Favourites"
          aria-current={area === "favorites" ? "page" : undefined}
        >
          <Icon name="mark-ok" />
          <span className="nav-link-label">Favourites</span>
        </Link>
        <Link
          href={gameServersHomeHref("recent")}
          className="nav-link"
          aria-label="Recent"
          aria-current={area === "recent" ? "page" : undefined}
        >
          <Icon name="activity" />
          <span className="nav-link-label">Recent</span>
        </Link>
        {mutate ? (
          <Link
            href={`${GS_ROOT}/create`}
            className="nav-link"
            aria-label="Create server"
            aria-current={path === `${GS_ROOT}/create` ? "page" : undefined}
          >
            <Icon name="create" />
            <span className="nav-link-label">Create server</span>
          </Link>
        ) : null}
        {mutate ? (
          <Link
            href={`${GS_ROOT}/catalogue`}
            className="nav-link"
            aria-label="Catalogue"
            aria-current={path === `${GS_ROOT}/catalogue` ? "page" : undefined}
          >
            <Icon name="search" />
            <span className="nav-link-label">Catalogue</span>
          </Link>
        ) : null}
        {mutate ? (
          <Link
            href={gameServersHomeHref("fleet")}
            className="nav-link"
            aria-label="Fleet"
            title="Select servers on the board for start, stop, restart, or backup"
            aria-current={area === "fleet" ? "page" : undefined}
          >
            <Icon name="workloads" />
            <span className="nav-link-label">Fleet</span>
          </Link>
        ) : null}
      </div>
      <label className="ctx-search search-field">
        <Icon name="search" size={14} />
        <input
          className="field-input"
          type="search"
          value={query}
          placeholder="Search servers"
          aria-label="Search game servers"
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
      <div className="ctx-tree" role="tree" aria-label="Game servers">
        {serversQ.error ? <p className="muted ctx-empty">{serversQ.error}</p> : null}
        {!serversQ.error && filtered.length === 0 ? <p className="muted ctx-empty">No game servers.</p> : null}
        {filtered.length > 0 ? (
          <div className="ctx-group">
            <p className="ctx-group-toggle" aria-hidden="true">
              Servers
            </p>
            {filtered.map((server) => (
              <ServerLink key={server.id} server={server} current={false} />
            ))}
          </div>
        ) : null}
      </div>
    </ContextSidebar>
  );
}

function GameServerContextNav({
  id,
  server,
  section,
}: {
  id: string;
  server?: GameServer;
  section: string;
}) {
  const [fetched, setFetched] = useState<GameServer | null>(null);
  useEffect(() => {
    if (server) {
      setFetched(server);
      return;
    }
    let cancelled = false;
    void getGameServer(id)
      .then((row) => {
        if (!cancelled) {
          setFetched(row);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id, server]);
  const current = server || fetched;
  const groups = buildGameServerNav(id, current, section);
  const name = current?.name || id;
  const running = live(current?.status);

  return (
    <>
      <button className="ctx-back" type="button" aria-label="Back to Game Servers" onClick={() => navigate(GS_ROOT)}>
        <span aria-hidden="true">←</span>
        <span className="ctx-back-label">Back to Game Servers</span>
      </button>
      <p className="nav-group-label ctx-area-title">Game Servers</p>
      <Link
        href={gameServerHref(id)}
        className="ctx-item is-current"
        data-nav-id={`game-server:${id}`}
        title={`${name} · ${current?.template_name || current?.game || "game"} · ${current?.status || "unknown"}`}
      >
        <span className={"ctx-dot" + (running ? " is-live" : "")} aria-hidden="true">
          {running ? "●" : "○"}
        </span>
        <span className="ctx-item-label">{name}</span>
      </Link>
      <p className="ctx-selected-meta">
        {current?.template_name || current?.game || "Game server"} · {statusLabel(current?.status)}
      </p>
      <nav className="gs-ctx-nav" aria-label="Game server">
        {groups.map((group) => (
          <div className="nav-group" key={group.label}>
            <p className="nav-group-label">{group.label}</p>
            {group.items.map((entry) => (
              <Link
                key={entry.href}
                href={entry.href}
                className="nav-link"
                aria-label={entry.label}
                title={entry.label}
                aria-current={entry.current ? "page" : undefined}
              >
                <Icon name={entry.icon} />
                <span className="nav-link-label">{entry.label}</span>
              </Link>
            ))}
          </div>
        ))}
      </nav>
    </>
  );
}

function ServerLink({ server, current }: { server: GameServer; current: boolean }) {
  const running = live(server.status);
  return (
    <Link
      href={gameServerHref(server.id)}
      className={"ctx-item" + (current ? " is-current" : "")}
      role="treeitem"
      aria-current={current ? "page" : undefined}
      aria-label={`${server.name}, ${server.template_name || server.game}, ${statusLabel(server.status)}`}
      title={`${server.name} · ${server.template_name || server.game} · ${statusLabel(server.status)}`}
      data-nav-id={`game-server:${server.id}`}
    >
      <span className={"ctx-dot" + (running ? " is-live" : "")} aria-hidden="true">
        {running ? "●" : "○"}
      </span>
      <span className="ctx-item-label">{server.name}</span>
    </Link>
  );
}
