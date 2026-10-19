import { useId, useState } from "react";
import { Dialog } from "../ui/Dialog";
import { useTargets, type CatalogueField } from "./catalogue";
import { RULE_OPS, expressionToRules, rulesToExpression, type DataRef, type Rule } from "./model";

type Problems = { path: string; message: string }[];

function problemFor(problems: Problems | undefined, key: string): string | undefined {
  return problems?.find((p) => p.path === `config.${key}` || p.path.startsWith(`config.${key}.`) || p.path.startsWith(`config.${key}[`))?.message;
}

/** DynamicContent lists the trigger's and earlier steps' data and inserts a {{ }} reference. */
export function DynamicContent({ refs, onPick, label = "Dynamic content" }: { refs: DataRef[]; onPick: (ref: string) => void; label?: string }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const groups = new Map<string, DataRef[]>();
  for (const r of refs) {
    if (q && !`${r.group} ${r.label} ${r.ref}`.toLowerCase().includes(q.toLowerCase())) {
      continue;
    }
    groups.set(r.group, [...(groups.get(r.group) ?? []), r]);
  }
  return (
    <>
      <button type="button" className="btn btn-ghost btn-sm" title="Insert dynamic content" aria-label={label} onClick={() => setOpen(true)}>
        {"{ }"}
      </button>
      <Dialog open={open} title="Dynamic content" onClose={() => setOpen(false)}>
        <input className="field-input" type="search" aria-label="Search dynamic content" placeholder="Search" value={q} onChange={(e) => setQ(e.target.value)} />
        <div className="au-picker">
          {[...groups.entries()].map(([group, items]) => (
            <div key={group} className="au-picker-group">
              <span>{group}</span>
              {items.map((r) => (
                <button
                  key={r.ref}
                  type="button"
                  className="btn btn-secondary btn-sm"
                  title={`{{ ${r.ref} }}`}
                  onClick={() => {
                    onPick(`{{ ${r.ref} }}`);
                    setOpen(false);
                  }}
                >
                  {r.label}
                </button>
              ))}
            </div>
          ))}
          {groups.size === 0 ? <p className="muted">Nothing matches.</p> : null}
        </div>
      </Dialog>
    </>
  );
}

function asText(v: unknown): string {
  if (v == null) {
    return "";
  }
  if (typeof v === "string") {
    return v;
  }
  return JSON.stringify(v);
}

function TextInput({
  id,
  value,
  onChange,
  refs,
  placeholder,
  multiline,
  list,
}: {
  id: string;
  value: unknown;
  onChange: (v: string) => void;
  refs: DataRef[];
  placeholder?: string;
  multiline?: boolean;
  list?: string;
}) {
  const text = asText(value);
  return (
    <div className="au-input-row">
      {multiline ? (
        <textarea id={id} className="field-input" rows={3} value={text} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <input id={id} className="field-input" value={text} placeholder={placeholder} list={list} onChange={(e) => onChange(e.target.value)} />
      )}
      <DynamicContent refs={refs} onPick={(ref) => onChange(text ? `${text}${text.endsWith(" ") ? "" : " "}${ref}` : ref)} />
    </div>
  );
}

function TargetInput({ id, field, value, onChange, refs }: { id: string; field: CatalogueField; value: unknown; onChange: (v: unknown) => void; refs: DataRef[] }) {
  const items = useTargets(field.target);
  const listId = `${id}-list`;
  if (field.target === "channel") {
    const chosen = Array.isArray(value) ? (value as string[]) : [];
    return (
      <div className="au-chips" role="group" aria-label={field.label}>
        {items.length === 0 ? <span className="meta">No channels yet; every channel set up under Alerts is used.</span> : null}
        {items.map((c) => (
          <label key={c.value} className="check-row">
            <input
              type="checkbox"
              checked={chosen.includes(c.value)}
              onChange={(e) => onChange(e.target.checked ? [...chosen, c.value] : chosen.filter((x) => x !== c.value))}
            />{" "}
            {c.label}
          </label>
        ))}
      </div>
    );
  }
  return (
    <>
      <TextInput id={id} value={value} onChange={onChange} refs={refs} placeholder={field.placeholder ?? "Pick or type"} list={listId} />
      <datalist id={listId}>
        {items.map((t) => (
          <option key={t.value} value={t.value}>
            {t.label}
            {t.meta ? ` (${t.meta})` : ""}
          </option>
        ))}
      </datalist>
    </>
  );
}

function JsonInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  const [text, setText] = useState(() => (value == null ? "" : typeof value === "string" ? value : JSON.stringify(value, null, 2)));
  const [bad, setBad] = useState("");
  return (
    <>
      <textarea
        id={id}
        className="field-input au-json"
        rows={4}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => {
          if (text.trim() === "") {
            setBad("");
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(text));
            setBad("");
          } catch {
            // Plain text (with {{ }} references) is allowed as well.
            onChange(text);
            setBad(text.trim().startsWith("{") || text.trim().startsWith("[") ? "Not valid JSON; it is sent as text." : "");
          }
        }}
      />
      {bad ? <span className="au-field-error">{bad}</span> : null}
    </>
  );
}

function HeadersInput({ id, value, onChange, refs }: { id: string; value: unknown; onChange: (v: unknown) => void; refs: DataRef[] }) {
  const obj = value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
  const rows = Object.entries(obj);
  function set(next: [string, unknown][]) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of next) {
      if (k.trim()) {
        out[k.trim()] = v;
      }
    }
    onChange(Object.keys(out).length ? out : undefined);
  }
  return (
    <div className="au-rules" id={id}>
      {rows.map(([k, v], i) => (
        <div key={i} className="au-rule">
          <input className="field-input" aria-label="Name" value={k} onChange={(e) => set(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))} />
          <span className="meta">=</span>
          <TextInput id={`${id}-${i}`} value={v} refs={refs} onChange={(nv) => set(rows.map((r, j) => (j === i ? [r[0], nv] : r)))} />
          <button type="button" className="btn btn-ghost btn-sm" aria-label="Remove" onClick={() => set(rows.filter((_, j) => j !== i))}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => set([...rows, [`name${rows.length + 1}`, ""]])}>
        Add
      </button>
    </div>
  );
}

function CasesInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  const list = Array.isArray(value) ? value.map((c) => (typeof c === "string" ? c : String((c as { value?: unknown })?.value ?? ""))) : [];
  return (
    <div className="au-rules" id={id}>
      {list.map((c, i) => (
        <div key={i} className="au-input-row">
          <input className="field-input" aria-label={`Case ${i + 1}`} value={c} onChange={(e) => onChange(list.map((x, j) => (j === i ? e.target.value : x)))} />
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange(list.filter((_, j) => j !== i))}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange([...list, `case${list.length + 1}`])}>
        Add case
      </button>
    </div>
  );
}

/** ConditionBuilder edits all/any rules and can switch to the expression they make. */
export function ConditionBuilder({
  config,
  onChange,
  refs,
}: {
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  refs: DataRef[];
}) {
  const rules = (Array.isArray(config.rules) ? config.rules : []) as Rule[];
  const match: "all" | "any" = config.match === "any" ? "any" : "all";
  const asExpression = rules.length === 0 && typeof config.expression === "string" && config.expression.trim() !== "";
  const [mode, setMode] = useState<"rules" | "expression">(asExpression ? "expression" : "rules");
  const [note, setNote] = useState("");
  const uid = useId();
  function setRules(next: Rule[], m = match) {
    onChange({ ...config, rules: next, match: m, expression: next.length ? rulesToExpression(next, m) : config.expression });
  }
  if (mode === "expression") {
    return (
      <div className="au-rules">
        <label className="field-label" htmlFor={`${uid}-expr`}>
          Expression
        </label>
        <TextInput id={`${uid}-expr`} value={config.expression} refs={refs} onChange={(v) => onChange({ ...config, rules: [], expression: v })} />
        <p className="field-hint">Such as trigger.percent &gt;= 85 and not isNull(vars.owner). Functions: contains, startsWith, isNull, length.</p>
        <div className="btn-row is-flush">
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => {
              const parsed = expressionToRules(String(config.expression ?? ""));
              if (!parsed && String(config.expression ?? "").trim()) {
                setNote("This expression is too complex for rules; keep editing it here.");
                return;
              }
              setNote("");
              setMode("rules");
              if (parsed) {
                setRules(parsed.rules, parsed.match);
              }
            }}
          >
            Edit as rules
          </button>
        </div>
        {note ? <p className="field-hint">{note}</p> : null}
      </div>
    );
  }
  return (
    <div className="au-rules">
      <div className="au-input-row">
        <label className="field-label" htmlFor={`${uid}-match`}>
          Match
        </label>
        <select id={`${uid}-match`} className="field-input" value={match} onChange={(e) => setRules(rules, e.target.value as "all" | "any")}>
          <option value="all">All of these rules</option>
          <option value="any">Any of these rules</option>
        </select>
      </div>
      {rules.map((r, i) => {
        const unary = RULE_OPS.find((o) => o.value === r.op)?.unary;
        return (
          <div key={i} className="au-rule">
            <TextInput id={`${uid}-l${i}`} value={r.left} refs={refs} onChange={(v) => setRules(rules.map((x, j) => (j === i ? { ...x, left: v } : x)))} />
            <select
              className="field-input"
              aria-label={`Rule ${i + 1} operator`}
              value={r.op}
              onChange={(e) => setRules(rules.map((x, j) => (j === i ? { ...x, op: e.target.value } : x)))}
            >
              {RULE_OPS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            {unary ? (
              <span />
            ) : (
              <TextInput
                id={`${uid}-r${i}`}
                value={r.right}
                refs={refs}
                onChange={(v) => setRules(rules.map((x, j) => (j === i ? { ...x, right: /^-?\d+(\.\d+)?$/.test(v.trim()) ? Number(v) : v } : x)))}
              />
            )}
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setRules(rules.filter((_, j) => j !== i))}>
              Remove
            </button>
          </div>
        );
      })}
      <div className="btn-row is-flush">
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => setRules([...rules, { left: "", op: "==", right: "" }])}>
          Add rule
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => setMode("expression")}>
          Edit as expression
        </button>
      </div>
      {rules.length ? <p className="field-hint">Reads as: {rulesToExpression(rules, match)}</p> : null}
    </div>
  );
}

