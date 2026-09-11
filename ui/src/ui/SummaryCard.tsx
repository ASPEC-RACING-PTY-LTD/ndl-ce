import type { ReactNode } from "react";

export function SummaryCard({
  label,
  value,
  meta,
  tone,
}: {
  label: string;
  value: ReactNode;
  meta?: ReactNode;
  tone?: "warn" | "danger";
}) {
  return (
    <article className={"summary-card" + (tone ? ` is-${tone}` : "")}>
      <span className="label">{label}</span>
      <span className="value">{value}</span>
      {meta ? <span className="meta">{meta}</span> : null}
    </article>
  );
}
