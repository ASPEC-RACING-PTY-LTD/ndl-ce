import { useState, type ReactNode } from "react";
import { explainError } from "../errors";

/**
 * ErrorNotice shows an error as a plain sentence, with what to do next, and
 * keeps the raw error behind a "Technical details" toggle that expands
 * downwards.
 */
export function ErrorNotice({
  error,
  title,
  action,
  className,
}: {
  error: unknown;
  /** Optional lead, for example "Starting failed". */
  title?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  const { summary, hint, detail } = explainError(error);
  const showDetail = detail !== "" && detail !== summary && `${detail}.` !== summary;
  return (
    <div className={`banner banner-error error-notice${className ? ` ${className}` : ""}`} role="alert">
      <p className="error-notice-main">
        {title ? <strong>{title}: </strong> : null}
        <span>{summary}</span>
        {hint ? <span className="error-notice-hint"> {hint}</span> : null}
        {action ? <span className="error-notice-action"> {action}</span> : null}
      </p>
      {showDetail ? (
        <details className="error-notice-details">
          <summary>Technical details</summary>
          <pre>{detail}</pre>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            onClick={() => {
              void navigator.clipboard?.writeText(detail).then(() => setCopied(true));
            }}
          >
            {copied ? "Copied" : "Copy"}
          </button>
        </details>
      ) : null}
    </div>
  );
}
