import { useEffect, useRef, useState } from "react";
import type { MeResponse } from "../../api/types";
import { roleLabel } from "../../labels";
import { navigate } from "../../router";
import { useSession } from "../../session";
import { Link } from "../Link";

export function AccountMenu({ user }: { user: MeResponse }) {
  const session = useSession();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(event: MouseEvent) {
      if (root.current && !root.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
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

  async function onLogout() {
    await session.signOut();
    navigate("/login", { replace: true });
  }

  return (
    <div className="menu" ref={root}>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-expanded={open}
        aria-haspopup="true"
        onClick={() => setOpen((v) => !v)}
      >
        {user.username}
      </button>
      {open ? (
        <div className="menu-panel" role="menu">
          <p className="picker-meta">
            {user.roles.length ? user.roles.map(roleLabel).join(", ") : "No roles"}
          </p>
          <Link href="/me" role="menuitem" onClick={() => setOpen(false)}>
            Account
          </Link>
          <button type="button" role="menuitem" onClick={() => void onLogout()}>
            Log out
          </button>
        </div>
      ) : null}
    </div>
  );
}
