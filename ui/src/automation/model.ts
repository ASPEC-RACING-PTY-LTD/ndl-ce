/**
 * The guided builder's model. A flow definition is a graph (trigger, nodes,
 * edges); the builder shows it as a vertical list of steps where branching
 * steps (condition, switch, approval, for each) hold nested lists. toModel
 * and toDef convert between the two. A graph the builder cannot show (such
 * as parallel branches drawn by hand) is reported as unsupported and is
 * edited as JSON instead.
 */

export type Position = { x: number; y: number };

export type DefTrigger = { id: string; type: string; config?: Record<string, unknown>; position?: Position };

export type RetryPolicy = { mode: "none" | "fixed" | "exponential"; max_attempts: number; delay_ms?: number; max_delay_ms?: number };

export type DefNode = {
  id: string;
  type: string;
  name?: string;
  config?: Record<string, unknown>;
  position?: Position;
  retry?: RetryPolicy;
  on_error?: "" | "stop" | "continue" | "fail_branch";
  timeout_ms?: number;
};

export type DefEdge = { from: string; to: string; port?: string };

export type Definition = {
  schema_version: number;
  trigger: DefTrigger;
  nodes: DefNode[];
  edges: DefEdge[];
  variables?: Record<string, unknown>;
  retry?: RetryPolicy;
  error?: { on_error: string };
};

export type Branch = { port: string; label: string; steps: Step[] };

export type Step = { node: DefNode; branches: Branch[]; errorSteps?: Step[] };

export type Model = {
  trigger: DefTrigger;
  steps: Step[];
  variables?: Record<string, unknown>;
  retry?: RetryPolicy;
  error?: { on_error: string };
  unsupported?: string;
};

export const TRIGGER_ID = "trigger";

export function emptyDefinition(): Definition {
  return { schema_version: 1, trigger: { id: TRIGGER_ID, type: "trigger.manual", config: {} }, nodes: [], edges: [] };
}

/** branchPorts lists the fixed branches a node type shows in the builder. */
export function branchPorts(node: DefNode): { port: string; label: string }[] {
  switch (node.type) {
    case "logic.condition":
      return [
        { port: "true", label: "If yes" },
        { port: "false", label: "If no" },
      ];
    case "logic.approval":
      return [
        { port: "approved", label: "If approved" },
        { port: "rejected", label: "If rejected" },
      ];
    case "logic.foreach":
      return [{ port: "each", label: "For each item" }];
    case "logic.switch": {
      const cases = Array.isArray(node.config?.cases) ? (node.config?.cases as unknown[]) : [];
      const out = cases
        .map((c) => {
          if (typeof c === "string") {
            return { port: c, label: `Case ${c}` };
          }
          const obj = (c ?? {}) as { value?: unknown; port?: unknown };
          const port = String(obj.port ?? obj.value ?? "");
          return { port, label: `Case ${String(obj.value ?? port)}` };
        })
        .filter((c) => c.port !== "");
      return [...out, { port: "default", label: "Otherwise" }];
    }
  }
  return [];
}

function nodeMap(def: Definition): Map<string, DefNode> {
  return new Map(def.nodes.map((n) => [n.id, n]));
}

function reachable(def: Definition, from: string): Set<string> {
  const seen = new Set<string>();
  const stack = [from];
  while (stack.length) {
    const id = stack.pop() as string;
    if (seen.has(id)) {
      continue;
    }
    seen.add(id);
    for (const e of def.edges) {
      if (e.from === id) {
        stack.push(e.to);
      }
    }
  }
  return seen;
}

function topoOrder(def: Definition): Map<string, number> {
  const order = new Map<string, number>();
  const indeg = new Map<string, number>();
  for (const n of def.nodes) {
    indeg.set(n.id, 0);
  }
  for (const e of def.edges) {
    if (indeg.has(e.to)) {
      indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
    }
  }
  const queue = def.edges.filter((e) => e.from === TRIGGER_ID).map((e) => e.to);
  for (const e of def.edges.filter((x) => x.from === TRIGGER_ID)) {
    indeg.set(e.to, (indeg.get(e.to) ?? 1) - 1);
  }
  let i = 0;
  const visit = [...new Set(queue)].filter((id) => (indeg.get(id) ?? 0) <= 0);
  while (visit.length) {
    const id = visit.shift() as string;
    if (order.has(id)) {
      continue;
    }
    order.set(id, i++);
    for (const e of def.edges) {
      if (e.from === id) {
        const d = (indeg.get(e.to) ?? 1) - 1;
        indeg.set(e.to, d);
        if (d <= 0) {
          visit.push(e.to);
        }
      }
    }
  }
  for (const n of def.nodes) {
    if (!order.has(n.id)) {
      order.set(n.id, i++);
    }
  }
  return order;
}

