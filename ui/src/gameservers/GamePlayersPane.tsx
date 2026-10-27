import { useCallback, useEffect, useState } from "react";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { ResourceTable } from "../components/ResourceTable";
import { confirmAction } from "../confirm";
import { gamePlayerAction, gamePlayers, type GamePlayers } from "./api";

const actionLabel: Record<string, string> = { kick: "Kick", ban: "Ban", op: "Make operator" };

function formatPlayed(seconds?: number): string {
  if (!seconds || seconds < 1) {
    return "";
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return h > 0 ? `${h}h ${m}m` : m > 0 ? `${m}m` : `${Math.floor(seconds)}s`;
}

/** GamePlayersPane lists who is online, asked of the game itself, and refreshes every 10 seconds. */
export function GamePlayersPane({ id }: { id: string }) {
  const [data, setData] = useState<GamePlayers | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setData(await gamePlayers(id));
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Players could not be loaded");
    }
  }, [id]);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 10000);
    return () => window.clearInterval(timer);
  }, [load]);

  async function act(action: string, name: string) {
    if (action !== "kick" && !confirmAction(`${actionLabel[action] ?? action} ${name}?`)) {
      return;
    }
    setBusy(true);
    setNote(null);
    try {
      const res = await gamePlayerAction(id, action, name);
      setNote(`Sent: ${res.command}`);
      window.setTimeout(() => void load(), 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : `${action} failed`);
    } finally {
      setBusy(false);
    }
  }

  const list = data?.list;
  const scores = Boolean(list?.players.some((p) => p.score));
  const played = Boolean(list?.players.some((p) => p.seconds));
  return (
    <article className="gs-widget">
      <h2>Players</h2>
      {error ? <ErrorNotice error={error} /> : null}
      {note ? (
        <p className="muted" role="status">
          {note}
        </p>
      ) : null}
      {!data && !error ? <LoadingState label="Asking the server who is online" /> : null}
      {data && !data.supported ? <p className="muted">This game has no player query No-dal can read. Whitelist, op, kick and ban commands run in the console.</p> : null}
      {data?.supported && !data.running ? <EmptyState title="The server is not running." /> : null}
      {data?.supported && data.running && data.error ? <p className="muted">{data.error}</p> : null}
      {list ? (
        <>
          <p>
            <strong>
              {list.online} of {list.max || "?"} online
            </strong>
            {list.bots ? ` (${list.bots} bots)` : ""}
            {list.map ? ` · ${list.map}` : ""}
          </p>
          <ResourceTable
            headers={[
              "Name",
              ...(scores ? ["Score"] : []),
              ...(played ? ["Connected"] : []),
              ...(data.actions.length
                ? [
                    <span key="act" className="visually-hidden">
                      Actions
                    </span>,
                  ]
                : []),
            ]}
            numeric={scores ? [1] : []}
            empty={<EmptyState title={list.online > 0 ? "Players are online, but the game does not share their names." : "Nobody is online."} />}
            rows={[...list.players]
              .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }))
              .map((p) => [
                p.name,
                ...(scores ? [String(p.score ?? 0)] : []),
                ...(played ? [formatPlayed(p.seconds)] : []),
                ...(data.actions.length
                  ? [
                      <span key={`act-${p.name}`} className="btn-row">
                        {data.actions.map((a) => (
                          <button
                            key={a}
                            className={`btn btn-sm ${a === "ban" ? "btn-danger" : "btn-secondary"}`}
                            type="button"
                            disabled={busy}
                            onClick={() => void act(a, p.name)}
                          >
                            {actionLabel[a] ?? a}
                          </button>
                        ))}
                      </span>,
                    ]
                  : []),
              ])}
          />
          {list.partial ? <p className="muted">The game only names some of the players online.</p> : null}
        </>
      ) : null}
    </article>
  );
}
