import type { ReactNode } from "react";

export function SummaryCard({
  label,
  value,
  meta,
  tone,
  onClick,
}: {
  label: string;
  value: ReactNode;
  meta?: ReactNode;
  tone?: "warn" | "danger";
  onClick?: () => void;
}) {
  const className = "summary-card" + (tone ? ` is-${tone}` : "");
  const body = (
    <>
      <span className="label">{label}</span>
      <span className="value">{value}</span>
      {meta ? <span className="meta">{meta}</span> : null}
    </>
  );
  if (onClick) {
    return (
      <button
        type="button"
        className={className + " is-action"}
        aria-haspopup="dialog"
        onClick={onClick}
      >
        {body}
      </button>
    );
  }
  return <article className={className}>{body}</article>;
}
