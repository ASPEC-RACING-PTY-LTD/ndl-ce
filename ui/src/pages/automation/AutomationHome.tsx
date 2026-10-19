import { useEffect, useState } from "react";
import {
  ApiError,
  createAutomationFlow,
  deleteAutomationConnection,
  listAutomationConnections,
  listAutomationFlows,
  listAutomationRuns,
  listAutomationTemplates,
  saveAutomationConnection,
  testAutomationConnection,
  type AutomationConnection,
  type AutomationFlow,
  type AutomationRun,
} from "../../api/client";
import { PageHeader } from "../../components/PageHeader";
import { ErrorNotice } from "../../components/ErrorNotice";
import { EmptyState, LoadingState } from "../../components/EmptyState";
import { ResultsToolbar, useResultsView } from "../../components/ResultsToolbar";
import { ResourceTable } from "../../components/ResourceTable";
import { StatusBadge } from "../../components/StatusBadge";
import { Link } from "../../components/Link";
import { Tabs } from "../../ui/Tabs";
import { Dialog } from "../../ui/Dialog";
import { navigate } from "../../router";
import { useSession } from "../../session";
import { hasGrant } from "../../rbac";
import { STATUS_LABELS, statusTone } from "../../automation/catalogue";
import "../../automation/automation.css";

type Tab = "flows" | "runs" | "connections" | "templates";

const TRIGGER_LABELS: Record<string, string> = { manual: "Run by hand", schedule: "Schedule", webhook: "Webhook", event: "Event" };

