import type { InputHTMLAttributes, ReactNode } from "react";

export function Checkbox({
  id,
  label,
  hint,
  ...input
}: { id: string; label: ReactNode; hint?: ReactNode } & Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "type">) {
  return (
    <div className="stack">
      <label className="check" htmlFor={id}>
        <input id={id} type="checkbox" {...input} />
        <span>{label}</span>
      </label>
      {hint ? <p className="field-hint">{hint}</p> : null}
    </div>
  );
}
