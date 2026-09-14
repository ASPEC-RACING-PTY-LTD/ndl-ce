import type { InputHTMLAttributes, ReactNode } from "react";

type FieldProps = {
  id: string;
  label: string;
  hint?: ReactNode;
  error?: string;
  prefix?: ReactNode;
  suffix?: ReactNode;
} & Omit<InputHTMLAttributes<HTMLInputElement>, "id">;

export function Field({ id, label, hint, error, className, prefix, suffix, ...input }: FieldProps) {
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  const wrapped = Boolean(prefix || suffix);

  return (
    <div className={className ? `field ${className}` : "field"}>
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      {wrapped ? (
        <div className="field-control">
          {prefix ? <span className="field-affix">{prefix}</span> : null}
          <input
            id={id}
            className="field-input"
            aria-invalid={error ? true : undefined}
            aria-describedby={describedBy}
            {...input}
          />
          {suffix ? <span className="field-affix field-unit">{suffix}</span> : null}
        </div>
      ) : (
        <input
          id={id}
          className="field-input"
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          {...input}
        />
      )}
      {hint ? (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errorId} className="field-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