export function formatWhen(iso?: string | null): string {
  if (!iso) {
    return "";
  }
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function automationErrorAction(err: unknown) {
  if (err instanceof ApiError && err.status === 409 && /automation is disabled/i.test(err.message)) {
    return (
      <>
        Turn on Automation in <Link href="/settings/features">Features</Link>.
      </>
    );
  }
  return undefined;
}

function flowTrigger(f: AutomationFlow): string {
  const draft = (f.draft ?? {}) as { trigger?: { type?: string } };
  const kind = f.trigger_kind || (draft.trigger?.type ?? "").replace("trigger.", "");
  return TRIGGER_LABELS[kind] ?? kind;
}

function flowState(f: AutomationFlow): { status: string; label: string } {
  if (!f.published_version) {
    return { status: "neutral", label: "Draft" };
  }
  if (f.enabled) {
    return { status: "running", label: "On" };
  }
  return { status: f.disabled_reason ? "warning" : "stopped", label: "Off" };
}

function FlowsTab({ mutate }: { mutate: boolean }) {
  const [items, setItems] = useState<AutomationFlow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [sort, setSort] = useState("name");
  const [view, setView] = useResultsView("ndl.automation.flows.view");
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");

  useEffect(() => {
    void listAutomationFlows()
      .then((r) => setItems(r.items ?? []))
      .catch((err) => setError(err));
  }, []);

  async function create() {
    try {
      const f = await createAutomationFlow({ name: name.trim() });
      navigate(`/automation/flows/${f.id}`);
    } catch (err) {
      setError(err);
    }
  }

  if (error && items == null) {
    return <ErrorNotice error={error} action={automationErrorAction(error)} />;
  }
  if (items == null) {
    return <LoadingState label="Loading flows" />;
  }
  const q = query.trim().toLowerCase();
  const shown = items
    .filter((f) => {
      if (filter === "on" && !(f.enabled && f.published_version)) return false;
      if (filter === "off" && (f.enabled || !f.published_version)) return false;
      if (filter === "draft" && f.published_version) return false;
      if (filter === "attention" && !(f.disabled_reason || (f.last_run as { status?: string } | undefined)?.status === "failed")) return false;
      return !q || `${f.name} ${f.description ?? ""} ${flowTrigger(f)}`.toLowerCase().includes(q);
    })
    .sort((a, b) => {
      if (sort === "updated") return String(b.updated_at ?? "").localeCompare(String(a.updated_at ?? ""));
      if (sort === "last_run") return String((b.last_run as { created_at?: string })?.created_at ?? "").localeCompare(String((a.last_run as { created_at?: string })?.created_at ?? ""));
      return a.name.localeCompare(b.name);
    });
  return (
    <>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="btn-row is-flush">
        {mutate ? (
          <button className="btn btn-primary" type="button" onClick={() => setCreating(true)}>
            New flow
          </button>
        ) : null}
      </div>
      {items.length === 0 ? (
        <EmptyState title="No flows yet">
          A flow is a trigger and the steps it runs: restart a container when it turns unhealthy, snapshot workloads every night, or call a URL when a
          backup fails. Start from a template or build one step by step.
        </EmptyState>
      ) : (
        <>
          <ResultsToolbar
            label="Flows"
            query={query}
            onQuery={setQuery}
            placeholder="Search flows"
            filter={filter}
            filters={[
              { value: "all", label: "All flows" },
              { value: "on", label: "On" },
              { value: "off", label: "Off" },
              { value: "draft", label: "Never published" },
              { value: "attention", label: "Needs attention" },
            ]}
            onFilter={setFilter}
            filterLabel="State"
            sort={sort}
            sorts={[
              { value: "name", label: "Name" },
              { value: "updated", label: "Last edited" },
              { value: "last_run", label: "Last run" },
            ]}
            onSort={setSort}
            view={view}
            onView={setView}
          />
          {shown.length === 0 ? (
            <p className="muted">No flows match the current filters.</p>
          ) : view === "table" ? (
            <article className="panel">
              <ResourceTable
                headers={["Flow", "Starts", "State", "Version", "Last run"]}
                onRowClick={(i) => navigate(`/automation/flows/${shown[i].id}`)}
                rows={shown.map((f) => {
                  const st = flowState(f);
                  const last = f.last_run as { status?: string; created_at?: string } | undefined;
                  return [
                    <Link key="n" href={`/automation/flows/${f.id}`}>
                      {f.name}
                    </Link>,
                    flowTrigger(f),
                    <StatusBadge key="s" status={st.status} label={st.label} />,
                    f.published_version ? `v${f.published_version}${f.draft_changed ? " (edited)" : ""}` : "Draft",
                    last ? <StatusBadge key="l" status={statusTone(last.status)} label={`${STATUS_LABELS[last.status ?? ""] ?? last.status} ${formatWhen(last.created_at)}`} /> : "Never",
                  ];
                })}
              />
            </article>
          ) : (
            <div className="au-grid">
              {shown.map((f) => {
                const st = flowState(f);
                const last = f.last_run as { status?: string; created_at?: string } | undefined;
                return (
                  <article key={f.id} className="au-card" aria-label={`Flow ${f.name}`}>
                    <header className="au-card-head">
                      <Link className="au-card-title" href={`/automation/flows/${f.id}`}>
                        {f.name}
                      </Link>
                      <StatusBadge status={st.status} label={st.label} />
                    </header>
                    {f.description ? <p className="meta">{f.description}</p> : null}
                    <div className="au-chips">
                      <span className="au-chip">{flowTrigger(f)}</span>
                      <span className="au-chip">{f.published_version ? `v${f.published_version}` : "Draft"}</span>
                      {f.draft_changed && f.published_version ? <span className="au-chip is-warn">Unpublished changes</span> : null}
                      {(f.destructive ?? []).length ? <span className="au-chip">Changes things</span> : null}
                      {last ? <span className={`au-chip${last.status === "failed" ? " is-bad" : last.status === "succeeded" ? " is-ok" : ""}`}>{STATUS_LABELS[last.status ?? ""] ?? last.status}</span> : null}
                    </div>
                    {f.disabled_reason ? <p className="tunnel-reason">{f.disabled_reason}</p> : null}
                    {last ? <p className="meta">Last run {formatWhen(last.created_at)}</p> : <p className="meta">Not run yet</p>}
                  </article>
                );
              })}
            </div>
          )}
        </>
      )}
      <Dialog
        open={creating}
        title="New flow"
        onClose={() => setCreating(false)}
        footer={
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setCreating(false)}>
              Cancel
            </button>
            <button className="btn btn-primary" type="button" disabled={!name.trim()} onClick={() => void create()}>
              Create
            </button>
          </div>
        }
      >
        <div className="field">
          <label className="field-label" htmlFor="au-new-name">
            Name
          </label>
          <input id="au-new-name" className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
      </Dialog>
    </>
  );
}

