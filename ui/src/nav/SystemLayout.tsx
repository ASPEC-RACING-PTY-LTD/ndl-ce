import type { ReactNode } from "react";
import { PageNav } from "../components/PageNav";
import { useSession } from "../session";
import { useNavDisclosure } from "./NavDisclosure";
import { SYSTEM_PAGES, visibleSections } from "./sections";

/**
 * SystemLayout gives every page under System (Add Features, Updates,
 * Appearance, Search, Certificates, License, Store, Docs and the Audit log)
 * the same sub-bar, so moving between them never needs the browser's back.
 */
export function SystemLayout({ children }: { children: ReactNode }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { capabilityOn } = useNavDisclosure();
  const [system] = visibleSections([{ id: "system", label: "System", href: "/settings/features", icon: "settings", children: SYSTEM_PAGES }], user, capabilityOn);
  const items = (system?.children ?? []).map((c) => ({ id: c.id, label: c.label, href: c.href, match: c.match }));
  return (
    <>
      {items.length > 1 ? <PageNav id="system" label="System" items={items} className="is-sub system-subnav" /> : null}
      {children}
    </>
  );
}
