export function CapacityBar({
  used,
  total,
  label,
}: {
  used?: number | null;
  total?: number | null;
  label?: string;
}) {
  if (used == null || total == null || total <= 0) {
    return <p className="field-hint">{label || "Capacity not reported"}</p>;
  }
  const ratio = Math.min(1, Math.max(0, used / total));
  const tone = ratio >= 0.9 ? "is-danger" : ratio >= 0.75 ? "is-warn" : "";
  return (
    <div className="capacity-bar">
      {label ? <p className="field-hint">{label}</p> : null}
      <div className="capacity-track" aria-hidden="true">
        <span className={"capacity-fill " + tone} style={{ width: `${Math.round(ratio * 100)}%` }} />
      </div>
    </div>
  );
}
