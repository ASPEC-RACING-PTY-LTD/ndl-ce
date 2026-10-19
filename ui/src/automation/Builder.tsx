import { useState } from "react";
import { Dialog } from "../ui/Dialog";
import { FieldsForm } from "./fields";
import { EVENT_OPS, PRESET_LABELS, eventFields, nodeInfo, useCatalogue, useTargets, type NodeTypeInfo } from "./catalogue";
import {
  dataRefs,
  insertStep,
  listAt,
  moveStep,
  newStepId,
  removeStep,
  stepsBefore,
  syncBranches,
  updateStep,
  whenDepth,
  whenText,
  type DefNode,
  type DefTrigger,
  type ListPath,
  type Model,
  type Step,
  type When,
} from "./model";

type Problems = Map<string, { path: string; message: string }[]>;

const CATEGORY_LABELS: Record<string, string> = { logic: "Logic", action: "Send and call", ndl: "No-dal" };

/** StepPicker is the catalogue of steps to add. */
function StepPicker({ open, onClose, onPick }: { open: boolean; onClose: () => void; onPick: (t: NodeTypeInfo) => void }) {
  const cat = useCatalogue();
  const [q, setQ] = useState("");
  const shown = cat.items.filter((n) => !q || `${n.display_name} ${n.description} ${n.type}`.toLowerCase().includes(q.toLowerCase()));
  return (
    <Dialog open={open} title="Add a step" wide onClose={onClose}>
      <input className="field-input" type="search" aria-label="Search steps" placeholder="Search steps" value={q} onChange={(e) => setQ(e.target.value)} />
      {(["logic", "action", "ndl"] as const).map((c) => {
        const items = shown.filter((n) => n.category === c);
        if (!items.length) {
          return null;
        }
        return (
          <section key={c}>
            <h3>{CATEGORY_LABELS[c]}</h3>
            <div className="au-catalogue">
              {items.map((n) => (
                <button key={n.type} type="button" onClick={() => onPick(n)}>
                  <strong>{n.display_name}</strong>
                  {n.destructive ? <span className="au-chip is-warn"> changes things</span> : null}
                  <small>{n.description}</small>
                </button>
              ))}
            </div>
          </section>
        );
      })}
    </Dialog>
  );
}

function defaultConfig(t: NodeTypeInfo): Record<string, unknown> {
  const cfg: Record<string, unknown> = {};
  for (const f of t.fields) {
    if (f.default !== undefined && f.default !== null) {
      cfg[f.key] = f.default;
    }
  }
  if (t.type === "logic.condition") {
    cfg.rules = [{ left: "", op: "==", right: "" }];
    cfg.match = "all";
  }
  if (t.type === "logic.switch") {
    cfg.cases = ["case1"];
  }
  return cfg;
}

function AddGap({ onAdd, label }: { onAdd: () => void; label: string }) {
  return (
    <div className="au-gap">
      <button type="button" className="au-add" aria-label={label} title={label} onClick={onAdd}>
        +
      </button>
    </div>
  );
}