/** FieldsForm builds a step's form from its catalogue fields. */
export function FieldsForm({
  fields,
  config,
  onChange,
  refs,
  problems,
  idPrefix,
}: {
  fields: CatalogueField[];
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  refs: DataRef[];
  problems?: Problems;
  idPrefix: string;
}) {
  const set = (key: string, v: unknown) => {
    const next = { ...config };
    if (v === undefined || v === "") {
      delete next[key];
    } else {
      next[key] = v;
    }
    onChange(next);
  };
  if (fields.some((f) => f.type === "rules")) {
    return (
      <div className="au-fields">
        <div className="is-wide">
          <ConditionBuilder config={config} onChange={onChange} refs={refs} />
          {problemFor(problems, "expression") ? <span className="au-field-error">{problemFor(problems, "expression")}</span> : null}
        </div>
      </div>
    );
  }
  return (
    <div className="au-fields">
      {fields.map((f) => {
        const id = `${idPrefix}-${f.key}`;
        const value = config[f.key] ?? (f.type === "select" || f.type === "boolean" ? f.default : undefined);
        const wide = ["textarea", "json", "headers", "cases", "rules"].includes(f.type) || f.target === "channel";
        let control;
        switch (f.type) {
          case "boolean":
            control = (
              <label className="check-row">
                <input id={id} type="checkbox" checked={value === true} onChange={(e) => set(f.key, e.target.checked)} /> {f.label}
              </label>
            );
            break;
          case "select":
            control = (
              <select id={id} className="field-input" value={String(value ?? "")} onChange={(e) => set(f.key, e.target.value)}>
                {(f.options ?? []).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            );
            break;
          case "number":
            control = (
              <TextInput
                id={id}
                value={value}
                refs={refs}
                placeholder={f.default != null ? String(f.default) : undefined}
                onChange={(v) => set(f.key, /^-?\d+(\.\d+)?$/.test(v.trim()) ? Number(v) : v)}
              />
            );
            break;
          case "json":
            control = <JsonInput id={id} value={value} onChange={(v) => set(f.key, v)} />;
            break;
          case "headers":
            control = <HeadersInput id={id} value={value} refs={refs} onChange={(v) => set(f.key, v)} />;
            break;
          case "cases":
            control = <CasesInput id={id} value={value} onChange={(v) => set(f.key, v)} />;
            break;
          case "target":
          case "connection":
            control = <TargetInput id={id} field={f.type === "connection" ? { ...f, target: "connection" } : f} value={value} refs={refs} onChange={(v) => set(f.key, v)} />;
            break;
          default:
            control = (
              <TextInput
                id={id}
                value={value ?? (f.default != null && f.required ? "" : undefined)}
                refs={refs}
                multiline={f.type === "textarea"}
                placeholder={f.placeholder ?? (f.default != null ? String(f.default) : undefined)}
                onChange={(v) => set(f.key, v)}
              />
            );
        }
        const problem = problemFor(problems, f.key);
        return (
          <div key={f.key} className={`field${wide ? " is-wide" : ""}`}>
            {f.type !== "boolean" ? (
              <label className="field-label" htmlFor={id}>
                {f.label}
                {f.required ? " *" : ""}
              </label>
            ) : null}
            {control}
            {f.help ? <p className="field-hint">{f.help}</p> : null}
            {problem ? <span className="au-field-error">{problem}</span> : null}
          </div>
        );
      })}
    </div>
  );
}
