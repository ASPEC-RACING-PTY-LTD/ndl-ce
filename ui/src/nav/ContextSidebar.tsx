import type { ReactNode } from "react";
import { preferMainNav } from "./prefs";

export function ContextSidebar({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <>
      <button className="ctx-back" type="button" aria-label="Back to Main Menu" onClick={() => preferMainNav()}>
        <span aria-hidden="true">←</span>
        <span className="ctx-back-label">Back to Main Menu</span>
      </button>
      <p className="nav-group-label ctx-area-title">{title}</p>
      {children}
    </>
  );
}