function WhenEditor({ list, onChange, fields, depth = 1 }: { list: When[]; onChange: (next: When[]) => void; fields: string[]; depth?: number }) {
  const set = (i: number, c: When) => onChange(list.map((x, j) => (j === i ? c : x)));
  return (
    <div className="au-when-group">
      {list.map((c, i) => (
        <div key={i}>
          {c.group ? (
            <>
              <div className="au-input-row">
                {i > 0 ? (
                  <select className="field-input" aria-label="Join" value={c.join ?? "and"} onChange={(e) => set(i, { ...c, join: e.target.value as "and" | "or" })}>
                    <option value="and">and</option>
                    <option value="or">or</option>
                  </select>
                ) : (
                  <span className="meta">When</span>
                )}
                <span className="meta">a group where</span>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange(list.filter((_, j) => j !== i))}>
                  Remove group
                </button>
              </div>
              <WhenEditor list={c.group} fields={fields} depth={depth + 1} onChange={(g) => set(i, { ...c, group: g })} />
            </>
          ) : (
            <div className="au-when-row">
              {i > 0 ? (
                <select className="field-input" aria-label="Join" value={c.join ?? "and"} onChange={(e) => set(i, { ...c, join: e.target.value as "and" | "or" })}>
                  <option value="and">and</option>
                  <option value="or">or</option>
                </select>
              ) : (
                <span className="meta">When</span>
              )}
              <input className="field-input" aria-label="Field" list="au-event-fields" value={c.field ?? ""} onChange={(e) => set(i, { ...c, field: e.target.value })} />
              <select className="field-input" aria-label="Test" value={c.op ?? "eq"} onChange={(e) => set(i, { ...c, op: e.target.value })}>
                {EVENT_OPS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
              {c.op === "exists" ? (
                <span />
              ) : (
                <input className="field-input" aria-label="Value" value={String(c.value ?? "")} onChange={(e) => set(i, { ...c, value: e.target.value })} />
              )}
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange(list.filter((_, j) => j !== i))}>
                Remove
              </button>
            </div>
          )}
        </div>
      ))}
      <datalist id="au-event-fields">
        {fields.map((f) => (
          <option key={f} value={f} />
        ))}
      </datalist>
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange([...list, { join: "and", field: fields[0] ?? "", op: "eq", value: "" }])}>
          Add condition
        </button>
        {depth < 3 ? (
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange([...list, { join: "and", group: [{ field: fields[0] ?? "", op: "eq", value: "" }] }])}>
            Add group
          </button>
        ) : null}
      </div>
    </div>
  );
}

/** ScopePicker limits an event flow to events on chosen nodes. */
function ScopePicker({ value, onChange }: { value: unknown; onChange: (v: string[]) => void }) {
  const nodes = useTargets("node");
  const chosen = Array.isArray(value) ? (value as string[]) : [];
  if (nodes.length < 2 && chosen.length === 0) {
    return null;
  }
  return (
    <div className="field">
      <span className="field-label">Applies to</span>
      <div className="au-chips" role="group" aria-label="Applies to">
        {nodes.map((n) => (
          <label key={n.value} className="check-row">
            <input
              type="checkbox"
              checked={chosen.includes(n.value)}
              onChange={(e) => onChange(e.target.checked ? [...chosen, n.value] : chosen.filter((x) => x !== n.value))}
            />{" "}
            {n.label}
          </label>
        ))}
      </div>
      <p className="field-hint">{chosen.length ? "Only events on the ticked nodes start the flow." : "Events on every node start the flow."}</p>
    </div>
  );
}

