import { useEffect, useState } from "react";
import { patchMe } from "../api/client";
import {
  ACTIVE_PROFILE_KEY,
  DEFAULT_TERMINAL_FONT,
  PROFILES_KEY,
  TERMINAL_FONT_KEY,
  TERMINAL_FONT_STEPS,
  applyDesign,
  captureDesign,
  clampTerminalFont,
  newProfileId,
  parseProfiles,
  useAppearanceDefaults,
  useInstanceLayout,
  useNavLayout,
  useNotifyDisplay,
  type DesignProfile,
} from "../appearance/state";
import { CodeThemeEditor, ColourEditor, StyleEditor, TerminalColourEditor, ThemePreview } from "../appearance/ThemeEditor";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorNotice } from "../components/ErrorNotice";
import { PageNav, type PageNavItem } from "../components/PageNav";
import { UxModeToggle } from "../components/UxModeToggle";
import { setPref, usePref } from "../prefs";
import { usePath } from "../router";
import { useSession } from "../session";
import { DEFAULT_THEME, THEME_KEY, previewTheme, savedTheme, type ThemePrefs } from "../theme";
import { Segmented } from "../ui/Segmented";
import { Switch } from "../ui/Switch";
import { uxLevel } from "../ux";

type Section = "colours" | "style" | "code" | "layout" | "notifications" | "terminal" | "profiles";

const NAV: PageNavItem[] = [
  { id: "colours", label: "Colours", href: "/theme", icon: "palette", match: (p) => p === "/theme" },
  { id: "style", label: "Style", href: "/theme/style", icon: "columns" },
  { id: "code", label: "Code editor", href: "/theme/code", icon: "edit" },
  { id: "layout", label: "Layout", href: "/theme/layout", icon: "dashboard" },
  { id: "notifications", label: "Notifications", href: "/theme/notifications", icon: "bell" },
  { id: "terminal", label: "Terminal", href: "/theme/terminal", icon: "terminal" },
  { id: "profiles", label: "Design profiles", href: "/theme/profiles", icon: "users" },
];

function sectionOf(path: string): Section {
  const s = path.split("/")[2] as Section | "other" | undefined;
  // The old Other page moved into Layout, under Legacy.
  if (s === "other") {
    return "layout";
  }
  return s && NAV.some((n) => n.id === s) ? s : "colours";
}

/** ThemePage is where each person sets how No-dal looks and behaves for them. */
export function ThemePage() {
  const path = usePath();
  const section = sectionOf(path);
  const [rawTheme] = usePref<unknown>(THEME_KEY, null);
  const saved = savedTheme();
  const [draft, setDraft] = useState<ThemePrefs>(saved);
  const savedKey = JSON.stringify(saved);
  const changed = JSON.stringify(draft) !== savedKey;

  // A saved change from elsewhere (another tab, a profile switch) resets the draft.
  useEffect(() => {
    setDraft(savedTheme());
  }, [savedKey, rawTheme]);

  useEffect(() => {
    previewTheme(changed ? draft : null);
  }, [draft, changed]);

  useEffect(() => () => previewTheme(null), []);

  function apply(next: ThemePrefs = draft) {
    setPref(THEME_KEY, next);
    previewTheme(null);
    setDraft(next);
  }

  const themeSection = section === "colours" || section === "style" || section === "terminal";
  return (
    <section className="page page-wide theme-page" aria-labelledby="theme-heading">
      <PageNav id="theme" label="Theme" items={NAV} className="is-sub" />
      <header className="page-header">
        <h1 id="theme-heading">{NAV.find((n) => n.id === section)?.label}</h1>
        <p className="page-lede">
          {section === "code"
            ? "Copy a template or your live theme, change it yourself or with an AI, paste it back, then Test, Preview and Apply."
            : "Your choices are saved to your account and follow you to other browsers. They apply to you only."}
        </p>
      </header>
      {themeSection ? (
        <div className="theme-layout">
          <div className="panel theme-panel">
            {section === "colours" ? <ColourEditor draft={draft} onChange={setDraft} /> : null}
            {section === "style" ? <StyleEditor draft={draft} onChange={setDraft} /> : null}
            {section === "terminal" ? <TerminalSection draft={draft} onChange={setDraft} /> : null}
          </div>
          <aside className="theme-aside">
            <ThemePreview theme={draft} />
            <div className="theme-apply-bar">
              <button className="btn btn-ghost btn-sm" type="button" onClick={() => setDraft({ ...DEFAULT_THEME, mode: draft.mode })}>
                Reset to default
              </button>
              <span className="theme-spacer" />
              <button className="btn btn-secondary btn-sm" type="button" disabled={!changed} onClick={() => setDraft(saved)}>
                Discard
              </button>
              <button className="btn btn-primary btn-sm" type="button" disabled={!changed} onClick={() => apply()}>
                Apply
              </button>
            </div>
            {changed ? <p className="field-hint">Previewing across the app. Apply saves it; Discard or leaving the page puts your theme back.</p> : null}
          </aside>
        </div>
      ) : null}
      {section === "code" ? (
        <div className="panel">
          <CodeThemeEditor live={saved} onPreview={(t) => previewTheme(t)} onApply={(t) => apply(t)} />
        </div>
      ) : null}
      {section === "layout" ? <LayoutSection /> : null}
      {section === "notifications" ? <NotificationsSection /> : null}
      {section === "profiles" ? <ProfilesSection /> : null}
    </section>
  );
}

