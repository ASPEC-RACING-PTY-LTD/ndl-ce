import type { InputHTMLAttributes, ReactNode } from "react";

export function Switch({
  id,
  label,
  ...input
}: { id: string; label: ReactNode } & Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "type">) {
  return (
    <label className="switch" htmlFor={id}>
      <input id={id} type="checkbox" role="switch" {...input} />
      <span>{label}</span>
    </label>
  );
}
