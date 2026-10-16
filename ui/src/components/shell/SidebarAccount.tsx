import { useEffect, useRef, useState } from "react";
import type { MeResponse } from "../../api/types";
import { roleLabel } from "../../labels";
import { usePref } from "../../prefs";
import { navigate } from "../../router";
import { useSession } from "../../session";
import { THEME_KEY, parseThemePrefs, type ThemeMode } from "../../theme";
import { Icon, type IconName } from "../Icon";
import { Link } from "../Link";
import { ThemeDialog } from "../ThemeDialog";

const MODES: { id: ThemeMode; label: string; icon: IconName }[] = [
  { id: "light", label: "Light mode", icon: "sun" },
  { id: "dark", label: "Dark mode", icon: "moon" },
  { id: "system", label: "Match system", icon: "monitor" },
];

/** SidebarAccount is the account button at the bottom of the sidebar. Its popup holds the account links, light and dark mode and the theme picker. */
export function SidebarAccount({ user, collapsed }: { user: MeResponse; collapsed: boolean }) {
  const session = useSession();
  const [open, setOpen] = useState(false);
  const [themeOpen, setThemeOpen] = useState(false);
  const [rawTheme, setTheme] = usePref<unknown>(THEME_KEY, null);
  const theme = parseThemePrefs(rawTheme);
  const root = useRef<HTMLDivElement>(null);
  const name = user.display_name || user.username;
  const initial = (name[0] ?? "?").toUpperCase();

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
    setOpen(false);
    await session.signOut();
    navigate("/login", { replace: true });
  }

  return (
    <div className="sidebar-account" ref={root}>
      <button
        className="sidebar-account-btn"
        type="button"
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label={`${name} menu`}
        title={collapsed ? name : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="account-initial" aria-hidden="true">
          {initial}
        </span>
        <span className="sidebar-account-name nav-link-label">
          <strong>{name}</strong>
          <span className="meta">{user.roles.length ? user.roles.map(roleLabel).join(", ") : "No roles"}</span>
        </span>
        <Icon name="chevron-down" size={14} className="sidebar-account-caret nav-link-label" />
      </button>
      {open ? (
        <div className="menu-panel sidebar-account-panel" role="menu" aria-label="Account">
          <div className="sidebar-account-head">
            <span className="account-initial is-large" aria-hidden="true">
              {initial}
            </span>
            <span>
              <strong>{name}</strong>
              <span className="meta">{user.username !== name ? `${user.username} · ` : ""}{user.roles.length ? user.roles.map(roleLabel).join(", ") : "No roles"}</span>
            </span>
          </div>
          <div className="mode-switch" role="group" aria-label="Colour mode">
            {MODES.map((m) => (
              <button
                key={m.id}
                type="button"
                className="btn btn-ghost btn-sm btn-icon"
                aria-pressed={theme.mode === m.id}
                aria-label={m.label}
                title={m.label}
                onClick={() => setTheme({ ...theme, mode: m.id })}
              >
                <Icon name={m.icon} size={15} />
              </button>
            ))}
            <button
              type="button"
              className="btn btn-ghost btn-sm mode-theme-btn"
              onClick={() => {
                setOpen(false);
                setThemeOpen(true);
              }}
            >
              <Icon name="palette" size={15} />
              Theme
            </button>
          </div>
          <hr />
          <Link href="/me" role="menuitem" onClick={() => setOpen(false)}>
            <Icon name="account" size={14} />
            Account
          </Link>
          <Link href="/settings/mfa" role="menuitem" onClick={() => setOpen(false)}>
            <Icon name="key" size={14} />
            Authenticator
          </Link>
          <hr />
          <button type="button" role="menuitem" onClick={() => void onLogout()}>
            <Icon name="logout" size={14} />
            Log out
          </button>
        </div>
      ) : null}
      <ThemeDialog open={themeOpen} onClose={() => setThemeOpen(false)} />
    </div>
  );
}
