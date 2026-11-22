import { Fragment, useCallback, useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { ErrorNotice } from "../components/ErrorNotice";
import { confirmAction } from "../confirm";
import { formatBytes } from "../format";
import { Segmented } from "../ui/Segmented";
import {
  createGameDatabaseUser,
  dropGameDatabaseUser,
  getGameDatabaseSchema,
  getGameDatabaseTable,
  getGameDatabaseResources,
  getGameDatabaseUsers,
  putGameDatabaseResources,
  restartGameDatabase,
  putGameDatabaseGrants,
  runGameDatabaseQuery,
  setGameDatabaseUserPassword,
  type DBQueryResult,
  type DBResourcesState,
  type DBResultSet,
  type DBSchema,
  type DBTablePage,
  type DBUser,
  type DBUsers,
} from "./api";

// DatabaseManager browses and manages one game server's MariaDB: its
// tables and rows, a SQL console, and users with their privileges. The
// MariaDB tab and Administration > Databases both show it.

type View = "browse" | "sql" | "users" | "resources";

const errText = (err: unknown, fallback: string) => (err instanceof ApiError || err instanceof Error ? err.message : fallback);

export function DatabaseManager({ id }: { id: string }) {
  const [view, setView] = useState<View>("browse");
  const [database, setDatabase] = useState("");
  const [sql, setSQL] = useState("");
  return (
    <article className="gs-widget db-manager" id="gs-sec-database-manager">
      <header className="gs-widget-head">
        <h2>Manage</h2>
        <Segmented<View>
          ariaLabel="Database manager view"
          value={view}
          onChange={setView}
          options={[
            { id: "browse", label: "Browse" },
            { id: "sql", label: "SQL" },
            { id: "users", label: "Users & privileges" },
            { id: "resources", label: "Resources" },
          ]}
        />
      </header>
      {view === "browse" ? (
        <BrowseView
          id={id}
          database={database}
          onDatabase={setDatabase}
          onQuery={(q) => {
            setSQL(q);
            setView("sql");
          }}
        />
      ) : null}
      {view === "sql" ? <SQLView id={id} database={database} sql={sql} onSQL={setSQL} /> : null}
      {view === "users" ? <UsersView id={id} /> : null}
      {view === "resources" ? <ResourcesView id={id} /> : null}
    </article>
  );
}

/* ---------------- Browse ---------------- */

function BrowseView({ id, database, onDatabase, onQuery }: { id: string; database: string; onDatabase: (db: string) => void; onQuery: (sql: string) => void }) {
  const [schema, setSchema] = useState<DBSchema | null>(null);
  const [table, setTable] = useState("");
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    setError(null);
    getGameDatabaseSchema(id, database)
      .then(setSchema)
      .catch((err) => setError(errText(err, "Could not read the tables")));
  }, [id, database]);
  useEffect(() => {
    setTable("");
    load();
  }, [load]);
  if (!schema) {
    return error ? <ErrorNotice error={error} /> : <p className="muted">Loading the tables</p>;
  }
  return (
    <div className="db-browse">
      <div className="db-toolbar">
        <label className="db-inline">
          Database
          <select className="field-input" value={schema.database} onChange={(e) => onDatabase(e.target.value === schema.server_database ? "" : e.target.value)}>
            {schema.databases.map((d) => (
              <option key={d} value={d}>
                {d}
                {d === schema.server_database ? " (this server's)" : ""}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="btn btn-sm btn-ghost" onClick={load}>
          Refresh
        </button>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {table ? (
        <TableView id={id} database={schema.database} table={table} onBack={() => setTable("")} onQuery={onQuery} />
      ) : schema.tables.length === 0 ? (
        <p className="muted">No tables yet. Create one from SQL, or let the game create its own when it first connects.</p>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Table</th>
                <th>Rows (about)</th>
                <th>Size</th>
                <th>Engine</th>
              </tr>
            </thead>
            <tbody>
              {schema.tables.map((t) => (
                <tr key={t.name}>
                  <td>
                    <button type="button" className="linkish" onClick={() => setTable(t.name)}>
                      {t.name}
                    </button>
                    {t.view ? <span className="meta"> · view</span> : null}
                  </td>
                  <td>{t.rows}</td>
                  <td>{formatBytes(t.data_bytes + t.index_bytes)}</td>
                  <td>{t.engine}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const PAGE = 100;

function TableView({ id, database, table, onBack, onQuery }: { id: string; database: string; table: string; onBack: () => void; onQuery: (sql: string) => void }) {
  const [page, setPage] = useState<DBTablePage | null>(null);
  const [offset, setOffset] = useState(0);
  const [order, setOrder] = useState("");
  const [dir, setDir] = useState<"asc" | "desc">("asc");
  const [showColumns, setShowColumns] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setError(null);
    getGameDatabaseTable(id, database, table, offset, PAGE, order, dir)
      .then(setPage)
      .catch((err) => setError(errText(err, "Could not read the table")));
  }, [id, database, table, offset, order, dir]);
  const sortBy = (col: string) => {
    if (order === col) {
      setDir(dir === "asc" ? "desc" : "asc");
    } else {
      setOrder(col);
      setDir("asc");
    }
    setOffset(0);
  };
  const quoted = "`" + table.replace(/`/g, "``") + "`";
  return (
    <div className="db-table">
      <div className="db-toolbar">
        <button type="button" className="btn btn-sm btn-ghost" onClick={onBack}>
          All tables
        </button>
        <strong>{table}</strong>
        <span className="meta">{page ? `${page.total} rows` : ""}</span>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => setShowColumns(!showColumns)}>
          {showColumns ? "Hide structure" : "Structure"}
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onQuery(`SELECT * FROM ${quoted} LIMIT 100;`)}>
          Query in SQL
        </button>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {page && showColumns ? (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Column</th>
                <th>Type</th>
                <th>Null</th>
                <th>Key</th>
                <th>Default</th>
                <th>Extra</th>
              </tr>
            </thead>
            <tbody>
              {page.columns.map((c) => (
                <tr key={c.name}>
                  <td>
                    <code>{c.name}</code>
                  </td>
                  <td>{c.type}</td>
                  <td>{c.nullable ? "Yes" : "No"}</td>
                  <td>{c.key}</td>
                  <td>{c.default === null ? <span className="db-null">NULL</span> : c.default}</td>
                  <td>{c.extra}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {page ? (
        <>
          <ResultGrid
            columns={page.columns.map((c) => c.name)}
            rows={page.rows}
            onSort={sortBy}
            sortedBy={order}
            sortDir={dir}
          />
          <div className="db-toolbar">
            <button type="button" className="btn btn-sm btn-ghost" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
              Previous
            </button>
            <span className="meta">
              {page.total === 0 ? "No rows" : `Rows ${offset + 1} to ${Math.min(offset + PAGE, page.total)} of ${page.total}`}
            </span>
            <button type="button" className="btn btn-sm btn-ghost" disabled={offset + PAGE >= page.total} onClick={() => setOffset(offset + PAGE)}>
              Next
            </button>
          </div>
        </>
      ) : null}
    </div>
  );
}

/** ResultGrid shows rows; NULL is shown apart from the text "NULL". */
export function ResultGrid({
  columns,
  rows,
  onSort,
  sortedBy,
  sortDir,
}: {
  columns: string[];
  rows: (string | null)[][];
  onSort?: (col: string) => void;
  sortedBy?: string;
  sortDir?: "asc" | "desc";
}) {
  if (rows.length === 0) {
    return <p className="muted">No rows.</p>;
  }
  return (
    <div className="table-wrap db-grid">
      <table className="data-table">
        <thead>
          <tr>
            {columns.map((c, i) => (
              <th key={i}>
                {onSort ? (
                  <button type="button" className="linkish" onClick={() => onSort(c)}>
                    {c}
                    {sortedBy === c ? (sortDir === "asc" ? " ▲" : " ▼") : ""}
                  </button>
                ) : (
                  c
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {r.map((v, j) => (
                <td key={j} title={v ?? "NULL"}>
                  {v === null ? <span className="db-null">NULL</span> : v}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/* ---------------- SQL ---------------- */

function SQLView({ id, database, sql, onSQL }: { id: string; database: string; sql: string; onSQL: (s: string) => void }) {
  const [result, setResult] = useState<DBQueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = () => {
    if (!sql.trim()) {
      return;
    }
    setRunning(true);
    setError(null);
    runGameDatabaseQuery(id, sql, database)
      .then(setResult)
      .catch((err) => setError(errText(err, "Could not run the SQL")))
      .finally(() => setRunning(false));
  };
  return (
    <div className="db-sql">
      <label>
        SQL{database ? ` in ${database}` : ""}
        <textarea
          className="field-input gs-editor db-sql-input"
          rows={8}
          value={sql}
          spellCheck={false}
          placeholder="SELECT * FROM players LIMIT 10;"
          onChange={(e) => onSQL(e.target.value)}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
              e.preventDefault();
              run();
            }
          }}
        />
        <span className="field-hint">
          Separate statements with ;. They run in order as the database's root user, and the first error stops the run. Ctrl+Enter runs.
          Each run is recorded on the server's activity, with passwords taken out.
        </span>
      </label>
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-primary" onClick={run} disabled={running || !sql.trim()}>
          {running ? "Running…" : "Run"}
        </button>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {result ? <QueryResultView result={result} /> : null}
    </div>
  );
}

export function QueryResultView({ result }: { result: DBQueryResult }) {
  return (
    <div className="db-results">
      <p className="meta">
        {result.statements.length} statement{result.statements.length === 1 ? "" : "s"} ran in {result.elapsed_ms} ms
      </p>
      {result.statements.map((st, i) => (
        <section key={i} className="db-statement">
          <pre className="db-statement-sql">{st.sql}</pre>
          {st.sets.length === 0 ? (
            <p className="muted">
              {st.affected >= 0 ? `${st.affected} row${st.affected === 1 ? "" : "s"} affected.` : "Done."}
            </p>
          ) : (
            st.sets.map((set: DBResultSet, j) => (
              <Fragment key={j}>
                <ResultGrid columns={set.columns} rows={set.rows} />
                <p className="meta">
                  {set.row_count} row{set.row_count === 1 ? "" : "s"}
                  {set.truncated ? `, the first ${set.rows.length} shown. Add a LIMIT to see others.` : ""}
                </p>
              </Fragment>
            ))
          )}
        </section>
      ))}
      {result.error ? <ErrorNotice error={`Statement ${result.error_index + 1} failed: ${result.error}`} /> : null}
    </div>
  );
}

/* ---------------- Users & privileges ---------------- */

function UsersView({ id }: { id: string }) {
  const [data, setData] = useState<DBUsers | null>(null);
  const [editing, setEditing] = useState<DBUser | null>(null);
  const [creating, setCreating] = useState(false);
  const [shown, setShown] = useState<{ who: string; password: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    getGameDatabaseUsers(id)
      .then(setData)
      .catch((err) => setError(errText(err, "Could not read the users")));
  }, [id]);
  useEffect(() => load(), [load]);
  const act = (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    return fn()
      .then(load)
      .catch((err) => setError(errText(err, "Action failed")))
      .finally(() => setBusy(false));
  };
  if (!data) {
    return error ? <ErrorNotice error={error} /> : <p className="muted">Loading the users</p>;
  }
  return (
    <div className="db-users">
      {error ? <ErrorNotice error={error} /> : null}
      {shown ? (
        <div className="banner" role="status">
          <p>
            Password for <code>{shown.who}</code>, shown once: <code>{shown.password}</code>{" "}
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void navigator.clipboard?.writeText(shown.password)}>
              Copy
            </button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setShown(null)}>
              Done
            </button>
          </p>
        </div>
      ) : null}
      <div className="db-toolbar">
        <button type="button" className="btn btn-sm btn-primary" onClick={() => setCreating(true)} disabled={busy}>
          New user
        </button>
      </div>
      {creating ? (
        <NewUserForm
          data={data}
          busy={busy}
          onCancel={() => setCreating(false)}
          onCreate={(body) =>
            act(() =>
              createGameDatabaseUser(id, body).then((r) => {
                setCreating(false);
                setShown({ who: `${r.user}@${r.host}`, password: r.password });
              }),
            )
          }
        />
      ) : null}
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>User</th>
              <th>Host</th>
              <th>Privileges</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.users.map((u) => (
              <tr key={`${u.user}@${u.host}`}>
                <td>
                  <code>{u.user}</code>
                  {u.server ? <span className="meta"> · the server's user</span> : null}
                  {u.protected ? <span className="meta"> · MariaDB's own</span> : null}
                </td>
                <td>
                  <code>{u.host}</code>
                </td>
                <td className="db-grants">
                  {Object.keys(u.privileges).length === 0 ? (
                    <span className="muted">{u.protected ? "Built in" : "None"}</span>
                  ) : (
                    Object.entries(u.privileges).map(([db, privs]) => (
                      <div key={db}>
                        <code>{db}</code>: {privs.length >= data.privileges.length ? "all" : privs.join(", ")}
                      </div>
                    ))
                  )}
                </td>
                <td>
                  {u.protected ? null : (
                    <span className="btn-row is-flush">
                      <button type="button" className="btn btn-sm btn-ghost" disabled={busy} onClick={() => setEditing(u)}>
                        Privileges
                      </button>
                      {u.server ? null : (
                        <>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            disabled={busy}
                            onClick={() => {
                              if (confirmAction(`Give ${u.user}@${u.host} a new password? Anything using the old one stops connecting.`)) {
                                void act(() => setGameDatabaseUserPassword(id, u.user, u.host).then((r) => setShown({ who: `${u.user}@${u.host}`, password: r.password })));
                              }
                            }}
                          >
                            New password
                          </button>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            disabled={busy}
                            onClick={() => {
                              if (confirmAction(`Remove the user ${u.user}@${u.host}? This cannot be undone.`)) {
                                void act(() => dropGameDatabaseUser(id, u.user, u.host));
                              }
                            }}
                          >
                            Remove
                          </button>
                        </>
                      )}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {editing ? (
        <GrantsEditor
          key={`${editing.user}@${editing.host}`}
          user={editing}
          data={data}
          busy={busy}
          onClose={() => setEditing(null)}
          onSave={(db, privs) => act(() => putGameDatabaseGrants(id, editing.user, editing.host, db, privs).then(() => setEditing(null)))}
        />
      ) : null}
    </div>
  );
}

function PrivilegeChecks({ all, value, onChange }: { all: string[]; value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="db-privs">
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onChange([...all])}>
          All
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onChange(["SELECT"])}>
          Read only
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onChange(["SELECT", "INSERT", "UPDATE", "DELETE"])}>
          Read and write rows
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onChange([])}>
          None
        </button>
      </div>
      <div className="db-priv-grid">
        {all.map((p) => (
          <label key={p} className="db-check">
            <input
              type="checkbox"
              checked={value.includes(p)}
              onChange={(e) => onChange(e.target.checked ? [...value, p] : value.filter((x) => x !== p))}
            />
            {p}
          </label>
        ))}
      </div>
    </div>
  );
}

function NewUserForm({
  data,
  busy,
  onCancel,
  onCreate,
}: {
  data: DBUsers;
  busy: boolean;
  onCancel: () => void;
  onCreate: (body: { user: string; host: string; password: string; database: string; privileges: string[] }) => void;
}) {
  const [user, setUser] = useState("");
  const [host, setHost] = useState("%");
  const [password, setPassword] = useState("");
  const [database, setDatabase] = useState(data.server_database);
  const [privs, setPrivs] = useState<string[]>(["SELECT", "INSERT", "UPDATE", "DELETE"]);
  return (
    <section className="gs-form db-panel">
      <h3>New user</h3>
      <div className="gs-form-grid">
        <label>
          Username
          <input className="field-input" value={user} onChange={(e) => setUser(e.target.value)} spellCheck={false} />
        </label>
        <label>
          Host
          <input className="field-input" value={host} onChange={(e) => setHost(e.target.value)} spellCheck={false} />
          <span className="field-hint">% is anywhere on the server's private network.</span>
        </label>
        <label>
          Password
          <input className="field-input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
          <span className="field-hint">Leave empty to generate one.</span>
        </label>
        <label>
          Database
          <select className="field-input" value={database} onChange={(e) => setDatabase(e.target.value)}>
            {data.databases
              .filter((d) => !["mysql", "information_schema", "performance_schema", "sys"].includes(d))
              .map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
          </select>
        </label>
      </div>
      <PrivilegeChecks all={data.privileges} value={privs} onChange={setPrivs} />
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-primary" disabled={busy || !user.trim()} onClick={() => onCreate({ user: user.trim(), host: host.trim(), password, database, privileges: privs })}>
          Create user
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </section>
  );
}

function GrantsEditor({
  user,
  data,
  busy,
  onClose,
  onSave,
}: {
  user: DBUser;
  data: DBUsers;
  busy: boolean;
  onClose: () => void;
  onSave: (db: string, privs: string[]) => void;
}) {
  const dbs = data.databases.filter((d) => !["mysql", "information_schema", "performance_schema", "sys"].includes(d));
  const [database, setDatabase] = useState(data.server_database);
  const [privs, setPrivs] = useState<string[]>(user.privileges[data.server_database] ?? []);
  return (
    <section className="gs-form db-panel">
      <h3>
        Privileges for <code>{user.user}</code>@<code>{user.host}</code>
      </h3>
      {user.server ? <p className="muted">This is the game's own user. Taking privileges away can stop the game reaching its data.</p> : null}
      <label>
        On database
        <select
          className="field-input"
          value={database}
          onChange={(e) => {
            setDatabase(e.target.value);
            setPrivs(user.privileges[e.target.value] ?? []);
          }}
        >
          {dbs.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
        </select>
      </label>
      <PrivilegeChecks all={data.privileges} value={privs} onChange={setPrivs} />
      {user.grants.length ? (
        <details>
          <summary>Current grants</summary>
          <pre className="db-statement-sql">{user.grants.join("\n")}</pre>
        </details>
      ) : null}
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={() => onSave(database, privs)}>
          Save privileges
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onClose} disabled={busy}>
          Close
        </button>
      </div>
    </section>
  );
}

/* ---------------- Resources ---------------- */

// ResourcesView sets the database's own memory and CPU limits, apart from
// the game's. Changes apply live while it runs.
function ResourcesView({ id }: { id: string }) {
  const [state, setState] = useState<DBResourcesState | null>(null);
  const [memory, setMemory] = useState("");
  const [cpus, setCPUs] = useState("");
  const [pool, setPool] = useState("");
  const [fast, setFast] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const apply = (s: DBResourcesState) => {
    setState(s);
    setMemory(String(s.memory_mb));
    setCPUs(s.cpus ? String(s.cpus) : "");
    setPool(s.buffer_pool_mb ? String(s.buffer_pool_mb) : "");
    setFast(Boolean(s.fast_commit));
  };
  const load = useCallback(() => {
    getGameDatabaseResources(id)
      .then(apply)
      .catch((err) => setError(errText(err, "Could not read the limits")));
  }, [id]);
  useEffect(() => load(), [load]);
  if (!state) {
    return error ? <ErrorNotice error={error} /> : <p className="muted">Loading the limits</p>;
  }
  const u = state.usage;
  const save = () => {
    setBusy(true);
    setError(null);
    setNote(null);
    putGameDatabaseResources(id, { memory_mb: Number(memory) || 0, cpus: Number(cpus) || 0, buffer_pool_mb: Number(pool) || 0, fast_commit: fast })
      .then((s) => {
        apply(s);
        setNote(s.usage?.running ? "Applied now, without a restart." : "Saved. It applies when the database starts.");
      })
      .catch((err) => setError(errText(err, "Could not change the limits")))
      .finally(() => setBusy(false));
  };
  const restart = () => {
    if (!confirmAction("Restart the database? The game loses its connection for a few seconds, then reconnects.")) {
      return;
    }
    setBusy(true);
    setError(null);
    restartGameDatabase(id)
      .then(() => {
        setNote("Restarted.");
        load();
      })
      .catch((err) => setError(errText(err, "Could not restart the database")))
      .finally(() => setBusy(false));
  };
  return (
    <div className="gs-form">
      <p className="muted">
        The database runs in its own container, so these limits are its own and separate from the game server's. Changes apply at once while
        it runs.
      </p>
      {u?.running ? (
        <dl className="kv-grid">
          <dt>Memory in use</dt>
          <dd>
            {formatBytes(u.memory_bytes)} of {formatBytes(u.memory_limit_bytes)}
          </dd>
          <dt>CPU now</dt>
          <dd>{u.cpu_percent.toFixed(1)}%</dd>
          <dt>Cache (InnoDB buffer pool)</dt>
          <dd>{formatBytes(u.buffer_pool_bytes)}</dd>
          <dt>Log flushed</dt>
          <dd>{u.flush_log_at_commit === 2 ? "Once a second (faster writes)" : u.flush_log_at_commit === 1 ? "At every commit" : String(u.flush_log_at_commit)}</dd>
        </dl>
      ) : null}
      {u?.restart_needed ? (
        <div className="banner" role="status">
          <p>
            This database was installed before NDL kept its settings in a config file and shared its socket. Changes here are in effect now; one
            restart from here keeps them after later restarts and adds the socket. Restart the game server afterwards so it can use the socket.{" "}
            <button type="button" className="btn btn-sm" onClick={restart} disabled={busy}>
              Restart database
            </button>
          </p>
        </div>
      ) : null}
      <div className="gs-form-grid">
        <label>
          Memory limit (MB)
          <input className="field-input" type="number" min={256} max={65536} step={128} value={memory} onChange={(e) => setMemory(e.target.value)} />
          <span className="field-hint">256 MB to 64 GB. MariaDB cannot use more than this.</span>
        </label>
        <label>
          CPU limit (cores)
          <input className="field-input" type="number" min={0} max={64} step={0.5} value={cpus} placeholder="No cap" onChange={(e) => setCPUs(e.target.value)} />
          <span className="field-hint">Empty or 0 lets it use every core. 1.5 is one and a half cores.</span>
        </label>
        <label>
          Cache size (MB)
          <input className="field-input" type="number" min={128} step={128} value={pool} placeholder={`Automatic (${state.usage?.effective_buffer_pool_mb ?? Math.max(128, Math.floor(Number(memory) / 2 / 128) * 128)} MB)`} onChange={(e) => setPool(e.target.value)} />
          <span className="field-hint">MariaDB keeps tables and indexes here. Automatic is half the memory limit.</span>
        </label>
      </div>
      <label className="db-check">
        <input type="checkbox" checked={fast} onChange={(e) => setFast(e.target.checked)} />
        Faster writes: flush the log once a second instead of at every commit
      </label>
      <span className="field-hint">
        Much faster saves. If the whole host loses power or crashes, up to the last second of changes can be lost; a crash of MariaDB on its
        own loses nothing.
      </span>
      {error ? <ErrorNotice error={error} /> : null}
      {note ? (
        <p className="muted" role="status">
          {note}
        </p>
      ) : null}
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-sm btn-primary" onClick={save} disabled={busy}>
          {busy ? "Applying…" : "Apply"}
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={restart} disabled={busy || !u?.running}>
          Restart database
        </button>
      </div>
    </div>
  );
}
