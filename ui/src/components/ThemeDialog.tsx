import { useEffect, useState } from "react";
import { setPref } from "../prefs";
import {
  ACCENT_SWATCHES,
  THEMES,
  THEME_KEY,
  isDark,
  previewColors,
  previewTheme,
  savedTheme,
  type ThemeMode,
  type ThemePrefs,
} from "../theme";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import { Icon } from "./Icon";
import { StatusBadge } from "./StatusBadge";

/** ThemeSwatch draws a small picture of the app in a theme. */
function ThemeSwatch({ prefs, dark }: { prefs: ThemePrefs; dark: boolean }) {
  const c = previewColors(prefs, dark);
  const accent = prefs.accent === "rainbow" ? "linear-gradient(90deg, #ef4444, #f97316, #eab308, #22c55e, #3b82f6, #a855f7, #ec4899)" : c.accent;
  return (
    <span className="theme-swatch" style={{ background: c.canvas, borderColor: c.line }} aria-hidden="true">
      <span className="theme-swatch-side" style={{ background: c.surface, borderColor: c.line }}>
        <span style={{ background: accent }} />
        <span style={{ background: c.muted }} />
        <span style={{ background: c.muted }} />
      </span>
      <span className="theme-swatch-main">
        <span className="theme-swatch-line" style={{ background: c.text }} />
        <span className="theme-swatch-line is-short" style={{ background: c.muted }} />
        <span className="theme-swatch-card" style={{ background: c.surface, borderColor: c.line }} />
        <span className="theme-swatch-btn" style={{ background: accent }} />
      </span>
    </span>
  );
}

/** ThemeDialog previews themes and accent colours live; Apply saves them to the user's profile. */
export function ThemeDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [draft, setDraft] = useState<ThemePrefs>(() => savedTheme());
  const [custom, setCustom] = useState("#9be34a");

  useEffect(() => {
    if (open) {
      const saved = savedTheme();
      setDraft(saved);
      if (saved.accent && saved.accent !== "rainbow") {
        setCustom(saved.accent);
      }
    }
  }, [open]);

  useEffect(() => {
    if (open) {
      previewTheme(draft);
    }
  }, [draft, open]);

  function close() {
    previewTheme(null);
    onClose();
  }

  function apply() {
    setPref(THEME_KEY, draft);
    previewTheme(null);
    onClose();
  }

  const dark = isDark(draft.mode);
  const saved = savedTheme();
  const changed = JSON.stringify(saved) !== JSON.stringify(draft);

  return (
    <Dialog
      open={open}
      title="Theme"
      wide
      onClose={close}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={() => setDraft({ mode: draft.mode, theme: "nodal", accent: null })}>
            Reset to default
          </button>
          <span className="theme-spacer" />
          <button className="btn btn-secondary" type="button" onClick={close}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" disabled={!changed} onClick={apply}>
            Apply
          </button>
        </div>
      }
    >
      <p className="field-hint">Changes show across the app while this is open. Apply saves them to your profile, so they follow you to other browsers.</p>
      <div className="theme-row">
        <span className="field-label">Mode</span>
        <Segmented<ThemeMode>
          ariaLabel="Mode"
          value={draft.mode}
          onChange={(mode) => setDraft({ ...draft, mode })}
          options={[
            { id: "light", label: "Light" },
            { id: "dark", label: "Dark" },
            { id: "system", label: "Match system" },
          ]}
        />
      </div>
      <div className="theme-grid" role="radiogroup" aria-label="Themes">
        {THEMES.map((t) => {
          const p: ThemePrefs = { ...draft, theme: t.id, accent: draft.theme === t.id ? draft.accent : null };
          return (
            <button
              key={t.id}
              type="button"
              role="radio"
              aria-checked={draft.theme === t.id}
              className={"theme-card" + (draft.theme === t.id ? " is-selected" : "")}
              onClick={() => setDraft({ ...draft, theme: t.id, accent: null })}
            >
              <ThemeSwatch prefs={p} dark={dark} />
              <span className="theme-card-text">
                <strong>{t.label}</strong>
                <span className="meta">{t.description}</span>
              </span>
              {draft.theme === t.id ? <Icon name="check" size={14} className="theme-card-check" /> : null}
            </button>
          );
        })}
      </div>
      <div className="theme-row">
        <span className="field-label">Accent colour</span>
        <div className="accent-row" role="radiogroup" aria-label="Accent colour">
          <button
            type="button"
            role="radio"
            aria-checked={!draft.accent}
            className={"accent-chip is-default" + (!draft.accent ? " is-selected" : "")}
            title="The theme's own accent"
            onClick={() => setDraft({ ...draft, accent: null })}
          >
            Theme
          </button>
          {ACCENT_SWATCHES.map((s) => (
            <button
              key={s.hex}
              type="button"
              role="radio"
              aria-checked={draft.accent === s.hex}
              aria-label={s.label}
              title={s.label}
              className={"accent-chip" + (draft.accent === s.hex ? " is-selected" : "")}
              style={{ background: s.hex }}
              onClick={() => setDraft({ ...draft, accent: s.hex })}
            />
          ))}
          <button
            type="button"
            role="radio"
            aria-checked={draft.accent === "rainbow"}
            aria-label="Rainbow"
            title="Rainbow: the accent slowly turns through every colour"
            className={"accent-chip is-rainbow" + (draft.accent === "rainbow" ? " is-selected" : "")}
            onClick={() => setDraft({ ...draft, accent: "rainbow" })}
          />
          <label className={"accent-custom" + (draft.accent === custom ? " is-selected" : "")} title="Pick any colour">
            <input
              type="color"
              aria-label="Custom accent colour"
              value={custom}
              onChange={(e) => {
                setCustom(e.target.value);
                setDraft({ ...draft, accent: e.target.value.toLowerCase() });
              }}
            />
            <span>Custom</span>
          </label>
        </div>
        <p className="field-hint">Any colour works. Its brightness is adjusted for light and dark mode so text on it stays readable.</p>
      </div>
      <div className="theme-preview" aria-label="Preview">
        <button className="btn btn-primary btn-sm" type="button" tabIndex={-1}>
          Primary
        </button>
        <button className="btn btn-secondary btn-sm" type="button" tabIndex={-1}>
          Secondary
        </button>
        <a href="#theme" onClick={(e) => e.preventDefault()} tabIndex={-1}>
          A link
        </a>
        <span className="tag-inline">Tag</span>
        <StatusBadge status="healthy" label="Healthy" />
      </div>
    </Dialog>
  );
}
