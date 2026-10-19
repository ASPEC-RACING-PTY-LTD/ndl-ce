import { useEffect, useMemo, useState } from "react";
import { confirmAction } from "../../confirm";
import {
  ApiError,
  automationFlowAction,
  createAutomationWebhook,
  deleteAutomationFlow,
  dryRunAutomationFlow,
  executeAutomationFlow,
  getAutomationFlow,
  getAutomationWebhook,
  listAutomationFlowVersions,
  patchAutomationFlow,
  type AutomationFlow,
  type AutomationProblem,
  type AutomationRun,
} from "../../api/client";
import type { AutomationWebhook, AutomationWebhookSecret } from "../../generated/openapi";
import { PageHeader } from "../../components/PageHeader";
import { ErrorNotice } from "../../components/ErrorNotice";
import { LoadingState } from "../../components/EmptyState";
import { StatusBadge } from "../../components/StatusBadge";
import { ResourceTable } from "../../components/ResourceTable";
import { Link } from "../../components/Link";
import { Tabs } from "../../ui/Tabs";
import { Dialog } from "../../ui/Dialog";
import { navigate, usePath } from "../../router";
import { useSession } from "../../session";
import { hasGrant } from "../../rbac";
import { Builder } from "../../automation/Builder";
import { ReportView, type Report } from "../../automation/ReportView";
import { CatalogueContext, EMPTY_CATALOGUE, eventFields, loadCatalogue, useCatalogue, type Catalogue } from "../../automation/catalogue";
import { problemsByStep, toDef, toModel, type Definition, type Model } from "../../automation/model";
import { RunsList, automationErrorAction, formatWhen } from "./AutomationHome";
import "../../automation/automation.css";

type Tab = "build" | "test" | "settings" | "runs" | "versions" | "webhook" | "json";

const LIMIT_LABELS: Record<string, [string, string]> = {
  max_loop_items: ["Items per loop", "A For each step stops after this many items."],
  max_steps: ["Steps per run", "Every step, including each loop pass, counts."],
  step_timeout_sec: ["Step time limit (seconds)", ""],
  run_timeout_sec: ["Run time limit (seconds)", "Includes delays."],
  concurrency: ["Runs at once", "More triggers wait in a queue, one at a time."],
  queue_depth: ["Queue length", "Triggers beyond this are dropped and logged."],
  per_minute: ["Starts per minute", "Manual runs do not count."],
  per_hour: ["Starts per hour", ""],
  debounce_sec: ["Ignore repeats for (seconds)", "The same event on the same target within this time starts only one run."],
  max_parallel: ["Parallel steps", ""],
  breaker_threshold: ["Turn off after failures in a row", "Failures and limit hits both count."],
};

function samplePayload(def: Definition, cat: Catalogue): string {
  if (def.trigger.type === "trigger.event") {
    const fields = eventFields(cat)[String(def.trigger.config?.kind ?? "")] ?? [];
    return JSON.stringify(Object.fromEntries(fields.map((f) => [f, f === "percent" ? 90 : ""])), null, 2);
  }
  if (def.trigger.type === "trigger.webhook") {
    return JSON.stringify({ example: "value" }, null, 2);
  }
  return "{}";
}

