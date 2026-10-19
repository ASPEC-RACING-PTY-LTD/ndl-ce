type RuleResult = { rule: string; left?: unknown; op?: string; right?: unknown; matched: boolean };

export type ReportLine = {
  node?: string;
  scope?: string;
  kind: string;
  text: string;
  matched?: boolean;
  rules?: RuleResult[];
};

export type Report = { ok: boolean; lines: ReportLine[] };

function show(v: unknown): string {
  if (v === undefined) {
    return "";
  }
  if (typeof v === "string") {
    return JSON.stringify(v);
  }
  return JSON.stringify(v);
}

const KIND_LABELS: Record<string, string> = {
  when: "Trigger conditions",
  trigger: "Trigger",
  condition: "Condition",
  branch: "Branch",
  would: "Would do",
  wait: "Wait",
  loop: "Loop",
  limit: "Limit reached",
  problem: "Problem",
  stop: "Stop",
};

function lineClass(l: ReportLine): string {
  if (l.kind === "would") {
    return "is-would";
  }
  if (l.kind === "problem" || l.kind === "limit") {
    return "is-problem";
  }
  if (l.matched === true) {
    return "is-true";
  }
  if (l.matched === false) {
    return "is-false";
  }
  return "";
}

/** ReportView shows a test run: matched conditions with their values and "would" lines. */
export function ReportView({ report }: { report: Report }) {
  const problems = report.lines.filter((l) => l.kind === "problem" || l.kind === "limit").length;
  return (
    <div className="stack">
      <p className={`banner ${report.ok ? "banner-ok" : "banner-error"}`} role="status">
        {report.ok
          ? "The test passed. Nothing was changed: action steps only said what they would do."
          : `The test found ${problems || "some"} problem${problems === 1 ? "" : "s"}. Nothing was changed.`}
      </p>
      <ul className="au-report" aria-label="Test results">
        {report.lines.map((l, i) => (
          <li key={i} className={lineClass(l)}>
            <span>
              <strong>{KIND_LABELS[l.kind] ?? l.kind}</strong>
              {l.node ? <span className="meta"> · {l.node}</span> : null}
              {l.scope ? <span className="meta"> · {l.scope}</span> : null}
            </span>
            <span>{l.text}</span>
            {(l.rules ?? []).map((r, j) => (
              <span key={j} className="au-rule-result">
                {r.matched ? "true " : "false"} {r.rule}
                {r.left !== undefined || r.right !== undefined ? `: ${show(r.left)} ${r.op ?? ""} ${show(r.right)}` : ""}
              </span>
            ))}
          </li>
        ))}
      </ul>
    </div>
  );
}
