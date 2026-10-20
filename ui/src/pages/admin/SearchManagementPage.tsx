import { useCallback, useEffect, useState } from "react";
import {
  cancelSearchIndex,
  clearDeepSearchIndex,
  getSearchSettings,
  putSearchSettings,
  startSearchIndex,
  type SearchSettingsBody,
  type SearchSettingsView,
} from "../../api/client";
import type { SearchFilter } from "../../generated/openapi";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { ErrorNotice } from "../../components/ErrorNotice";
import { RelativeTime } from "../../ui/RelativeTime";
import { Switch } from "../../ui/Switch";

function eta(seconds: number | undefined): string {
  if (seconds === undefined || seconds < 0) {
    return "working it out";
  }
  if (seconds < 60) {
    return "under a minute";
  }
  const m = Math.round(seconds / 60);
  if (m < 60) {
    return `about ${m} min`;
  }
  const h = Math.floor(m / 60);
  return `about ${h} h ${m % 60} min`;
}

function lines(v: string): string[] {
  return v
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/**
 * SearchManagementPage configures global search: what deep indexing
 * covers, saved filters shown in the search box, kinds hidden for everyone,
 * and the indexing run with its progress.
 */
export function SearchManagementPage() {
  const [view, setView] = useState<SearchSettingsView | null>(null);
  const [draft, setDraft] = useState<SearchSettingsBody | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState("");
  const [clearOpen, setClearOpen] = useState(false);

  const load = useCallback(async () => {
    try {
      const v = await getSearchSettings();
      setView(v);
      setDraft((cur) => cur ?? v.settings);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const running = view?.job?.status === "running";
  useEffect(() => {
    if (!running) {
      return;
    }
    const id = window.setInterval(() => void load(), 2000);
    return () => window.clearInterval(id);
  }, [running, load]);

  async function act(fn: () => Promise<SearchSettingsView>, msg: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      const v = await fn();
      setView(v);
      setDraft(v.settings);
      setSaved(msg);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!view || !draft) {
    return (
      <section className="page page-wide">
        <h1>Search</h1>
        {error ? <ErrorNotice error={error} /> : <p className="muted">Loading</p>}
      </section>
    );
  }
  const deep = draft.deep;
  const changed = JSON.stringify(draft) !== JSON.stringify(view.settings);
  const job = view.job;
  const setDeep = (patch: Partial<typeof deep>) => setDraft({ ...draft, deep: { ...deep, ...patch } });
  const kindLabel = (id: string) => view.kinds.find((k) => k.id === id)?.label ?? id;

  function setFilter(i: number, patch: Partial<SearchFilter>) {
    if (!draft) {
      return;
    }
    const filters = draft.filters.map((f, j) => (j === i ? { ...f, ...patch } : f));
    setDraft({ ...draft, filters });
  }

  return (
    <section className="page page-wide" aria-labelledby="search-mgmt-heading">
      <header className="page-header">
        <h1 id="search-mgmt-heading">Search</h1>
        <p className="page-lede">
          The search box in the header finds anything here that the person searching is allowed to see. Primary indexing (names and basic details of
          everything) is always on and refreshes every few minutes.
        </p>
      </header>
      {error ? <ErrorNotice error={error} /> : null}
      {saved ? (
        <p className="banner banner-ok" role="status">
          {saved}
        </p>
      ) : null}
      <div className="admin-grid">
        <div className="panel admin-box">
          <h2>Index</h2>
          <dl className="kv-compact">
            <dt>Primary</dt>
            <dd>
              {view.primary_count.toLocaleString()} items
              {view.primary_refreshed_at ? (
                <>
                  , refreshed <RelativeTime value={view.primary_refreshed_at} />
                </>
              ) : null}
            </dd>
            <dt>Deep</dt>
            <dd>{view.deep_count.toLocaleString()} files</dd>
          </dl>
          {job ? (
            <div className="index-progress">
              <div className="index-progress-head">
                <strong>{running ? "Indexing" : job.status === "done" ? "Last run finished" : job.status === "canceled" ? "Last run stopped" : "Last run failed"}</strong>
                <span className="meta">
                  {job.done.toLocaleString()} of {running ? "about " : ""}
                  {Math.max(job.total, job.done).toLocaleString()} files
                  {running ? ` · ${eta(job.eta_seconds)} left` : ""}
                </span>
              </div>
              <div
                className="capacity-bar"
                role="progressbar"
                aria-label="Indexing progress"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={job.percent ?? (job.status === "done" ? 100 : 0)}
              >
                <span style={{ width: `${job.percent ?? (job.status === "done" ? 100 : 0)}%` }} />
              </div>
              {job.error ? <p className="meta">{job.error}</p> : null}
              <p className="field-hint">
                Runs on the appliance, so you can leave this page. After an update or restart it carries on where it stopped, re-indexing the last batch
                to be safe.
              </p>
            </div>
          ) : null}
          <div className="btn-row">
            {running ? (
              <button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void act(cancelSearchIndex, "Indexing stopped.")}>
                Stop indexing
              </button>
            ) : (
              <button
                className="btn btn-primary"
                type="button"
                disabled={busy || changed}
                title={changed ? "Save the settings first" : undefined}
                onClick={() => void act(startSearchIndex, deep.files || deep.host_files ? "Indexing started." : "Primary index refreshed.")}
              >
                {deep.files || deep.host_files ? "Index everything now" : "Refresh primary index"}
              </button>
            )}
            <button className="btn btn-ghost" type="button" disabled={busy || running || view.deep_count === 0} onClick={() => setClearOpen(true)}>
              Clear deep index
            </button>
          </div>
        </div>
        <div className="panel admin-box">
          <h2>Deep indexing</h2>
          <Switch id="deep-files" checked={deep.files} onChange={(e) => setDeep({ files: e.target.checked })} label="File names in running system containers" />
          <Switch
            id="deep-host"
            checked={deep.host_files}
            disabled={!view.can_index_host}
            onChange={(e) => setDeep({ host_files: e.target.checked })}
            label="File names on the host (only SuperAdmins see these results)"
          />
          <label className="field">
            <span className="field-label">Folders to index (one per line)</span>
            <textarea rows={4} value={deep.roots.join("\n")} onChange={(e) => setDeep({ roots: lines(e.target.value) })} />
          </label>
          <label className="field">
            <span className="field-label">Skip folders named</span>
            <textarea rows={3} value={deep.exclude.join("\n")} onChange={(e) => setDeep({ exclude: lines(e.target.value) })} />
          </label>
          <div className="field-row">
            <label className="field">
              <span className="field-label">Folder depth</span>
              <input type="number" min={1} max={32} value={deep.max_depth} onChange={(e) => setDeep({ max_depth: Number(e.target.value) })} />
            </label>
            <label className="field">
              <span className="field-label">At most files</span>
              <input type="number" min={1000} max={5000000} step={1000} value={deep.max_files} onChange={(e) => setDeep({ max_files: Number(e.target.value) })} />
            </label>
          </div>
        </div>
      </div>
      <div className="panel admin-box">
        <h2>Filters in the search box</h2>
        <p className="field-hint">Each filter is a chip under the search box that narrows results to some kinds, a status, or adds words to the search.</p>
        <div className="filter-editor">
          {draft.filters.map((f, i) => (
            <div key={f.id} className="filter-row">
              <input aria-label="Filter label" value={f.label} maxLength={40} onChange={(e) => setFilter(i, { label: e.target.value })} />
              <select
                aria-label="Kinds"
                multiple
                value={f.kinds}
                onChange={(e) => setFilter(i, { kinds: Array.from(e.target.selectedOptions).map((o) => o.value) })}
              >
                {view.kinds.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.label}
                  </option>
                ))}
              </select>
              <input aria-label="Status" placeholder="Status, e.g. running" value={f.status ?? ""} onChange={(e) => setFilter(i, { status: e.target.value })} />
              <input aria-label="Extra words" placeholder="Extra words" value={f.query ?? ""} onChange={(e) => setFilter(i, { query: e.target.value })} />
              <button className="btn btn-ghost btn-sm" type="button" onClick={() => setDraft({ ...draft, filters: draft.filters.filter((_, j) => j !== i) })}>
                Remove
              </button>
            </div>
          ))}
          <button
            className="btn btn-secondary btn-sm"
            type="button"
            disabled={draft.filters.length >= 50}
            onClick={() => setDraft({ ...draft, filters: [...draft.filters, { id: "", label: "New filter", kinds: [] }] })}
          >
            Add filter
          </button>
        </div>
        <h3>Hidden from results</h3>
        <div className="chip-row">
          {view.kinds.map((k) => {
            const hidden = (draft.hidden_kinds ?? []).includes(k.id);
            return (
              <button
                key={k.id}
                type="button"
                className="chip"
                aria-pressed={hidden}
                onClick={() =>
                  setDraft({
                    ...draft,
                    hidden_kinds: hidden ? (draft.hidden_kinds ?? []).filter((x) => x !== k.id) : [...(draft.hidden_kinds ?? []), k.id],
                  })
                }
              >
                {hidden ? "Hidden: " : ""}
                {kindLabel(k.id)}
              </button>
            );
          })}
        </div>
      </div>
      <div className="sticky-actions">
        <button className="btn btn-secondary" type="button" disabled={!changed || busy} onClick={() => setDraft(view.settings)}>
          Discard
        </button>
        <button className="btn btn-primary" type="button" disabled={!changed || busy} onClick={() => void act(() => putSearchSettings(draft), "Search settings saved.")}>
          Save settings
        </button>
      </div>
      <ConfirmDialog
        open={clearOpen}
        title="Clear deep index"
        confirmLabel="Clear"
        danger
        onClose={() => setClearOpen(false)}
        onConfirm={() => {
          setClearOpen(false);
          void act(clearDeepSearchIndex, "Deep index cleared.");
        }}
      >
        <p>File names leave search results until the next indexing run. Primary results stay.</p>
      </ConfirmDialog>
    </section>
  );
}