/** toModel turns a definition into the builder's nested steps. */
export function toModel(def: Definition): Model {
  const nodes = nodeMap(def);
  const order = topoOrder(def);
  const used = new Set<string>();
  let unsupported = "";

  function outEdges(id: string, port?: string): DefEdge[] {
    return def.edges.filter((e) => e.from === id && (port === undefined || (e.port ?? "") === port));
  }

  function join(targets: (string | null)[]): string | null {
    const present = targets.filter((t): t is string => t != null);
    if (present.length < 2) {
      return null;
    }
    let common = reachable(def, present[0]);
    for (const t of present.slice(1)) {
      const r = reachable(def, t);
      common = new Set([...common].filter((x) => r.has(x)));
    }
    if (common.size === 0) {
      return null;
    }
    return [...common].sort((a, b) => (order.get(a) ?? 0) - (order.get(b) ?? 0))[0];
  }

  function seq(start: string | null, stop: Set<string>): Step[] {
    const out: Step[] = [];
    let cur = start;
    while (cur != null && !stop.has(cur)) {
      const node = nodes.get(cur);
      if (!node) {
        unsupported ||= `an edge points at a missing step ${cur}`;
        break;
      }
      if (used.has(cur)) {
        unsupported ||= `step ${cur} is reached in more than one place`;
        break;
      }
      used.add(cur);
      const ports = branchPorts(node);
      const errEdge = outEdges(cur, "error")[0];
      let next: string | null = null;
      const step: Step = { node, branches: [] };
      if (node.type === "logic.foreach") {
        const each = outEdges(cur, "each")[0]?.to ?? null;
        const done = outEdges(cur, "done")[0]?.to ?? null;
        step.branches.push({ port: "each", label: "For each item", steps: seq(each, new Set([...stop, ...(done ? [done] : [])])) });
        next = done;
      } else if (ports.length > 0) {
        const targets = ports.map((p) => outEdges(cur as string, p.port)[0]?.to ?? null);
        const j = join([...targets, ...(errEdge ? [] : [])]);
        const inner = new Set([...stop, ...(j ? [j] : [])]);
        ports.forEach((p, i) => {
          const t = targets[i];
          step.branches.push({ port: p.port, label: p.label, steps: t != null && t !== j ? seq(t, inner) : [] });
        });
        next = j;
        const plain = outEdges(cur, "");
        if (plain.length > 0) {
          unsupported ||= `${cur} has an edge without a branch`;
        }
      } else {
        const plain = outEdges(cur, "");
        if (plain.length > 1) {
          unsupported ||= `${cur} starts parallel branches`;
        }
        next = plain[0]?.to ?? null;
      }
      if (errEdge) {
        step.errorSteps = seq(errEdge.to, new Set([...stop, ...(next ? [next] : [])]));
      }
      out.push(step);
      cur = next;
    }
    return out;
  }

  const first = def.edges.filter((e) => e.from === TRIGGER_ID);
  if (first.length > 1) {
    unsupported = "the trigger starts parallel branches";
  }
  const steps = seq(first[0]?.to ?? null, new Set());
  for (const n of def.nodes) {
    if (!used.has(n.id)) {
      unsupported ||= `step ${n.id} is not connected the way the builder shows steps`;
    }
  }
  const model: Model = { trigger: def.trigger, steps, variables: def.variables, retry: def.retry, error: def.error };
  if (unsupported) {
    model.unsupported = unsupported;
  }
  return model;
}

type Tail = { id: string; port: string };

/** toDef turns builder steps back into a definition. */
export function toDef(model: Model): Definition {
  const nodes: DefNode[] = [];
  const edges: DefEdge[] = [];

  function link(tails: Tail[], to: string) {
    for (const t of tails) {
      edges.push(t.port ? { from: t.id, to, port: t.port } : { from: t.id, to });
    }
  }

  function emit(steps: Step[], entry: Tail[]): Tail[] {
    let tails = entry;
    for (const step of steps) {
      nodes.push(step.node);
      link(tails, step.node.id);
      const id = step.node.id;
      if (step.node.type === "logic.foreach") {
        const body = step.branches.find((b) => b.port === "each")?.steps ?? [];
        emit(body, [{ id, port: "each" }]);
        tails = [{ id, port: "done" }];
      } else if (step.branches.length > 0) {
        const next: Tail[] = [];
        for (const b of step.branches) {
          if (b.steps.length === 0) {
            next.push({ id, port: b.port });
          } else {
            next.push(...emit(b.steps, [{ id, port: b.port }]));
          }
        }
        tails = next;
      } else {
        tails = [{ id, port: "" }];
      }
      if (step.errorSteps && step.errorSteps.length > 0) {
        emit(step.errorSteps, [{ id, port: "error" }]);
      }
    }
    return tails;
  }

  emit(model.steps, [{ id: TRIGGER_ID, port: "" }]);
  const def: Definition = { schema_version: 1, trigger: model.trigger, nodes, edges };
  if (model.variables && Object.keys(model.variables).length) {
    def.variables = model.variables;
  }
  if (model.retry) {
    def.retry = model.retry;
  }
  if (model.error) {
    def.error = model.error;
  }
  return def;
}