function TestPanel({ flow, def, onFlow }: { flow: AutomationFlow; def: Definition; onFlow: (f: AutomationFlow) => void }) {
  const cat = useCatalogue();
  const [payload, setPayload] = useState(() => samplePayload(def, cat));
  const [published, setPublished] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [run, setRun] = useState<AutomationRun | null>(null);
  async function test(sample?: "recent") {
    setBusy(true);
    setError(null);
    try {
      let body: Record<string, unknown> = {};
      if (!sample) {
        body = payload.trim() ? JSON.parse(payload) : {};
      }
      const res = await dryRunAutomationFlow(flow.id, { published, payload: body, ...(sample ? { sample } : {}) });
      setRun(res.run);
      if (res.flow) {
        onFlow(res.flow);
      }
    } catch (err) {
      setError(err instanceof SyntaxError ? new Error("The sample is not valid JSON") : err);
    } finally {
      setBusy(false);
    }
  }
  const report = (run?.report ?? null) as Report | null;
  return (
    <article className="panel stack">
      <p className="field-hint">
        A test run checks the trigger and its conditions, works out every expression, branch and loop for real, and records what each action step would do.
        Nothing is changed. Flows with steps that change things need a passing test of the exact draft before publishing.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="au-sample">
          Trigger data
        </label>
        <textarea id="au-sample" className="field-input au-json" rows={6} value={payload} onChange={(e) => setPayload(e.target.value)} />
      </div>
      <label className="check-row">
        <input type="checkbox" checked={published} disabled={!flow.published_version} onChange={(e) => setPublished(e.target.checked)} /> Test the published version
        instead of the draft
      </label>
      <div className="btn-row is-flush">
        <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void test()}>
          {busy ? "Testing" : "Run test"}
        </button>
        {def.trigger.type === "trigger.event" || def.trigger.type === "trigger.webhook" ? (
          <button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void test("recent")}>
            Test with the latest real {def.trigger.type === "trigger.event" ? "event" : "delivery"}
          </button>
        ) : null}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {run ? (
        <>
          <p className="meta">
            <StatusBadge status={run.status === "succeeded" ? "succeeded" : "failed"} label={run.status} /> {run.error}{" "}
            <Link href={`/automation/runs/${run.id}`}>Open the test run</Link>
          </p>
          {report ? <ReportView report={report} /> : null}
        </>
      ) : null}
    </article>
  );
}

function SettingsPanel({ flow, cat, mutate, onFlow }: { flow: AutomationFlow; cat: Catalogue; mutate: boolean; onFlow: (f: AutomationFlow) => void }) {
  const [name, setName] = useState(flow.name);
  const [description, setDescription] = useState(flow.description ?? "");
  const [limits, setLimits] = useState<Record<string, number>>((flow.limits ?? {}) as Record<string, number>);
  const [error, setError] = useState<unknown>(null);
  const [saved, setSaved] = useState(false);
  async function save() {
    try {
      onFlow(await patchAutomationFlow(flow.id, { name, description, limits }));
      setSaved(true);
      setError(null);
    } catch (err) {
      setError(err);
    }
  }
  const keys = Object.keys(LIMIT_LABELS).filter((k) => cat.limits[k] || limits[k] !== undefined);
  return (
    <article className="panel stack">
      <div className="au-fields">
        <div className="field">
          <label className="field-label" htmlFor="au-flow-name">
            Name
          </label>
          <input id="au-flow-name" className="field-input" value={name} disabled={!mutate} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field is-wide">
          <label className="field-label" htmlFor="au-flow-desc">
            Description
          </label>
          <textarea id="au-flow-desc" className="field-input" rows={2} value={description} disabled={!mutate} onChange={(e) => setDescription(e.target.value)} />
        </div>
      </div>
      <h3>Safety limits</h3>
      <div className="au-limits">
        {keys.map((k) => {
          const cap = cat.limits[k];
          const [label, hint] = LIMIT_LABELS[k];
          return (
            <div key={k} className="field">
              <label className="field-label" htmlFor={`au-limit-${k}`}>
                {label}
              </label>
              <input
                id={`au-limit-${k}`}
                className="field-input"
                type="number"
                min={cap?.min}
                max={cap?.max}
                disabled={!mutate}
                value={limits[k] ?? cap?.default ?? ""}
                onChange={(e) => setLimits({ ...limits, [k]: Number(e.target.value) })}
              />
              <p className="field-hint">
                {hint}
                {cap ? ` At most ${cap.max}.` : ""}
              </p>
            </div>
          );
        })}
      </div>
      <p className="field-hint">
        Always on: at most {cat.fixed_limits.max_running_flows ?? 8} flows run at once; the same stop, restart, migrate, script or prune runs on one target at most{" "}
        {cat.fixed_limits.destructive_per_target ?? 3} times in {cat.fixed_limits.destructive_window_minutes ?? 15} minutes across all flows; a flow never starts
        from its own run, and flows start each other at most {cat.fixed_limits.max_chain_depth ?? 3} deep.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {saved ? (
        <p className="banner banner-ok" role="status">
          Saved.
        </p>
      ) : null}
      {mutate ? (
        <div className="btn-row is-flush">
          <button className="btn btn-primary" type="button" onClick={() => void save()}>
            Save settings
          </button>
        </div>
      ) : null}
    </article>
  );
}

