import type { ReactNode } from "react";

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="panel empty-panel">
      <p className="empty-title">{title}</p>
      {children ? <p>{children}</p> : null}
    </div>
  );
}

export function LoadingState({ label = "Loading" }: { label?: string }) {
  return (
    <p role="status" aria-busy="true">
      {label}
    </p>
  );
}

export function ErrorState({ children }: { children: ReactNode }) {
  return (
    <p className="banner banner-error" role="alert">
      {children}
    </p>
  );
}
