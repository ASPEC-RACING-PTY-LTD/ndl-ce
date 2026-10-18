import type { ReactNode } from "react";
import { getGameServer } from "../gameservers/api";
import { statusLabel } from "../gameservers/caps";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { useQuery } from "../query";
import { usePath } from "../router";
import { GS_ROOT, gameServerIdFromPath, gameServerSectionFromPath } from "./gameServers";
import { buildGameServerNav } from "./gameServersNav";

/**
 * GameServerFrame is a server's own section bar (Overview, Console, Files,
 * ...) above its pages. It replaced the Game Servers sidebar, so the main
 * sidebar stays visible.
 */
export function GameServerFrame({ children }: { children: ReactNode }) {
  const path = usePath();
  const id = gameServerIdFromPath(path) ?? "";
  const section = gameServerSectionFromPath(path) || "overview";
  const q = useQuery(`game-server:${id}`, () => getGameServer(id), 8000);
  const server = q.data?.id === id ? q.data : undefined;
  const links = buildGameServerNav(id, server, section).flatMap((g) => g.items);
  return (
    <div className="wl-frame">
      <div className="wl-frame-bar">
        <Link href={GS_ROOT} className="wl-frame-back" aria-label="Back to Game Servers" title="Back to Game Servers">
          <Icon name="collapse" size={14} />
        </Link>
        <div className="wl-frame-id">
          <span className="wl-frame-name">{server?.name ?? (q.error ? "Game server" : "Loading")}</span>
          {server ? (
            <span className="wl-frame-meta">
              {server.template_name || server.game} · {statusLabel(server.status)}
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
      </div>
      {children}
    </div>
  );
}
