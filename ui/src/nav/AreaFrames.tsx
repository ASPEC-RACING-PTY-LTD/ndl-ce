import { useEffect, useRef, useState, type ReactNode } from "react";
import { useInstanceLayout, useNavLayout } from "../appearance/state";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageNav, usePageNavOrder } from "../components/PageNav";
import { hasGrant } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";
import { useNavDisclosure } from "./NavDisclosure";
import { ADMIN_SECTIONS, CREATE_ENTRIES, INSTANCE_SECTIONS, isAdminPath, isInstancePath, toPageNav, visibleSections, type Section } from "./sections";

function useSections(sections: Section[]): Section[] {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { capabilityOn } = useNavDisclosure();
  return visibleSections(sections, user, capabilityOn);
}

/** createEntries lists the "+ Create" choices in the order of the person's Instance bar. */
export function createEntries(order: string[], capabilityOn: (id: string) => boolean) {
  const rank = new Map(order.map((id, i) => [id, i]));
  return CREATE_ENTRIES.filter((e) => !e.capability || capabilityOn(e.capability)).sort(
    (a, b) => (rank.get(a.section) ?? 99) - (rank.get(b.section) ?? 99),
  );
}

/** CreateMenu is the "+ Create" button: every instance type, in the bar's order. */
export function CreateMenu() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { capabilityOn } = useNavDisclosure();
  const sections = useSections(INSTANCE_SECTIONS);
  const [ordered] = usePageNavOrder("instance", sections);
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const entries = createEntries(
    ordered.map((s) => s.id),
    capabilityOn,
  );

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(e: MouseEvent) {
      if (root.current && !root.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!hasGrant(user, "compute.create") && !hasGrant(user, "gameserver.create")) {
    return null;
  }
  return (
    <div className="menu create-menu" ref={root}>
      <button className="btn btn-primary btn-sm" type="button" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <Icon name="create" size={14} /> Create
      </button>
      {open ? (
        <div className="menu-panel create-menu-panel" role="menu" aria-label="Create">
          {entries.map((e) => (
            <Link key={e.href} href={e.href} role="menuitem" onClick={() => setOpen(false)}>
              {e.label}
            </Link>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function AdminBar() {
  const sections = useSections(ADMIN_SECTIONS);
  return <PageNav id="admin" label="Administration" icon="shield" items={toPageNav(sections)} />;
}

function InstanceBar() {
  const sections = useSections(INSTANCE_SECTIONS);
  return <PageNav id="instance" label="Instance" icon="workloads" items={toPageNav(sections)} actions={<CreateMenu />} />;
}

/**
 * AreaFrame puts the area's page bar (Administration or Instance) above a
 * page, as the person's layout settings say. The bar is a sibling of the
 * page, so a layout change (or saved settings arriving) never remounts it.
 */
export function AreaFrame({ children }: { children: ReactNode }) {
  const path = usePath();
  const [nav] = useNavLayout();
  const [instance] = useInstanceLayout();
  const bar = instance === "instance" && isInstancePath(path) ? <InstanceBar /> : nav === "modern" && isAdminPath(path) ? <AdminBar /> : null;
  return (
    <>
      {bar}
      {children}
    </>
  );
}

/** AdminHome is /admin: a card for every admin area this person may open. */
export function AdminHome() {
  const sections = useSections(ADMIN_SECTIONS);
  return (
    <section className="page page-wide" aria-labelledby="admin-heading">
      <header className="page-header">
        <h1 id="admin-heading">Administration</h1>
        <p className="page-lede">Everything that runs the appliance. Drag the buttons above to put the ones you use first.</p>
      </header>
      {sections.length === 0 ? <p className="muted">You have no administration pages.</p> : null}
      <div className="fluid-grid admin-cards">
        {sections.map((s) => (
          <Link key={s.id} href={s.href} className="panel admin-card">
            <span className="admin-card-head">
              <Icon name={s.icon} size={18} />
              <strong>{s.label}</strong>
            </span>
            {s.description ? <span className="muted">{s.description}</span> : null}
            {(s.children ?? []).length > 0 ? <span className="meta">{s.children!.map((c) => c.label).join(" · ")}</span> : null}
          </Link>
        ))}
      </div>
    </section>
  );
}
