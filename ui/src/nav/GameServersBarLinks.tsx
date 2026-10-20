import { Link } from "../components/Link";
import { usePath } from "../router";
import { gameServersHomeFilter, gameServersHomeHref } from "./gameServers";

const FILTERS = [
  ["all", "Servers"],
  ["favorites", "Favourites"],
  ["recent", "Recent"],
] as const;

/**
 * GameServersBarLinks are the Game Servers dashboard's views. They join the
 * Instance bar while the dashboard is open, and leave with it.
 */
export function GameServersBarLinks() {
  const filter = gameServersHomeFilter(usePath());
  return (
    <nav className="page-nav-sub" aria-label="Game Servers views">
      {FILTERS.map(([id, label]) => (
        <Link key={id} href={gameServersHomeHref(id)} className="page-nav-sub-link" aria-current={filter === id ? "page" : undefined}>
          {label}
        </Link>
      ))}
    </nav>
  );
}