// --- editing helpers ---

/** A path names a step list: [] is the top level; [i, "true"] the yes branch of step i. */
export type ListPath = (number | string)[];

export function listAt(steps: Step[], path: ListPath): Step[] {
  let cur = steps;
  for (let i = 0; i < path.length; i += 2) {
    const step = cur[path[i] as number];
    const port = path[i + 1] as string;
    if (!step) {
      return [];
    }
    cur = port === "error" ? (step.errorSteps ??= []) : (step.branches.find((b) => b.port === port)?.steps ?? []);
  }
  return cur;
}

function cloneSteps(steps: Step[]): Step[] {
  return steps.map((s) => ({
    node: { ...s.node, config: s.node.config ? { ...s.node.config } : undefined },
    branches: s.branches.map((b) => ({ ...b, steps: cloneSteps(b.steps) })),
    errorSteps: s.errorSteps ? cloneSteps(s.errorSteps) : undefined,
  }));
}

export function insertStep(steps: Step[], path: ListPath, index: number, step: Step): Step[] {
  const next = cloneSteps(steps);
  listAt(next, path).splice(index, 0, step);
  return next;
}

export function removeStep(steps: Step[], path: ListPath, index: number): Step[] {
  const next = cloneSteps(steps);
  listAt(next, path).splice(index, 1);
  return next;
}

export function moveStep(steps: Step[], path: ListPath, index: number, delta: -1 | 1): Step[] {
  const next = cloneSteps(steps);
  const list = listAt(next, path);
  const to = index + delta;
  if (to < 0 || to >= list.length) {
    return next;
  }
  [list[index], list[to]] = [list[to], list[index]];
  return next;
}

export function updateStep(steps: Step[], path: ListPath, index: number, node: DefNode): Step[] {
  const next = cloneSteps(steps);
  const list = listAt(next, path);
  const step = list[index];
  if (!step) {
    return next;
  }
  step.node = node;
  step.branches = syncBranches(node, step.branches);
  if (node.on_error !== "fail_branch") {
    delete step.errorSteps;
  } else {
    step.errorSteps ??= [];
  }
  return next;
}

/** syncBranches keeps a step's branch lists in line with its type and cases. */
export function syncBranches(node: DefNode, current: Branch[]): Branch[] {
  return branchPorts(node).map((p) => ({ ...p, steps: current.find((b) => b.port === p.port)?.steps ?? [] }));
}

export function allSteps(steps: Step[]): Step[] {
  const out: Step[] = [];
  for (const s of steps) {
    out.push(s);
    for (const b of s.branches) {
      out.push(...allSteps(b.steps));
    }
    if (s.errorSteps) {
      out.push(...allSteps(s.errorSteps));
    }
  }
  return out;
}

/** newStepId picks a readable id that is not taken, such as restart_2. */
export function newStepId(steps: Step[], type: string): string {
  const base = (type.split(".").pop() ?? "step").replace(/[^A-Za-z0-9]/g, "_").replace(/^[^A-Za-z]+/, "") || "step";
  const taken = new Set(allSteps(steps).map((s) => s.node.id));
  if (!taken.has(base)) {
    return base;
  }
  for (let i = 2; ; i++) {
    if (!taken.has(`${base}_${i}`)) {
      return `${base}_${i}`;
    }
  }
}