function TerminalSection({ draft, onChange }: { draft: ThemePrefs; onChange: (t: ThemePrefs) => void }) {
  const [font, setFont] = usePref<number>(TERMINAL_FONT_KEY, DEFAULT_TERMINAL_FONT);
  return (
    <div className="theme-editor">
      <div className="theme-row">
        <label className="field-label" htmlFor="term-font">
          Text size {clampTerminalFont(font)}px
        </label>
        <select id="term-font" value={clampTerminalFont(font)} onChange={(e) => setFont(Number(e.target.value))}>
          {TERMINAL_FONT_STEPS.map((s) => (
            <option key={s} value={s}>
              {s}px
            </option>
          ))}
        </select>
        <p className="field-hint">Hold Ctrl and scroll over a terminal to change it there. Saved as soon as you pick it.</p>
      </div>
      <div className="theme-row">
        <span className="field-label">Colours</span>
        <TerminalColourEditor draft={draft} onChange={onChange} />
      </div>
    </div>
  );
}

function LayoutSection() {
  const d = useAppearanceDefaults();
  const [nav, setNav, ownNav] = useNavLayout();
  const [inst, setInst, ownInst] = useInstanceLayout();
  return (
    <div className="panel theme-editor">
      <div className="theme-row">
        <span className="field-label">Navigation</span>
        <Segmented<string>
          ariaLabel="Navigation"
          value={ownNav ? nav : "default"}
          onChange={(v) => setNav(v === "default" ? undefined : (v as "modern" | "classic"))}
          options={[
            { id: "default", label: `Default (${d.nav === "modern" ? "Modern" : "Classic"})` },
            { id: "modern", label: "Modern" },
            { id: "classic", label: "Classic" },
          ]}
        />
        <p className="field-hint">
          Modern keeps the sidebar to Dashboard, Compute and Administration, with a page bar of sections and dropdowns at the top of each page. Classic lists
          every page in the sidebar.
        </p>
      </div>
      <div className="theme-row">
        <span className="field-label">Compute pages</span>
        <Segmented<string>
          ariaLabel="Compute pages"
          value={ownInst ? inst : "default"}
          onChange={(v) => setInst(v === "default" ? undefined : (v as "instance" | "classic"))}
          options={[
            { id: "default", label: `Default (${d.instance === "instance" ? "Instance" : "Classic"})` },
            { id: "instance", label: "Instance" },
            { id: "classic", label: "Classic" },
          ]}
        />
        <p className="field-hint">
          Instance gathers Workloads, VMs, Game Servers, OCI Containers and Docker under one Instance page with an overview. Classic keeps each as its own
          sidebar entry.
        </p>
      </div>
      <p className="field-hint">Page bars: drag a button to move it, or right-click it. The order is saved for you on every page.</p>
      <LegacySection />
    </div>
  );
}

/** NotificationsSection is how notifications show; Theme and Settings both hold it. */
export function NotificationsSection() {
  const [n, setN] = useNotifyDisplay();
  return (
    <div className="panel theme-editor">
      <div className="theme-row">
        <Switch id="notify-toast" checked={n.toast} onChange={(e) => setN({ ...n, toast: e.target.checked })} label="Pop-up notifications" />
        <p className="field-hint">New notifications slide down from the top of the page and go away on their own.</p>
      </div>
      <div className="theme-row">
        <label className="field-label" htmlFor="notify-seconds">
          Pop-ups stay for {n.seconds} seconds
        </label>
        <input
          id="notify-seconds"
          type="range"
          min={2}
          max={5}
          step={1}
          disabled={!n.toast}
          value={n.seconds}
          onChange={(e) => setN({ ...n, seconds: Number(e.target.value) })}
        />
      </div>
      <div className="theme-row">
        <Switch id="notify-panel" checked={n.panel} onChange={(e) => setN({ ...n, panel: e.target.checked })} label="Bell panel" />
        <p className="field-hint">The bell in the header opens a panel of your notifications, sized to fit them and your window.</p>
      </div>
      {!n.panel && !n.toast ? <p className="banner banner-warn">Notifications will not show anywhere except the Notifications page.</p> : null}
    </div>
  );
}