function WebhookPanel({ flow, mutate }: { flow: AutomationFlow; mutate: boolean }) {
  const [hook, setHook] = useState<AutomationWebhook | null>(null);
  const [secret, setSecret] = useState<AutomationWebhookSecret | null>(null);
  const [error, setError] = useState<unknown>(null);
  const reload = () =>
    getAutomationWebhook(flow.id)
      .then(setHook)
      .catch((err) => setError(err));
  useEffect(() => {
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flow.id]);
  async function make(rotate: boolean) {
    try {
      setSecret(await createAutomationWebhook(flow.id, rotate));
      await reload();
    } catch (err) {
      setError(err);
    }
  }
  const deliveries = (hook?.deliveries ?? []) as { id: string; status: number; run_id?: string; error?: string; at: string }[];
  return (
    <article className="panel stack">
      {error ? <ErrorNotice error={error} /> : null}
      {secret ? (
        <div className="banner" role="status">
          <p>Copy these now; they are not shown again.</p>
          <p>
            URL: <code>{`${window.location.origin}${secret.path}`}</code>
          </p>
          <p>
            Header: <code>{`${secret.secret_header ?? "X-Nodal-Webhook-Secret"}: ${secret.secret}`}</code>
          </p>
        </div>
      ) : null}
      {hook?.exists ? (
        <p className="meta">The webhook exists. Its token and secret were last set {formatWhen(hook.rotated_at)}. Bodies over 1 MB are refused; an Idempotency-Key header runs the flow once per key.</p>
      ) : (
        <p className="meta">No webhook yet.</p>
      )}
      {mutate ? (
        <div className="btn-row is-flush">
          {hook?.exists ? (
            <button className="btn btn-secondary" type="button" onClick={() => void make(true)}>
              Rotate token and secret
            </button>
          ) : (
            <button className="btn btn-primary" type="button" onClick={() => void make(false)}>
              Create webhook
            </button>
          )}
        </div>
      ) : null}
      <h3>Recent deliveries</h3>
      <ResourceTable
        empty={<p className="muted">None yet.</p>}
        headers={["When", "Answer", "Run", "Note"]}
        rows={deliveries.map((d) => [
          formatWhen(d.at),
          String(d.status),
          d.run_id ? (
            <Link key="r" href={`/automation/runs/${d.run_id}`}>
              {d.run_id.slice(0, 8)}
            </Link>
          ) : (
            ""
          ),
          d.error ?? "",
        ])}
      />
    </article>
  );
}

function VersionsPanel({ flow }: { flow: AutomationFlow }) {
  const [items, setItems] = useState<{ version: number; published_by: string; note: string; created_at: string; current: boolean }[] | null>(null);
  useEffect(() => {
    void listAutomationFlowVersions(flow.id)
      .then((r) => setItems((r.items ?? []) as never))
      .catch(() => setItems([]));
  }, [flow.id, flow.published_version]);
  if (!items) {
    return <LoadingState label="Loading versions" />;
  }
  return (
    <article className="panel">
      <ResourceTable
        empty={<p className="muted">Not published yet.</p>}
        headers={["Version", "Published by", "When", "Note"]}
        rows={items.map((v) => [`v${v.version}${v.current ? " (live)" : ""}`, v.published_by, formatWhen(v.created_at), v.note])}
      />
    </article>
  );
}

