import type { ReactNode } from "react";
import { ErrorNotice } from "./ErrorNotice";
import { Icon, type IconName } from "./Icon";

export function EmptyState({
  title,
  children,
  action,
  icon,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  icon?: IconName;
}) {
  return (
    <div className="empty-panel">
      {icon ? (
        <span className="empty-icon" aria-hidden="true">
          <Icon name={icon} size={18} />
        </span>
      ) : null}
      <p className="empty-title">{title}</p>
      {children ? <p className="lede">{children}</p> : null}
      {action}
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
  if (typeof children === "string") {
    return <ErrorNotice error={children} />;
  }
  return (
    <ErrorNotice error={children} />
  );
}