/** stepsBefore lists the steps whose output a step at path/index can read. */
export function stepsBefore(steps: Step[], path: ListPath, index: number): { step: Step; inLoop: boolean }[] {
  const out: { step: Step; inLoop: boolean }[] = [];
  let cur = steps;
  let inLoop = false;
  for (let i = 0; i < path.length; i += 2) {
    const at = path[i] as number;
    for (let j = 0; j < at; j++) {
      out.push(...allSteps([cur[j]]).map((s) => ({ step: s, inLoop: false })));
    }
    const owner = cur[at];
    if (!owner) {
      break;
    }
    out.push({ step: owner, inLoop: owner.node.type === "logic.foreach" });
    inLoop ||= owner.node.type === "logic.foreach";
    const port = path[i + 1] as string;
    cur = port === "error" ? (owner.errorSteps ?? []) : (owner.branches.find((b) => b.port === port)?.steps ?? []);
  }
  for (let j = 0; j < index && j < cur.length; j++) {
    out.push(...allSteps([cur[j]]).map((s) => ({ step: s, inLoop: false })));
  }
  if (inLoop) {
    out.forEach((o) => {
      if (o.step.node.type === "logic.foreach") {
        o.inLoop = true;
      }
    });
  }
  return out;
}

// --- condition rules and expressions ---

export type Rule = { left: unknown; op: string; right?: unknown };

export const RULE_OPS: { value: string; label: string; unary?: boolean }[] = [
  { value: "==", label: "is equal to" },
  { value: "!=", label: "is not equal to" },
  { value: ">", label: "is greater than" },
  { value: ">=", label: "is at least" },
  { value: "<", label: "is less than" },
  { value: "<=", label: "is at most" },
  { value: "contains", label: "contains" },
  { value: "not_contains", label: "does not contain" },
  { value: "starts_with", label: "starts with" },
  { value: "is_empty", label: "is empty", unary: true },
  { value: "not_empty", label: "is not empty", unary: true },
];

const SINGLE_REF = /^\s*\{\{\s*([^{}]+?)\s*\}\}\s*$/;

function operand(v: unknown): string {
  if (typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  if (v == null) {
    return "null";
  }
  const s = String(v);
  const ref = SINGLE_REF.exec(s);
  if (ref) {
    return ref[1];
  }
  if (/^-?\d+(\.\d+)?$/.test(s.trim())) {
    return s.trim();
  }
  return JSON.stringify(s);
}

/** rulesToExpression writes rules as one expression, joined by and or or. */
export function rulesToExpression(rules: Rule[], match: "all" | "any"): string {
  const parts = rules.map((r) => {
    const l = operand(r.left);
    const rr = operand(r.right);
    switch (r.op) {
      case "contains":
        return `contains(${l}, ${rr})`;
      case "not_contains":
        return `not contains(${l}, ${rr})`;
      case "starts_with":
        return `startsWith(${l}, ${rr})`;
      case "is_empty":
        return `length(${l}) == 0`;
      case "not_empty":
        return `length(${l}) > 0`;
    }
    return `${l} ${r.op} ${rr}`;
  });
  return parts.join(match === "any" ? " or " : " and ");
}

function fromOperand(s: string): unknown {
  const t = s.trim();
  if (/^-?\d+(\.\d+)?$/.test(t)) {
    return Number(t);
  }
  if (t === "true" || t === "false") {
    return t === "true";
  }
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) {
    return t.slice(1, -1);
  }
  return `{{ ${t} }}`;
}

/** expressionToRules reads a simple expression back into rules, or null. */
export function expressionToRules(src: string): { rules: Rule[]; match: "all" | "any" } | null {
  const text = src.trim();
  if (!text || /[()]/.test(text.replace(/\b(contains|startsWith|length)\([^()]*\)/g, ""))) {
    return null;
  }
  const hasAnd = / and /.test(text);
  const hasOr = / or /.test(text);
  if (hasAnd && hasOr) {
    return null;
  }
  const match = hasOr ? "any" : "all";
  const rules: Rule[] = [];
  for (const part of text.split(hasOr ? / or / : / and /)) {
    const p = part.trim();
    let m = /^(not )?contains\((.+), (.+)\)$/.exec(p);
    if (m) {
      rules.push({ left: fromOperand(m[2]), op: m[1] ? "not_contains" : "contains", right: fromOperand(m[3]) });
      continue;
    }
    m = /^startsWith\((.+), (.+)\)$/.exec(p);
    if (m) {
      rules.push({ left: fromOperand(m[1]), op: "starts_with", right: fromOperand(m[2]) });
      continue;
    }
    m = /^length\((.+)\) (==|>) 0$/.exec(p);
    if (m) {
      rules.push({ left: fromOperand(m[1]), op: m[2] === "==" ? "is_empty" : "not_empty" });
      continue;
    }
    m = /^(.+?) (==|!=|>=|<=|>|<) (.+)$/.exec(p);
    if (m) {
      rules.push({ left: fromOperand(m[1]), op: m[2], right: fromOperand(m[3]) });
      continue;
    }
    return null;
  }
  return { rules, match };
}

