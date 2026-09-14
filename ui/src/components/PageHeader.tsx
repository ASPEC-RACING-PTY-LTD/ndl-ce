import type { ReactNode } from "react";

export function PageHeader({
  title,
  kicker,
  actions,
  id,
}: {
  title: string;
  kicker?: ReactNode;
  actions?: ReactNode;
  id: string;
}) {
  return (
    <header className="page-header">
      <div className="page-header-row">
        <h1 id={id}>{title}</h1>
        {actions}
      </div>
      {kicker ? <p className="page-kicker">{kicker}</p> : null}
    </header>
  );
}
