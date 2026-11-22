import { useCallback, useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { PageHeader } from "../components/PageHeader";
import { confirmAction } from "../confirm";
import { formatBytes } from "../format";
import { DatabaseManager, QueryResultView } from "../gameservers/DatabaseManager";
import { bulkDatabases, listAllDatabases, type BulkDBResult, type GlobalDatabase } from "../gameservers/api";
import { gameServerHref } from "../nav/gameServers";

// DatabasesPage is the global database manager: every game server's
// MariaDB, with bulk start, stop and SQL, and the full manager for one.

const STATE_LABEL: Record<string, string> = {
  configured: "Not installed",
  running: "Running",
  stopped: "Stopped",
  missing: "Container missing",
};

const errText = (err: unknown, fallback: string) => (err instanceof ApiError || err instanceof Error ? err.message : fallback);

export function DatabasesPage() {
  const [items, setItems] = useState<GlobalDatabase[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<string | null>(null);
  const [sql, setSQL] = useState("");
  const [results, setResults] = useState<BulkDBResult[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    listAllDatabases()
      .then((r) => setItems(r.items))
      .catch((err) => setError(errText(err, "Could not list the databases")));
  }, []);
  useEffect(() => load(), [load]);

  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    setSelected(next);
  };
  const bulk = (action: "start" | "stop" | "restart" | "query", dryRun = false) => {
    const ids = [...selected];
    const names = (items ?? []).filter((d) => selected.has(d.server_id)).map((d) => d.server_name);
    const verb = { start: "Start", stop: "Stop", restart: "Restart", query: "" }[action];
    const what = action === "query" ? `Run this SQL in ${ids.length} database(s): ${names.join(", ")}?` : `${verb} ${ids.length} database(s): ${names.join(", ")}?`;
    if (!dryRun && !confirmAction(what)) {
      return;
    }
    setBusy(true);
    setError(null);
    bulkDatabases(ids, action, sql, dryRun)
      .then((r) => {
        setResults(r.results);
        load();
      })
      .catch((err) => setError(errText(err, "The bulk action failed")))
      .finally(() => setBusy(false));
  };

  if (!items) {
    return (
      <section className="page page-wide" aria-labelledby="dbs-heading">
        {error ? <ErrorNotice error={error} /> : <LoadingState label="Listing the databases" />}
      </section>
    );
  }
  const running = items.filter((d) => d.state === "running");
  const allSelected = items.length > 0 && selected.size === items.length;
  const openDB = items.find((d) => d.server_id === open);
  return (
    <section className="page page-wide" aria-labelledby="dbs-heading">
      <PageHeader
        id="dbs-heading"
        title="Databases"
        kicker="Every game server's own MariaDB. Pick some to start, stop, restart or run the same SQL in all of them, or manage one in full, including its memory and CPU limits."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" onClick={load}>
              Refresh
            </button>
          </div>
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {items.length === 0 ? (
        <p className="muted">No game server has a database yet. Add one from a game server's MariaDB tab.</p>
      ) : (
        <>
          <p className="meta">
            {items.length} database{items.length === 1 ? "" : "s"}, {running.length} running,{" "}
            {formatBytes(running.reduce((sum, d) => sum + d.size_bytes, 0))} of data.
          </p>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>
                    <input
                      type="checkbox"
                      aria-label="Select all"
                      checked={allSelected}
                      onChange={() => setSelected(allSelected ? new Set() : new Set(items.map((d) => d.server_id)))}
                    />
                  </th>
                  <th>Game server</th>
                  <th>Database</th>
                  <th>User</th>
                  <th>State</th>
                  <th>Tables</th>
                  <th>Size</th>
                  <th>Memory</th>
                  <th>CPU</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {items.map((d) => (
                  <tr key={d.server_id} aria-selected={selected.has(d.server_id)}>
                    <td>
                      <input type="checkbox" aria-label={`Select ${d.server_name}`} checked={selected.has(d.server_id)} onChange={() => toggle(d.server_id)} />
                    </td>
                    <td>
                      <a href={gameServerHref(d.server_id, "databases")}>{d.server_name}</a>
                      <span className="meta"> · {d.game}</span>
                    </td>
                    <td>
                      <code>{d.database}</code>
                    </td>
                    <td>
                      <code>{d.user}</code>
                    </td>
                    <td>{STATE_LABEL[d.state] ?? d.state}</td>
                    <td>{d.state === "running" ? d.tables : ""}</td>
                    <td>{d.state === "running" ? formatBytes(d.size_bytes) : ""}</td>
                    <td>{d.memory_mb ? `${d.memory_mb} MB` : ""}</td>
                    <td>{d.cpus ? `${d.cpus} cores` : "No cap"}</td>
                    <td>
                      {d.state === "running" ? (
                        <button type="button" className="btn btn-sm btn-ghost" onClick={() => setOpen(open === d.server_id ? null : d.server_id)}>
                          {open === d.server_id ? "Close" : "Manage"}
                        </button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <article className="gs-widget">
            <header className="gs-widget-head">
              <h2>Bulk actions</h2>
              <span className="meta">{selected.size} selected</span>
            </header>
            <div className="btn-row is-flush">
              <button type="button" className="btn btn-sm" disabled={busy || selected.size === 0} onClick={() => bulk("start")}>
                Start
              </button>
              <button type="button" className="btn btn-sm" disabled={busy || selected.size === 0} onClick={() => bulk("stop")}>
                Stop
              </button>
              <button type="button" className="btn btn-sm" disabled={busy || selected.size === 0} onClick={() => bulk("restart")}>
                Restart
              </button>
            </div>
            <label>
              SQL to run in each selected database
              <textarea
                className="field-input gs-editor db-sql-input"
                rows={6}
                value={sql}
                spellCheck={false}
                placeholder="UPDATE settings SET value = 'on' WHERE name = 'events';"
                onChange={(e) => setSQL(e.target.value)}
              />
              <span className="field-hint">Runs in each database in turn, as its root user. A failure in one does not stop the others.</span>
            </label>
            <div className="btn-row is-flush">
              <button type="button" className="btn btn-sm" disabled={busy || selected.size === 0 || !sql.trim()} onClick={() => bulk("query", true)}>
                Test (dry run)
              </button>
              <button type="button" className="btn btn-sm btn-primary" disabled={busy || selected.size === 0 || !sql.trim()} onClick={() => bulk("query")}>
                {busy ? "Running…" : `Run in ${selected.size} database${selected.size === 1 ? "" : "s"}`}
              </button>
            </div>
            {results ? (
              <div className="db-results">
                {results.map((r) => (
                  <section key={r.server_id} className="db-statement">
                    <h3>
                      {r.server_name || r.server_id}: {r.ok ? "done" : "failed"}
                      {r.state ? ` (${STATE_LABEL[r.state] ?? r.state})` : ""}
                    </h3>
                    {r.query ? <QueryResultView result={r.query} /> : r.error ? <ErrorNotice error={r.error} /> : null}
                  </section>
                ))}
              </div>
            ) : null}
          </article>

          {openDB ? (
            <>
              <h2 className="db-open-title">
                {openDB.server_name}: <code>{openDB.database}</code>
              </h2>
              <DatabaseManager key={openDB.server_id} id={openDB.server_id} />
            </>
          ) : null}
        </>
      )}
    </section>
  );
}