// --- WHEN conditions ---

export type When = { join?: "and" | "or"; kind?: string; field?: string; op?: string; value?: unknown; group?: When[] };

export function whenDepth(list: When[]): number {
  let d = 1;
  for (const c of list) {
    if (c.group && c.group.length) {
      d = Math.max(d, 1 + whenDepth(c.group));
    }
  }
  return d;
}

/** whenText reads WHEN conditions as a sentence, with and binding tighter. */
export function whenText(list: When[]): string {
  return list
    .map((c, i) => {
      const join = i === 0 ? "" : `${c.join === "or" ? "or" : "and"} `;
      if (c.group && c.group.length) {
        return `${join}(${whenText(c.group)})`;
      }
      if (c.kind && !c.field) {
        return `${join}event is ${c.kind}`;
      }
      if (c.op === "exists") {
        return `${join}${c.field} exists`;
      }
      return `${join}${c.field} ${c.op === "neq" ? "is not" : c.op === "contains" ? "contains" : "is"} ${JSON.stringify(c.value ?? "")}`;
    })
    .join(" ");
}

// --- dynamic content ---

export type DataRef = { ref: string; label: string; group: string };

const OUTPUT_KEYS: Record<string, string[]> = {
  "action.http": ["status", "body", "headers"],
  "action.webhook": ["status", "body"],
  "logic.condition": ["result"],
  "logic.switch": ["value", "case"],
  "logic.compose": ["value"],
  "logic.set_variable": ["value"],
  "logic.approval": ["approved", "by", "note"],
  "logic.foreach": ["count", "results"],
  "ndl.workloads.list": ["items", "count"],
  "ndl.docker.containers.list": ["items", "count"],
  "ndl.uptime.recover": ["status", "reason", "all_up"],
  "ndl.storage.pressure": ["status", "reason", "operation_ids", "job_id"],
};

/** dataRefs lists what dynamic content a step can insert. */
export function dataRefs(
  trigger: DefTrigger,
  before: { step: Step; inLoop: boolean }[],
  eventFields: Record<string, string[]>,
): DataRef[] {
  const out: DataRef[] = [];
  const tg = "Trigger";
  switch (trigger.type) {
    case "trigger.event": {
      const kind = String(trigger.config?.kind ?? "");
      for (const f of eventFields[kind] ?? []) {
        out.push({ ref: `trigger.${f}`, label: f, group: tg });
      }
      out.push({ ref: "trigger.event.kind", label: "event kind", group: tg }, { ref: "trigger.event.at", label: "event time", group: tg });
      break;
    }
    case "trigger.webhook":
      out.push({ ref: "trigger", label: "whole body", group: tg }, { ref: "trigger.headers", label: "headers", group: tg });
      break;
    case "trigger.schedule":
      out.push({ ref: "trigger.scheduled_at", label: "scheduled time", group: tg });
      break;
    default:
      out.push({ ref: "inputs", label: "run inputs", group: tg });
  }
  for (const { step, inLoop } of before) {
    const n = step.node;
    const group = n.name ? `${n.name} (${n.id})` : n.id;
    if (inLoop) {
      out.push({ ref: "item", label: "current item", group }, { ref: "index", label: "item position", group });
    }
    for (const k of OUTPUT_KEYS[n.type] ?? ["status", "job_id", "result"]) {
      out.push({ ref: `nodes.${n.id}.${k}`, label: k, group });
    }
    if (n.type === "logic.set_variable" && typeof n.config?.name === "string" && n.config.name) {
      out.push({ ref: `vars.${n.config.name}`, label: `variable ${n.config.name}`, group: "Variables" });
    }
  }
  out.push({ ref: "run.id", label: "run id", group: "Run" }, { ref: "run.started_at", label: "started at", group: "Run" });
  return out;
}

/** problemsFor picks the validation problems of one step by its index in the definition. */
export function problemsByStep(problems: { path: string; message: string }[], def: Definition): Map<string, { path: string; message: string }[]> {
  const out = new Map<string, { path: string; message: string }[]>();
  for (const p of problems) {
    const m = /^nodes\[(\d+)\](.*)$/.exec(p.path);
    const key = m ? (def.nodes[Number(m[1])]?.id ?? "") : p.path.startsWith("trigger") ? TRIGGER_ID : "";
    const list = out.get(key) ?? [];
    list.push(m ? { path: m[2].replace(/^\./, ""), message: p.message } : p);
    out.set(key, list);
  }
  return out;
}