function ProfilesSection() {
  const { profileLimit } = useAppearanceDefaults();
  const [raw, setRaw] = usePref<unknown>(PROFILES_KEY, []);
  const [active, setActive] = usePref<string>(ACTIVE_PROFILE_KEY, "");
  const profiles = parseProfiles(raw);
  const [name, setName] = useState("");
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [deleting, setDeleting] = useState<DesignProfile | null>(null);
  const [error, setError] = useState("");
  const full = profiles.length >= profileLimit;

  function save(next: DesignProfile[]) {
    setError("");
    setRaw(next);
  }

  function create() {
    const n = name.trim() || `Profile ${profiles.length + 1}`;
    if (n.length > 40) {
      setError("Names are up to 40 characters.");
      return;
    }
    const p: DesignProfile = { id: newProfileId(), name: n, data: captureDesign() };
    save([...profiles, p]);
    setActive(p.id);
    setName("");
  }

  function switchTo(p: DesignProfile) {
    applyDesign(p.data);
    setActive(p.id);
  }

  return (
    <div className="panel theme-editor">
      <p className="field-hint">
        A design profile saves your whole look: theme, style, layouts, page bar order, notifications and terminal settings. Switch between them in one click.
        You can keep up to {profileLimit} {profileLimit === 1 ? "profile" : "profiles"}.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <ul className="profile-list" role="list">
        {profiles.map((p, i) => {
          const readOnly = i >= profileLimit;
          return (
            <li key={p.id} className={"profile-row" + (p.id === active ? " is-active" : "")}>
              <ThemePreview theme={(p.data.theme as ThemePrefs) ?? DEFAULT_THEME} compact />
              <div className="profile-row-text">
                {renaming?.id === p.id ? (
                  <input
                    aria-label="Profile name"
                    value={renaming.name}
                    maxLength={40}
                    autoFocus
                    onChange={(e) => setRenaming({ id: p.id, name: e.target.value })}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && renaming.name.trim()) {
                        save(profiles.map((x) => (x.id === p.id ? { ...x, name: renaming.name.trim() } : x)));
                        setRenaming(null);
                      } else if (e.key === "Escape") {
                        setRenaming(null);
                      }
                    }}
                  />
                ) : (
                  <strong>{p.name}</strong>
                )}
                <span className="meta">
                  {p.id === active ? "In use" : "Saved"}
                  {readOnly ? " · read-only: over your limit of " + profileLimit : ""}
                </span>
              </div>
              <div className="btn-row">
                <button className="btn btn-primary btn-sm" type="button" disabled={p.id === active} onClick={() => switchTo(p)}>
                  Switch to
                </button>
                <button
                  className="btn btn-secondary btn-sm"
                  type="button"
                  disabled={readOnly}
                  onClick={() => save(profiles.map((x) => (x.id === p.id ? { ...x, data: captureDesign() } : x)))}
                >
                  Save current look
                </button>
                <button className="btn btn-ghost btn-sm" type="button" disabled={readOnly} onClick={() => setRenaming({ id: p.id, name: p.name })}>
                  Rename
                </button>
                <button className="btn btn-ghost btn-sm" type="button" onClick={() => setDeleting(p)}>
                  Delete
                </button>
              </div>
            </li>
          );
        })}
      </ul>
      <div className="profile-new">
        <input aria-label="New profile name" placeholder="Name, for example Night shift" maxLength={40} value={name} disabled={full} onChange={(e) => setName(e.target.value)} />
        <button className="btn btn-primary btn-sm" type="button" disabled={full} onClick={create}>
          Save current look as a profile
        </button>
        {full ? <span className="meta">You have reached your limit. Delete one to add another.</span> : null}
      </div>
      <ConfirmDialog
        open={deleting !== null}
        title="Delete design profile"
        confirmLabel="Delete"
        danger
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting) {
            save(profiles.filter((x) => x.id !== deleting.id));
            if (active === deleting.id) {
              setActive("");
            }
          }
          setDeleting(null);
        }}
      >
        <p>Delete &quot;{deleting?.name ?? ""}&quot;? Your current look stays as it is.</p>
      </ConfirmDialog>
    </div>
  );
}

/** LegacySection holds older options kept for people who still use them. */
function LegacySection() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [level, setLevel] = useState(uxLevel(user));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function change(next: "guided" | "advanced" | "expert") {
    setBusy(true);
    setError("");
    try {
      await patchMe({ ux_level: next, expert_ack: next === "expert" ? true : undefined });
      setLevel(next);
      if (session.status === "ready") {
        await session.refresh();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="theme-legacy">
      <h2 className="theme-legacy-title">Legacy</h2>
      <div className="theme-row">
        <span className="field-label">Form detail</span>
        <div aria-busy={busy}>
          <UxModeToggle value={level} onChange={(v) => void change(v)} />
        </div>
        <p className="field-hint">
          How many options create forms show. Guided shows the essentials, Advanced adds tuning, Expert shows everything including settings that can break
          a workload. Choosing Expert confirms you understand that.
        </p>
        {error ? <ErrorNotice error={error} /> : null}
      </div>
    </div>
  );
}