function TriggerCard({ trigger, onChange, problems }: { trigger: DefTrigger; onChange: (t: DefTrigger) => void; problems?: { path: string; message: string }[] }) {
  const cat = useCatalogue();
  const cfg = trigger.config ?? {};
  const set = (patch: Record<string, unknown>) => onChange({ ...trigger, config: { ...cfg, ...patch } });
  const kind = String(cfg.kind ?? "");
  const conds = (Array.isArray(cfg.conditions) ? cfg.conditions : []) as When[];
  const fields = eventFields(cat)[kind] ?? [];
  const preset = String(cfg.preset ?? (cfg.cron ? "cron" : "hourly"));
  const pErr = (p: string) => problems?.find((x) => x.path.startsWith(`trigger.${p}`))?.message;
  return (
    <article className={`au-step is-trigger${problems?.length ? " is-problem" : ""}`} aria-label="Trigger">
      <div className="au-step-head">
        <div className="au-step-title">
          <strong>Trigger</strong>
          <span className="meta">How the flow starts</span>
        </div>
      </div>
      <div className="au-fields">
        <div className="field">
          <label className="field-label" htmlFor="au-trigger-type">
            Starts
          </label>
          <select
            id="au-trigger-type"
            className="field-input"
            value={trigger.type}
            onChange={(e) => onChange({ ...trigger, type: e.target.value, config: e.target.value === "trigger.schedule" ? { preset: "daily", at: "03:00", timezone: "UTC" } : {} })}
          >
            {(cat.triggers.length ? cat.triggers : [{ type: trigger.type, label: trigger.type }]).map((t) => (
              <option key={t.type} value={t.type}>
                {t.label}
              </option>
            ))}
          </select>
          {pErr("type") ? <span className="au-field-error">{pErr("type")}</span> : null}
        </div>
        {trigger.type === "trigger.schedule" ? (
          <>
            <div className="field">
              <label className="field-label" htmlFor="au-preset">
                How often
              </label>
              <select id="au-preset" className="field-input" value={preset} onChange={(e) => set({ preset: e.target.value })}>
                {(cat.schedule_presets.length ? cat.schedule_presets : Object.keys(PRESET_LABELS)).map((p) => (
                  <option key={p} value={p}>
                    {PRESET_LABELS[p] ?? p}
                  </option>
                ))}
              </select>
              {pErr("config.preset") ? <span className="au-field-error">{pErr("config.preset")}</span> : null}
            </div>
            {preset === "cron" ? (
              <div className="field">
                <label className="field-label" htmlFor="au-cron">
                  Cron line
                </label>
                <input id="au-cron" className="field-input" placeholder="30 2 * * 1-5" value={String(cfg.cron ?? "")} onChange={(e) => set({ cron: e.target.value })} />
                <p className="field-hint">minute hour day month weekday</p>
                {pErr("config.cron") ? <span className="au-field-error">{pErr("config.cron")}</span> : null}
              </div>
            ) : null}
            {preset === "daily" || preset === "weekly" ? (
              <div className="field">
                <label className="field-label" htmlFor="au-at">
                  At
                </label>
                <input id="au-at" className="field-input" type="time" value={String(cfg.at ?? "03:00")} onChange={(e) => set({ at: e.target.value })} />
              </div>
            ) : null}
            {preset === "weekly" ? (
              <div className="field">
                <label className="field-label" htmlFor="au-weekday">
                  On
                </label>
                <select id="au-weekday" className="field-input" value={String(cfg.weekday ?? 1)} onChange={(e) => set({ weekday: Number(e.target.value) })}>
                  {["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"].map((d, i) => (
                    <option key={d} value={i}>
                      {d}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
            <div className="field">
              <label className="field-label" htmlFor="au-tz">
                Timezone
              </label>
              <input id="au-tz" className="field-input" placeholder="UTC" value={String(cfg.timezone ?? "")} onChange={(e) => set({ timezone: e.target.value })} />
              {pErr("config.timezone") ? <span className="au-field-error">{pErr("config.timezone")}</span> : null}
            </div>
          </>
        ) : null}
        {trigger.type === "trigger.event" ? (
          <div className="field">
            <label className="field-label" htmlFor="au-event">
              When
            </label>
            <select id="au-event" className="field-input" value={kind} onChange={(e) => set({ kind: e.target.value })}>
              <option value="">Choose an event</option>
              {cat.events.map((ev) => (
                <option key={ev.kind} value={ev.kind}>
                  {ev.label}
                </option>
              ))}
            </select>
            {pErr("config.kind") ? <span className="au-field-error">{pErr("config.kind")}</span> : null}
          </div>
        ) : null}
        {trigger.type === "trigger.webhook" ? (
          <p className="field-hint is-wide">The flow starts when its webhook URL is called with the secret. Create the URL on the Webhook tab after publishing.</p>
        ) : null}
        {trigger.type === "trigger.manual" ? <p className="field-hint is-wide">Runs when someone presses Run, even while the flow is turned off.</p> : null}
      </div>
      {trigger.type === "trigger.event" ? <ScopePicker value={cfg.scope_nodes} onChange={(v) => set({ scope_nodes: v.length ? v : undefined })} /> : null}
      {trigger.type === "trigger.event" ? (
        <div>
          <span className="field-label">Only when (optional)</span>
          <WhenEditor list={conds} fields={fields} onChange={(next) => set({ conditions: next })} />
          {conds.length ? <p className="field-hint">Reads as: {whenText(conds)}. And binds tighter than or.</p> : null}
          {whenDepth(conds) > 3 ? <span className="au-field-error">Groups nest at most 3 levels deep.</span> : null}
          {pErr("config.conditions") ? <span className="au-field-error">{pErr("config.conditions")}</span> : null}
        </div>
      ) : null}
    </article>
  );
}

function StepCard({
  step,
  index,
  path,
  model,
  count,
  problems,
  onModel,
}: {
  step: Step;
  index: number;
  path: ListPath;
  model: Model;
  count: number;
  problems: Problems;
  onModel: (steps: Step[]) => void;
}) {
  const cat = useCatalogue();
  const info = nodeInfo(cat, step.node.type);
  const [open, setOpen] = useState(true);
  const [advanced, setAdvanced] = useState(false);
  const mine = problems.get(step.node.id) ?? [];
  const refs = dataRefs(model.trigger, stepsBefore(model.steps, path, index), eventFields(cat));
  const update = (node: DefNode) => onModel(updateStep(model.steps, path, index, node));
  const tone = info?.category === "logic" ? " is-logic" : "";
  return (
    <article className={`au-step${tone}${mine.length ? " is-problem" : ""}`} aria-label={`Step ${step.node.name || step.node.id}`}>
      <div className="au-step-head">
        <div className="au-step-title">
          <strong>{step.node.name || info?.display_name || step.node.type}</strong>
          <span className="meta">
            {info?.display_name ?? step.node.type} · {step.node.id}
            {info?.destructive ? " · changes things" : ""}
          </span>
        </div>
        <div className="au-step-tools">
          <button type="button" className="btn btn-ghost btn-sm" aria-label="Move up" disabled={index === 0} onClick={() => onModel(moveStep(model.steps, path, index, -1))}>
            Up
          </button>
          <button type="button" className="btn btn-ghost btn-sm" aria-label="Move down" disabled={index === count - 1} onClick={() => onModel(moveStep(model.steps, path, index, 1))}>
            Down
          </button>
          <button type="button" className="btn btn-ghost btn-sm" aria-expanded={open} onClick={() => setOpen(!open)}>
            {open ? "Collapse" : "Expand"}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" aria-label={`Delete step ${step.node.id}`} onClick={() => onModel(removeStep(model.steps, path, index))}>
            Delete
          </button>
        </div>
      </div>
      {open ? (
        <>
          <div className="au-fields">
            <div className="field">
              <label className="field-label" htmlFor={`au-${step.node.id}-name`}>
                Step name
              </label>
              <input id={`au-${step.node.id}-name`} className="field-input" value={step.node.name ?? ""} placeholder={info?.display_name} onChange={(e) => update({ ...step.node, name: e.target.value })} />
            </div>
          </div>
          <FieldsForm
            idPrefix={`au-${step.node.id}`}
            fields={info?.fields ?? []}
            config={step.node.config ?? {}}
            refs={refs}
            problems={mine}
            onChange={(config) => update({ ...step.node, config })}
          />
          {mine
            .filter((p) => !p.path.startsWith("config."))
            .map((p, i) => (
              <span key={i} className="au-field-error">
                {p.message}
              </span>
            ))}
          <button type="button" className="btn btn-ghost btn-sm" aria-expanded={advanced} onClick={() => setAdvanced(!advanced)}>
            {advanced ? "Hide error handling" : "Error handling and retries"}
          </button>
          {advanced ? (
            <div className="au-fields">
              <div className="field">
                <label className="field-label" htmlFor={`au-${step.node.id}-onerror`}>
                  If this step fails
                </label>
                <select
                  id={`au-${step.node.id}-onerror`}
                  className="field-input"
                  value={step.node.on_error ?? ""}
                  onChange={(e) => update({ ...step.node, on_error: e.target.value as DefNode["on_error"] })}
                >
                  <option value="">Stop the run (flow default)</option>
                  <option value="continue">Continue with the next step</option>
                  <option value="fail_branch">Run the If it fails branch</option>
                </select>
              </div>
              {info?.idempotent ? (
                <>
                  <div className="field">
                    <label className="field-label" htmlFor={`au-${step.node.id}-retry`}>
                      Retries
                    </label>
                    <select
                      id={`au-${step.node.id}-retry`}
                      className="field-input"
                      value={step.node.retry?.mode ?? "none"}
                      onChange={(e) =>
                        update({
                          ...step.node,
                          retry: e.target.value === "none" ? undefined : { mode: e.target.value as "fixed" | "exponential", max_attempts: step.node.retry?.max_attempts ?? 3, delay_ms: step.node.retry?.delay_ms ?? 5000 },
                        })
                      }
                    >
                      <option value="none">No retries</option>
                      <option value="fixed">Fixed delay</option>
                      <option value="exponential">Doubling delay</option>
                    </select>
                  </div>
                  {step.node.retry ? (
                    <>
                      <div className="field">
                        <label className="field-label" htmlFor={`au-${step.node.id}-attempts`}>
                          Attempts
                        </label>
                        <input
                          id={`au-${step.node.id}-attempts`}
                          className="field-input"
                          type="number"
                          min={1}
                          max={8}
                          value={step.node.retry.max_attempts}
                          onChange={(e) => update({ ...step.node, retry: { ...step.node.retry!, max_attempts: Number(e.target.value) } })}
                        />
                      </div>
                      <div className="field">
                        <label className="field-label" htmlFor={`au-${step.node.id}-delay`}>
                          First delay (ms)
                        </label>
                        <input
                          id={`au-${step.node.id}-delay`}
                          className="field-input"
                          type="number"
                          min={0}
                          value={step.node.retry.delay_ms ?? 0}
                          onChange={(e) => update({ ...step.node, retry: { ...step.node.retry!, delay_ms: Number(e.target.value) } })}
                        />
                      </div>
                    </>
                  ) : null}
                </>
              ) : (
                <p className="field-hint">This step is not retried automatically: running it twice could do the work twice.</p>
              )}
              <div className="field">
                <label className="field-label" htmlFor={`au-${step.node.id}-timeout`}>
                  Time limit (ms)
                </label>
                <input
                  id={`au-${step.node.id}-timeout`}
                  className="field-input"
                  type="number"
                  min={0}
                  value={step.node.timeout_ms ?? ""}
                  placeholder="flow default"
                  onChange={(e) => update({ ...step.node, timeout_ms: e.target.value ? Number(e.target.value) : undefined })}
                />
              </div>
            </div>
          ) : null}
          {step.branches.length ? (
            <div className="au-branches">
              {step.branches.map((b) => (
                <div key={b.port} className="au-branch">
                  <span className="au-branch-label">{b.label}</span>
                  <StepList steps={b.steps} path={[...path, index, b.port]} model={model} problems={problems} onModel={onModel} />
                </div>
              ))}
            </div>
          ) : null}
          {step.node.on_error === "fail_branch" ? (
            <div className="au-branch is-error">
              <span className="au-branch-label">If it fails</span>
              <StepList steps={step.errorSteps ?? []} path={[...path, index, "error"]} model={model} problems={problems} onModel={onModel} />
            </div>
          ) : null}
        </>
      ) : null}
    </article>
  );
}

export function StepList({
  steps,
  path,
  model,
  problems,
  onModel,
}: {
  steps: Step[];
  path: ListPath;
  model: Model;
  problems: Problems;
  onModel: (steps: Step[]) => void;
}) {
  const [adding, setAdding] = useState<number | null>(null);
  const where = path.length ? "here" : "after the trigger";
  return (
    <>
      <AddGap label={`Add a step ${where}`} onAdd={() => setAdding(0)} />
      {steps.map((s, i) => (
        <div key={s.node.id}>
          <StepCard step={s} index={i} path={path} model={model} count={steps.length} problems={problems} onModel={onModel} />
          <AddGap label={`Add a step after ${s.node.id}`} onAdd={() => setAdding(i + 1)} />
        </div>
      ))}
      <StepPicker
        open={adding !== null}
        onClose={() => setAdding(null)}
        onPick={(t) => {
          const node: DefNode = { id: newStepId(model.steps, t.type), type: t.type, config: defaultConfig(t) };
          const step: Step = { node, branches: syncBranches(node, []) };
          onModel(insertStep(model.steps, path, adding ?? listAt(model.steps, path).length, step));
          setAdding(null);
        }}
      />
    </>
  );
}

export function Builder({ model, onChange, problems }: { model: Model; onChange: (m: Model) => void; problems: Problems }) {
  return (
    <div className="au-builder">
      <TriggerCard trigger={model.trigger} problems={problems.get("trigger")} onChange={(trigger) => onChange({ ...model, trigger })} />
      <StepList steps={model.steps} path={[]} model={model} problems={problems} onModel={(steps) => onChange({ ...model, steps })} />
    </div>
  );
}

