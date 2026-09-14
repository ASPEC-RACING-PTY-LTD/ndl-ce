import type { ReactNode } from "react";

export function ErrorBanner({
  title,
  secondary,
  detail,
  action,
}: {
  title: string;
  secondary?: string;
  detail?: string;
  action?: ReactNode;
}) {
  return (
    <div className="error-banner" role="alert">
      <p className="error-title">{title}</p>
      {secondary ? <p className="error-secondary">{secondary}</p> : null}
      {action}
      {detail ? (
        <details>
          <summary>Advanced details</summary>
          <pre className="activity-detail-raw">{detail}</pre>
        </details>
      ) : null}
    </div>
  );
}
