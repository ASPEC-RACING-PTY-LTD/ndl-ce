import type { ReactNode } from "react";

export function SelectionCard({
  title,
  description,
  selected,
  disabled,
  onSelect,
  children,
  role,
}: {
  title: string;
  description?: ReactNode;
  selected?: boolean;
  disabled?: boolean;
  onSelect: () => void;
  children?: ReactNode;
  role?: "radio";
}) {
  const exclusive = role === "radio";
  return (
    <button
      type="button"
      role={exclusive ? "radio" : undefined}
      className={"selection-card" + (selected ? " is-selected" : "") + (disabled ? " is-disabled" : "")}
      aria-label={title}
      aria-pressed={exclusive ? undefined : selected}
      aria-checked={exclusive ? Boolean(selected) : undefined}
      disabled={disabled}
      onClick={onSelect}
    >
      <span className="title">{title}</span>
      {description ? <span className="desc">{description}</span> : null}
      {children}
    </button>
  );
}