export function FlowEditorPage() {
  const path = usePath();
  const id = decodeURIComponent(path.split("/")[3] ?? "");
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "policy.apply");
  const runner = hasGrant(user, "policy.run");
  const owner = Boolean(user?.roles?.includes("admin"));
  const [flow, setFlow] = useState<AutomationFlow | null>(null);
  const [cat, setCat] = useState<Catalogue>(EMPTY_CATALOGUE);
  const [model, setModel] = useState<Model | null>(null);
  const [dirty, setDirty] = useState(false);
  const [problems, setProblems] = useState<AutomationProblem[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [tab, setTab] = useState<Tab>("build");
  const [json, setJson] = useState("");
  const [skip, setSkip] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    void Promise.all([getAutomationFlow(id), loadCatalogue()])
      .then(([f, c]) => {
        if (!live) return;
        setFlow(f);
        setCat(c);
        const def = f.draft as unknown as Definition;
        setModel(toModel(def));
        setJson(JSON.stringify(def, null, 2));
        setProblems(f.problems ?? []);
      })
      .catch((err) => live && setError(err));
    return () => {
      live = false;
    };
  }, [id]);

  const def = useMemo(() => (model && !model.unsupported ? toDef(model) : flow ? (flow.draft as unknown as Definition) : null), [model, flow]);
  const byStep = useMemo(() => (def ? problemsByStep(problems, def) : new Map()), [problems, def]);

  if (error && !flow) {
    return (
      <section className="page">
        <ErrorNotice error={error} action={automationErrorAction(error)} />
      </section>
    );
  }
  if (!flow || !model || !def) {
    return (
      <section className="page">
        <LoadingState label="Loading flow" />
      </section>
    );
  }

  async function save(next?: Definition): Promise<AutomationFlow | null> {
    if (!flow) return null;
    try {
      const f = await patchAutomationFlow(flow.id, { definition: (next ?? def) as unknown as Record<string, unknown> });
      setFlow(f);
      setProblems(f.problems ?? []);
      setDirty(false);
      setError(null);
      setJson(JSON.stringify(next ?? def, null, 2));
      return f;
    } catch (err) {
      setError(err);
      return null;
    }
  }

  async function act(fn: () => Promise<AutomationFlow>, done: string) {
    try {
      setFlow(await fn());
      setNotice(done);
      setError(null);
    } catch (err) {
      if (err instanceof ApiError && Array.isArray(err.body?.problems)) {
        setProblems(err.body?.problems as AutomationProblem[]);
        setTab("build");
      }
      if (err instanceof ApiError && err.body?.needs_dry_run) {
        setNotice("Run a test of this draft first (Test run tab), then publish.");
      }
      setError(err);
    }
  }

  async function publish(reason?: string) {
    if (dirty && !(await save())) {
      return;
    }
    await act(() => automationFlowAction(flow!.id, "publish", reason ? { skip_dry_run_reason: reason } : {}), "Published. Runs use the new version from now on.");
  }

  const state = !flow.published_version ? { s: "neutral", l: "Draft" } : flow.enabled ? { s: "running", l: "On" } : { s: "stopped", l: "Off" };
  const tabs: { id: Tab; label: string }[] = [
    { id: "build", label: "Build" },
    { id: "test", label: "Test run" },
    { id: "settings", label: "Settings" },
    { id: "runs", label: "Runs" },
    { id: "versions", label: "Versions" },
    ...(def.trigger.type === "trigger.webhook" ? [{ id: "webhook" as Tab, label: "Webhook" }] : []),
    { id: "json", label: "JSON" },
  ];
  return (
    <CatalogueContext.Provider value={cat}>
      <section className="page page-wide au-page" aria-labelledby="flow-heading">
        <PageHeader
          id="flow-heading"
          title={flow.name}
          kicker={
            <>
              <Link href="/automation">Automation</Link> · <StatusBadge status={state.s} label={state.l} />{" "}
              {flow.published_version ? `version ${flow.published_version}` : "never published"}
              {flow.draft_changed && flow.published_version ? " · the draft has unpublished changes" : ""}
            </>
          }
          actions={
            <div className="btn-row is-flush">
              {mutate ? (
                <button className="btn btn-secondary" type="button" disabled={!dirty} onClick={() => void save()}>
                  Save draft
                </button>
              ) : null}
              {mutate ? (
                <button className="btn btn-primary" type="button" onClick={() => void publish()}>
                  Publish
                </button>
              ) : null}
              {mutate && flow.published_version ? (
                <button
                  className="btn btn-secondary"
                  type="button"
                  onClick={() => void act(() => automationFlowAction(flow.id, flow.enabled ? "disable" : "enable"), flow.enabled ? "Turned off." : "Turned on.")}
                >
                  {flow.enabled ? "Turn off" : "Turn on"}
                </button>
              ) : null}
              {runner && flow.published_version ? (
                <button
                  className="btn btn-secondary"
                  type="button"
                  onClick={() =>
                    void executeAutomationFlow(flow.id)
                      .then((r) => navigate(`/automation/runs/${r.run.id}`))
                      .catch((err) => setError(err))
                  }
                >
                  Run now
                </button>
              ) : null}
              {mutate ? (
                <button
                  className="btn btn-ghost"
                  type="button"
                  onClick={() =>
                    void automationFlowAction(flow.id, "clone")
                      .then((c) => navigate(`/automation/flows/${c.id}`))
                      .catch((err) => setError(err))
                  }
                >
                  Clone
                </button>
              ) : null}
              {mutate ? (
                <button
                  className="btn btn-ghost"
                  type="button"
                  onClick={() => {
                    if (confirmAction(`Delete ${flow.name}? Its unfinished runs are cancelled.`)) {
                      void deleteAutomationFlow(flow.id)
                        .then(() => navigate("/automation"))
                        .catch((err) => setError(err));
                    }
                  }}
                >
                  Delete
                </button>
              ) : null}
            </div>
          }
        />
        {flow.disabled_reason ? <p className="banner">{flow.disabled_reason}</p> : null}
        {error ? <ErrorNotice error={error} action={automationErrorAction(error)} /> : null}
        {notice ? (
          <p className="banner banner-ok" role="status">
            {notice}
            {owner && /test of this draft/.test(notice) ? (
              <>
                {" "}
                <button className="btn btn-ghost btn-sm" type="button" onClick={() => setSkip("")}>
                  Publish without a test
                </button>
              </>
            ) : null}
          </p>
        ) : null}
        {(flow.destructive ?? []).length ? (
          <p className="field-hint">
            Steps {(flow.destructive ?? []).join(", ")} change things. {flow.dry_run_passed ? "The current draft passed a test run." : "Test this draft before publishing."}
          </p>
        ) : null}
        <Tabs ariaLabel="Flow" value={tab} onChange={setTab} options={tabs} />
        {tab === "build" ? (
          model.unsupported ? (
            <p className="banner">This flow has a shape the step builder cannot show ({model.unsupported}). Edit it on the JSON tab.</p>
          ) : (
            <>
              {problems.length ? (
                <p className="banner banner-error" role="alert">
                  {problems.length} problem{problems.length === 1 ? "" : "s"} to fix before publishing; they are marked on the steps.
                </p>
              ) : null}
              {problems
                .filter((p) => !byStep.has(p.path) && !/^nodes\[|^trigger/.test(p.path))
                .map((p, i) => (
                  <p key={i} className="au-field-error">
                    {p.path}: {p.message}
                  </p>
                ))}
              <Builder
                model={model}
                problems={byStep}
                onChange={(m) => {
                  setModel(m);
                  setDirty(true);
                }}
              />
            </>
          )
        ) : null}
        {tab === "test" ? (
          <TestPanel
            flow={flow}
            def={def}
            onFlow={(f) => {
              setFlow(f);
            }}
          />
        ) : null}
        {tab === "settings" ? <SettingsPanel flow={flow} cat={cat} mutate={mutate} onFlow={setFlow} /> : null}
        {tab === "runs" ? <RunsList flowId={flow.id} /> : null}
        {tab === "versions" ? <VersionsPanel flow={flow} /> : null}
        {tab === "webhook" ? <WebhookPanel flow={flow} mutate={mutate} /> : null}
        {tab === "json" ? (
          <article className="panel stack">
            <p className="field-hint">The whole definition. Positions are only used by a canvas; the runtime ignores them.</p>
            <textarea className="field-input au-json" aria-label="Definition JSON" rows={20} value={json} onChange={(e) => setJson(e.target.value)} />
            {mutate ? (
              <div className="btn-row is-flush">
                <button
                  className="btn btn-primary"
                  type="button"
                  onClick={() => {
                    try {
                      const next = JSON.parse(json) as Definition;
                      setModel(toModel(next));
                      void save(next);
                    } catch {
                      setError(new Error("The definition is not valid JSON"));
                    }
                  }}
                >
                  Save JSON
                </button>
              </div>
            ) : null}
          </article>
        ) : null}
        <Dialog
          open={skip != null}
          title="Publish without a test"
          onClose={() => setSkip(null)}
          footer={
            <div className="btn-row">
              <button className="btn btn-ghost" type="button" onClick={() => setSkip(null)}>
                Cancel
              </button>
              <button
                className="btn btn-primary"
                type="button"
                disabled={!skip?.trim()}
                onClick={() => {
                  const reason = skip ?? "";
                  setSkip(null);
                  void publish(reason);
                }}
              >
                Publish
              </button>
            </div>
          }
        >
          <div className="field">
            <label className="field-label" htmlFor="au-skip-reason">
              Why (kept in the audit log)
            </label>
            <textarea id="au-skip-reason" className="field-input" rows={3} value={skip ?? ""} onChange={(e) => setSkip(e.target.value)} />
          </div>
        </Dialog>
      </section>
    </CatalogueContext.Provider>
  );
}
