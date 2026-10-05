import { createContext, useContext, type ReactNode } from "react";
import { Icon, type IconName } from "./Icon";

/** Inside a section layout (IAM), page headers render as section headers. */
const SectionScope = createContext(false);

export function SectionScopeProvider({ children }: { children: ReactNode }) {
  return <SectionScope.Provider value>{children}</SectionScope.Provider>;
}

export function PageHeader({
  title,
  kicker,
  actions,
  id,
  icon,
}: {
  title: string;
  kicker?: ReactNode;
  actions?: ReactNode;
  id: string;
  icon?: IconName;
}) {
  const nested = useContext(SectionScope);
  const Heading = nested ? "h2" : "h1";
  return (
    <header className={"page-header" + (nested ? " is-section" : "")}>
      <div className="page-header-row">
        <div className="page-title-group">
          {icon && !nested ? (
            <span className="page-icon" aria-hidden="true">
              <Icon name={icon} size={18} />
            </span>
          ) : null}
          <div className="page-title-text">
            <Heading id={id} className="page-title">
              {title}
            </Heading>
            {kicker ? <p className="page-kicker">{kicker}</p> : null}
          </div>
        </div>
        {actions ? <div className="page-actions">{actions}</div> : null}
      </div>
    </header>
  );
}
