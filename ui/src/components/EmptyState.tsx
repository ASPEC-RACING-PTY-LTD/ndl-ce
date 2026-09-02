import type { ReactNode } from "react";

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty-panel">
      <p className="empty-title">{title}</p>
      {children ? <p className="lede">{children}</p> : null}
    </div>
  );
}

export function LoadingState({ label = "Loading" }: { label?: string }) {
  return (
    <p className="loading-state" role="status" aria-busy="true">
      <span className="loading-dot" />
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