export function RunsList({ flowId }: { flowId?: string }) {
  const [items, setItems] = useState<AutomationRun[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState("newest");
  const [view, setView] = useResultsView("ndl.automation.runs.view", "table");

  useEffect(() => {
    let live = true;
    const load = () =>
      listAutomationRuns({ flow_id: flowId, status: filter, limit: 200 })
        .then((r) => live && setItems(r.items ?? []))
        .catch((err) => live && setError(err));
    void load();
    const t = window.setInterval(() => void load(), 10000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [flowId, filter]);

  if (error && items == null) {
    return <ErrorNotice error={error} action={automationErrorAction(error)} />;
  }
  if (items == null) {
    return <LoadingState label="Loading runs" />;
  }
  const q = query.trim().toLowerCase();
  const shown = items
    .filter((r) => !q || `${r.flow_name ?? ""} ${r.started_by ?? ""} ${r.trigger_kind ?? ""} ${r.error ?? ""}`.toLowerCase().includes(q))
    .sort((a, b) => (sort === "oldest" ? -1 : 1) * String(b.created_at ?? "").localeCompare(String(a.created_at ?? "")));
  return (
    <>
      <ResultsToolbar
        label="Runs"
        query={query}
        onQuery={setQuery}
        placeholder="Search runs"
        filter={filter}
        filters={[{ value: "", label: "Every status" }, ...Object.entries(STATUS_LABELS).filter(([k]) => !["skipped", "retrying"].includes(k)).map(([value, label]) => ({ value, label }))]}
        onFilter={setFilter}
        filterLabel="Status"
        sort={sort}
        sorts={[
          { value: "newest", label: "Newest" },
          { value: "oldest", label: "Oldest" },
        ]}
        onSort={setSort}
        view={view}
        onView={setView}
      />
      {shown.length === 0 ? (
        <p className="muted">No runs yet.</p>
      ) : view === "table" ? (
        <article className="panel">
          <ResourceTable
            headers={["Run", "Flow", "Status", "Started by", "When", "Note"]}
            onRowClick={(i) => navigate(`/automation/runs/${shown[i].id}`)}
            rows={shown.map((r) => [
              <Link key="id" href={`/automation/runs/${r.id}`}>
                {r.mode === "dry_run" ? "Test " : ""}
                {r.id.slice(0, 8)}
              </Link>,
              r.flow_name ?? "",
              <StatusBadge key="s" status={statusTone(r.status)} label={STATUS_LABELS[r.status] ?? r.status} />,
              r.started_by || r.trigger_kind || "",
              formatWhen(r.created_at),
              <span key="e" className="meta">
                {r.error ?? ""}
              </span>,
            ])}
          />
        </article>
      ) : (
        <div className="au-grid">
          {shown.map((r) => (
            <article key={r.id} className="au-card">
              <header className="au-card-head">
                <Link className="au-card-title" href={`/automation/runs/${r.id}`}>
                  {r.flow_name || r.id.slice(0, 8)}
                </Link>
                <StatusBadge status={statusTone(r.status)} label={STATUS_LABELS[r.status] ?? r.status} />
              </header>
              <div className="au-chips">
                {r.mode === "dry_run" ? <span className="au-chip">Test run</span> : null}
                <span className="au-chip">{r.started_by || r.trigger_kind}</span>
              </div>
              <p className="meta">{formatWhen(r.created_at)}</p>
              {r.error ? <p className="tunnel-reason">{r.error}</p> : null}
            </article>
          ))}
        </div>
      )}
    </>
  );
}

type ConnDraft = { id?: string; name: string; kind: string; testUrl: string; allowPrivate: boolean; token: string; username: string; password: string; headers: string };

function ConnectionsTab({ mutate }: { mutate: boolean }) {
  const [items, setItems] = useState<AutomationConnection[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<ConnDraft | null>(null);
  const [notice, setNotice] = useState("");

  const reload = () =>
    listAutomationConnections()
      .then((r) => setItems(r.items ?? []))
      .catch((err) => setError(err));
  useEffect(() => {
    void reload();
  }, []);

  async function save() {
    if (!draft) {
      return;
    }
    const secret: Record<string, unknown> = {};
    if (draft.token) secret.token = draft.token;
    if (draft.username) secret.username = draft.username;
    if (draft.password) secret.password = draft.password;
    if (draft.headers.trim()) {
      try {
        secret.headers = JSON.parse(draft.headers);
      } catch {
        setError(new Error("Headers must be a JSON object, such as {\"X-Key\": \"value\"}"));
        return;
      }
    }
    try {
      await saveAutomationConnection(
        {
          name: draft.name.trim(),
          kind: draft.kind,
          config: { ...(draft.testUrl ? { test_url: draft.testUrl } : {}), ...(draft.allowPrivate ? { allow_private_network: true } : {}) },
          ...(Object.keys(secret).length || !draft.id ? { secret } : {}),
        },
        draft.id,
      );
      setDraft(null);
      await reload();
    } catch (err) {
      setError(err);
    }
  }

  if (error && items == null) {
    return <ErrorNotice error={error} action={automationErrorAction(error)} />;
  }
  if (items == null) {
    return <LoadingState label="Loading connections" />;
  }
  return (
    <>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner" role="status">
          {notice}
        </p>
      ) : null}
      <p className="field-hint">
        Saved credentials for HTTP steps. Secrets are encrypted and never shown again; a token is sent as Authorization: Bearer.
      </p>
      {mutate ? (
        <div className="btn-row is-flush">
          <button
            className="btn btn-primary"
            type="button"
            onClick={() => setDraft({ name: "", kind: "bearer", testUrl: "", allowPrivate: false, token: "", username: "", password: "", headers: "" })}
          >
            New connection
          </button>
        </div>
      ) : null}
      {items.length === 0 ? (
        <EmptyState title="No connections yet">Add one to call an outside API with a token or password without writing it into a flow.</EmptyState>
      ) : (
        <article className="panel">
          <ResourceTable
            headers={["Name", "Kind", "Holds", "Updated", ""]}
            rows={items.map((c) => [
              c.name,
              c.kind,
              (c.secret_fields ?? []).join(", ") || "nothing secret",
              formatWhen(c.updated_at),
              mutate ? (
                <div key="a" className="btn-row is-flush">
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    onClick={() =>
                      void testAutomationConnection(c.id)
                        .then((r) => setNotice(`${c.name}: ${r.ok ? "works" : "failed"}${r.status ? ` (HTTP ${r.status})` : ""}${r.error ? `: ${r.error}` : ""}${r.message ? `: ${r.message}` : ""}`))
                        .catch((err) => setError(err))
                    }
                  >
                    Test
                  </button>
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    onClick={() => {
                      const cfg = (c.config ?? {}) as { test_url?: string; allow_private_network?: boolean };
                      setDraft({ id: c.id, name: c.name, kind: c.kind, testUrl: cfg.test_url ?? "", allowPrivate: Boolean(cfg.allow_private_network), token: "", username: "", password: "", headers: "" });
                    }}
                  >
                    Edit
                  </button>
                  <button
                    className="btn btn-ghost btn-sm"
                    type="button"
                    onClick={() =>
                      void deleteAutomationConnection(c.id)
                        .then(reload)
                        .catch((err) => setError(err))
                    }
                  >
                    Delete
                  </button>
                </div>
              ) : (
                ""
              ),
            ])}
          />
        </article>
      )}
      <Dialog
        open={draft != null}
        title={draft?.id ? "Edit connection" : "New connection"}
        onClose={() => setDraft(null)}
        footer={
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setDraft(null)}>
              Cancel
            </button>
            <button className="btn btn-primary" type="button" disabled={!draft?.name.trim()} onClick={() => void save()}>
              Save
            </button>
          </div>
        }
      >
        {draft ? (
          <div className="stack">
            <div className="field">
              <label className="field-label" htmlFor="au-conn-name">
                Name
              </label>
              <input id="au-conn-name" className="field-input" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="au-conn-kind">
                Kind
              </label>
              <select id="au-conn-kind" className="field-input" value={draft.kind} onChange={(e) => setDraft({ ...draft, kind: e.target.value })}>
                <option value="bearer">Bearer token</option>
                <option value="basic">User name and password</option>
                <option value="headers">Custom headers</option>
                <option value="http">Other</option>
              </select>
            </div>
            {draft.kind === "bearer" || draft.kind === "http" ? (
              <div className="field">
                <label className="field-label" htmlFor="au-conn-token">
                  Token
                </label>
                <input id="au-conn-token" className="field-input" type="password" autoComplete="off" placeholder={draft.id ? "Leave empty to keep" : ""} value={draft.token} onChange={(e) => setDraft({ ...draft, token: e.target.value })} />
              </div>
            ) : null}
            {draft.kind === "basic" ? (
              <>
                <div className="field">
                  <label className="field-label" htmlFor="au-conn-user">
                    User name
                  </label>
                  <input id="au-conn-user" className="field-input" autoComplete="off" value={draft.username} onChange={(e) => setDraft({ ...draft, username: e.target.value })} />
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="au-conn-pass">
                    Password
                  </label>
                  <input id="au-conn-pass" className="field-input" type="password" autoComplete="off" value={draft.password} onChange={(e) => setDraft({ ...draft, password: e.target.value })} />
                </div>
              </>
            ) : null}
            {draft.kind === "headers" || draft.kind === "http" ? (
              <div className="field">
                <label className="field-label" htmlFor="au-conn-headers">
                  Headers (JSON)
                </label>
                <textarea id="au-conn-headers" className="field-input au-json" rows={3} value={draft.headers} onChange={(e) => setDraft({ ...draft, headers: e.target.value })} />
              </div>
            ) : null}
            <div className="field">
              <label className="field-label" htmlFor="au-conn-test">
                Test URL (optional)
              </label>
              <input id="au-conn-test" className="field-input" placeholder="https://api.example.com/me" value={draft.testUrl} onChange={(e) => setDraft({ ...draft, testUrl: e.target.value })} />
            </div>
            <label className="check-row">
              <input type="checkbox" checked={draft.allowPrivate} onChange={(e) => setDraft({ ...draft, allowPrivate: e.target.checked })} /> Allow private network addresses for the test
            </label>
          </div>
        ) : null}
      </Dialog>
    </>
  );
}

type Template = { id: string; name: string; description: string };

function TemplatesTab({ mutate }: { mutate: boolean }) {
  const [items, setItems] = useState<Template[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    void listAutomationTemplates()
      .then((r) => setItems((r.items ?? []) as unknown as Template[]))
      .catch((err) => setError(err));
  }, []);
  if (error && items == null) {
    return <ErrorNotice error={error} action={automationErrorAction(error)} />;
  }
  if (items == null) {
    return <LoadingState label="Loading templates" />;
  }
  return (
    <>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="au-grid">
        {items.map((t) => (
          <article key={t.id} className="au-card" aria-label={`Template ${t.name}`}>
            <header className="au-card-head">
              <span className="au-card-title">{t.name}</span>
            </header>
            <p className="meta">{t.description}</p>
            {mutate ? (
              <div className="btn-row is-flush">
                <button
                  className="btn btn-secondary btn-sm"
                  type="button"
                  onClick={() =>
                    void createAutomationFlow({ template: t.id })
                      .then((f) => navigate(`/automation/flows/${f.id}`))
                      .catch((err) => setError(err))
                  }
                >
                  Use this template
                </button>
              </div>
            ) : null}
          </article>
        ))}
      </div>
    </>
  );
}

