import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { useInstanceLayout, useNavLayout } from "../appearance/state";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageNav, usePageNavOrder } from "../components/PageNav";
import { hasGrant } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";
import { useNavDisclosure } from "./NavDisclosure";
import { GameServersBarLinks } from "./GameServersBarLinks";
import { gameServerIdFromPath, isGameServersHomePath } from "./gameServers";
import { ADMIN_SECTIONS, CREATE_ENTRIES, INSTANCE_SECTIONS, isAdminPath, isInstancePath, toPageNav, visibleSections, type CreateEntry, type Section } from "./sections";

function useSections(sections: Section[]): Section[] {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { capabilityOn } = useNavDisclosure();
  return visibleSections(sections, user, capabilityOn);
}

/**
 * createEntries lists the "Create +" choices: instances in the order of the
 * person's Instance bar, then templates.
 */
export function createEntries(order: string[], capabilityOn: (id: string) => boolean): CreateEntry[] {
  const rank = new Map(order.map((id, i) => [id, i]));
  const at = (e: CreateEntry) => (e.group ? 1000 : 0) + (rank.get(e.section) ?? 99);
  return CREATE_ENTRIES.filter((e) => !e.capability || capabilityOn(e.capability)).sort((a, b) => at(a) - at(b));
}

/** CreateMenu is the "Create +" button: every instance type and template, in the bar's order. */
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
        Create <Icon name="create" size={14} />
      </button>
      {open ? (
        <div className="menu-panel create-menu-panel" role="menu" aria-label="Create">
          {entries.map((e, i) => (
            <Fragment key={e.href}>
              {e.group && entries[i - 1]?.group !== e.group ? (
                <span className="create-menu-group" role="presentation">
                  {e.group}
                </span>
              ) : null}
              <Link href={e.href} role="menuitem" onClick={() => setOpen(false)}>
                {e.label}
              </Link>
            </Fragment>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function AdminBar() {
  const sections = useSections(ADMIN_SECTIONS);
  return <PageNav id="admin" label="Administration" items={toPageNav(sections)} className="is-area" />;
}

function InstanceBar({ path }: { path: string }) {
  const sections = useSections(INSTANCE_SECTIONS);
  // On the Game Servers dashboard its filters join this bar instead of a second row.
  const extra = isGameServersHomePath(path) ? <GameServersBarLinks /> : null;
  return <PageNav id="instance" label="Instance" items={toPageNav(sections)} extra={extra} actions={<CreateMenu />} className="is-area" />;
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
  // A game server's own bar (GameServerFrame) takes the Instance bar's place on its pages.
  const instanceBar = instance === "instance" && isInstancePath(path) && !gameServerIdFromPath(path);
  const bar = instanceBar ? <InstanceBar path={path} /> : nav === "modern" && isAdminPath(path) ? <AdminBar /> : null;
  return (
    <>
      {bar}
      {children}
    </>
  );
}
