import type { ReactNode, SelectHTMLAttributes } from "react";

export function SelectField({
  id,
  label,
  hint,
  children,
  ...select
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  children: ReactNode;
} & Omit<SelectHTMLAttributes<HTMLSelectElement>, "id">) {
  const hintId = hint ? `${id}-hint` : undefined;
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <select id={id} className="field-input" aria-describedby={hintId} {...select}>
        {children}
      </select>
      {hint ? (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      ) : null}
    </div>
  );
}