export function AutomationHome() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "policy.apply");
  const [tab, setTab] = useState<Tab>(() => {
    const t = new URLSearchParams(window.location.search).get("tab");
    return t === "runs" || t === "connections" || t === "templates" ? t : "flows";
  });
  return (
    <section className="page page-wide au-page" aria-labelledby="automation-heading">
      <PageHeader
        id="automation-heading"
        title="Automation"
        kicker="Flows run steps when something happens, on a schedule or by hand. Each step uses the same checks, jobs and audit log as doing it yourself, and runs as the Automation identity."
      />
      <Tabs
        ariaLabel="Automation"
        value={tab}
        onChange={(t) => {
          setTab(t);
          window.history.replaceState(window.history.state, "", t === "flows" ? "/automation" : `/automation?tab=${t}`);
        }}
        options={[
          { id: "flows", label: "Flows" },
          { id: "runs", label: "Runs" },
          { id: "connections", label: "Connections" },
          { id: "templates", label: "Templates" },
        ]}
      />
      {tab === "flows" ? <FlowsTab mutate={mutate} /> : null}
      {tab === "runs" ? <RunsList /> : null}
      {tab === "connections" ? <ConnectionsTab mutate={mutate} /> : null}
      {tab === "templates" ? <TemplatesTab mutate={mutate} /> : null}
      <p className="meta">
        Older policies are still listed on <Link href="/automation/policies">Policies</Link>; each one also has an equivalent flow here.
      </p>
    </section>
  );
}
