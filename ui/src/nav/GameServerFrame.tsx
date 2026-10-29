import { useState, type ReactNode } from "react";
import { ApiError } from "../api/client";
import { confirmAction } from "../confirm";
import { ErrorNotice } from "../components/ErrorNotice";
import { gamePower, getGameServer } from "../gameservers/api";
import { statusLabel } from "../gameservers/caps";
import type { GameServer } from "../gameservers/types";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { useQuery } from "../query";
import { canOnItem, hasRoleAccess } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";
import { GS_ROOT, gameServerIdFromPath, gameServerSectionFromPath } from "./gameServers";
import { buildGameServerNav, gameTabAllow } from "./gameServersNav";

/** The query key a server's page and its bar share, so both show one copy. */
export const gameServerQueryKey = (id: string) => `game-server:${id}`;

/**
 * GameServerFrame is a server's own bar: back, the server's name and state,
 * its tabs and its power actions. It takes the Instance bar's place on the
 * server's pages, so there is one bar, not two.
 */
export function GameServerFrame({ children }: { children: ReactNode }) {
  const path = usePath();
  const id = gameServerIdFromPath(path) ?? "";
  const section = gameServerSectionFromPath(path) || "overview";
  const q = useQuery(gameServerQueryKey(id), () => getGameServer(id), 4000);
  const server = q.data?.id === id ? q.data : undefined;
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const allow = gameTabAllow(hasRoleAccess(user), (perm) => canOnItem(user, "gameserver", id, perm));
  const links = buildGameServerNav(id, server, section, allow).flatMap((g) => g.items);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="wl-frame gs-frame">
      <div className="wl-frame-bar gs-frame-bar">
        <Link href={GS_ROOT} className="wl-frame-back" aria-label="Back to Game Servers" title="Back to Game Servers">
          <Icon name="collapse" size={14} />
        </Link>
        <div className="wl-frame-id">
          <span className="wl-frame-name">{server?.name ?? (q.error ? "Game server" : "Loading")}</span>
          {server ? (
            <span className="wl-frame-meta">
              {server.template_name || server.game} · <span className={"gs-status is-" + server.status}>{statusLabel(server.status)}</span>
            </span>
          ) : null}
        </div>
        <nav className="wl-frame-tabs" aria-label="Game server">
          {links.map((entry) => (
            <Link key={entry.href} href={entry.href} className="wl-frame-tab" aria-current={entry.current ? "page" : undefined}>
              <Icon name={entry.icon} size={14} />
              {entry.label}
            </Link>
          ))}
        </nav>
        {server ? <PowerActions server={server} onDone={q.reload} onError={setError} /> : null}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {children}
    </div>
  );
}

function PowerActions({ server, onDone, onError }: { server: GameServer; onDone: () => Promise<unknown>; onError: (msg: string | null) => void }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  // Power comes from a role or from a grant on this one server.
  const mutate = canOnItem(user, "gameserver", server.id, "gameserver.power");
  const [busy, setBusy] = useState(false);
  if (!mutate) {
    return null;
  }
  const running = server.status === "running";

  async function power(action: "start" | "stop" | "restart" | "kill") {
    if (action === "kill" && !confirmAction("Force stop now? Unsaved world data may be lost.")) {
      return;
    }
    setBusy(true);
    onError(null);
    try {
      await gamePower(server.id, action);
      await onDone();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "Power action failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="btn-row is-flush gs-frame-actions">
      {running ? (
        <>
          <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => void power("restart")}>
            Restart
          </button>
          <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => void power("stop")}>
            Stop
          </button>
        </>
      ) : (
        <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={() => void power("start")}>
          Start
        </button>
      )}
      <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => void power("kill")}>
        Force stop
      </button>
    </div>
  );
}
