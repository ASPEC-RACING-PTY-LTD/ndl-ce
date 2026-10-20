import { lazy, Suspense, useState, type CSSProperties } from "react";
import {
  ACCENT_SWATCHES,
  DEFAULT_STYLE,
  STYLES,
  TERMINAL_COLOR_KEYS,
  THEMES,
  isDark,
  previewColors,
  type Density,
  type StyleId,
  type ThemeMode,
  type ThemePrefs,
} from "../theme";
import { Segmented } from "../ui/Segmented";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { CODE_TEMPLATES, checkThemeCode, themeToCode, type CodeCheck } from "./themeCode";

const CodeEditor = lazy(() => import("../components/CodeEditor"));

const STYLE_SHAPE: Record<StyleId, { r: string; c: string; lg: string; shadow: string }> = {
  bubble: { r: "10px", c: "999px", lg: "16px", shadow: "0 4px 14px rgb(0 0 0 / 0.18)" },
  plain: { r: "4px", c: "4px", lg: "6px", shadow: "none" },
  boxy: { r: "0", c: "0", lg: "0", shadow: "4px 4px 0 rgb(127 127 127 / 0.45)" },
  compact: { r: "8px", c: "999px", lg: "12px", shadow: "0 2px 8px rgb(0 0 0 / 0.15)" },
  glass: { r: "12px", c: "10px", lg: "18px", shadow: "0 8px 30px rgb(0 0 0 / 0.25)" },
};

const RAINBOW = "linear-gradient(90deg, #ef4444, #f97316, #eab308, #22c55e, #3b82f6, #a855f7, #ec4899)";

/** ThemePreview draws a small working picture of a theme and style. */
export function ThemePreview({ theme, compact = false }: { theme: ThemePrefs; compact?: boolean }) {
  const dark = isDark(theme.mode);
  const c = previewColors(theme, dark);
  const shape = STYLE_SHAPE[theme.style ?? DEFAULT_STYLE];
  const radius = typeof theme.radius === "number" ? `${theme.radius}px` : null;
  const tok = theme.tokens ?? {};
  const accent = theme.accent === "rainbow" ? RAINBOW : (tok["--accent"] ?? c.accent);
  const secondary = theme.secondary ?? (tok["--bg-elevated"] ?? c.surface);
  const style: CSSProperties = {
    background: tok["--bg-canvas"] ?? c.canvas,
    color: tok["--fg-primary"] ?? c.text,
    borderColor: tok["--line"] ?? c.line,
    borderRadius: radius ?? shape.lg,
  };
  const card: CSSProperties = {
    background: tok["--bg-surface"] ?? c.surface,
    borderColor: tok["--line"] ?? c.line,
    borderRadius: radius ?? shape.lg,
    boxShadow: shape.shadow,
  };
  const ctl = radius ?? shape.c;
  return (
    <div className={"theme-preview-box" + (compact ? " is-compact" : "")} style={style} aria-label="Theme preview">
      <div className="theme-preview-card" style={card}>
        <strong>Workloads</strong>
        <span style={{ color: c.muted }}>3 running, 1 stopped</span>
        <div className="theme-preview-row">
          <span className="theme-preview-btn" style={{ background: accent, color: dark ? "#111" : "#fff", borderRadius: ctl }}>
            Primary
          </span>
          <span
            className="theme-preview-btn"
            style={{ background: theme.secondary ? secondary : "transparent", border: `1px solid ${tok["--line"] ?? c.line}`, borderRadius: ctl }}
          >
            Secondary
          </span>
          <span className="theme-preview-chip" style={{ borderColor: tok["--line"] ?? c.line, background: theme.secondary ? secondary : "transparent" }}>
            Tag
          </span>
        </div>
        {!compact ? (
          <div className="theme-preview-input" style={{ borderColor: tok["--line"] ?? c.line, borderRadius: ctl }}>
            Search…
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** ThemeSwatch is the small picture on a preset card. */
function ThemeSwatch({ prefs, dark }: { prefs: ThemePrefs; dark: boolean }) {
  const c = previewColors(prefs, dark);
  const accent = prefs.accent === "rainbow" ? RAINBOW : c.accent;
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

type Target = "accent" | "secondary";

/**
 * ColourEditor picks the mode, a preset and the two colours. The Primary
 * and Secondary buttons at the bottom choose which colour the swatches set.
 */
export function ColourEditor({ draft, onChange }: { draft: ThemePrefs; onChange: (t: ThemePrefs) => void }) {
  const [target, setTarget] = useState<Target>("accent");
  const [custom, setCustom] = useState<Record<Target, string>>({
    accent: draft.accent && draft.accent !== "rainbow" ? draft.accent : "#9be34a",
    secondary: draft.secondary ?? "#3b82f6",
  });
  const dark = isDark(draft.mode);
  const value = target === "accent" ? draft.accent : draft.secondary;
  const set = (v: string | null) => onChange(target === "accent" ? { ...draft, accent: v } : { ...draft, secondary: v });
  return (
    <div className="theme-editor">
      <div className="theme-row">
        <span className="field-label">Mode</span>
        <Segmented<ThemeMode>
          ariaLabel="Mode"
          value={draft.mode}
          onChange={(mode) => onChange({ ...draft, mode })}
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
          const on = draft.theme === t.id;
          return (
            <button
              key={t.id}
              type="button"
              role="radio"
              aria-checked={on}
              className={"theme-card" + (on ? " is-selected" : "")}
              onClick={() => onChange({ ...draft, theme: t.id, accent: null })}
            >
              <ThemeSwatch prefs={p} dark={dark} />
              <span className="theme-card-text">
                <strong>{t.label}</strong>
                <span className="meta">{t.description}</span>
              </span>
              {on ? <Icon name="check" size={14} className="theme-card-check" /> : null}
            </button>
          );
        })}
      </div>
      <div className="theme-row">
        <span className="field-label">{target === "accent" ? "Primary colour" : "Secondary colour"}</span>
        <div className="accent-row" role="radiogroup" aria-label={target === "accent" ? "Primary colour" : "Secondary colour"}>
          <button
            type="button"
            role="radio"
            aria-checked={!value}
            className={"accent-chip is-default" + (!value ? " is-selected" : "")}
            title={target === "accent" ? "The theme's own accent" : "Neutral, from the theme"}
            onClick={() => set(null)}
          >
            Theme
          </button>
          {ACCENT_SWATCHES.map((s) => (
            <button
              key={s.hex}
              type="button"
              role="radio"
              aria-checked={value === s.hex}
              aria-label={s.label}
              title={s.label}
              className={"accent-chip" + (value === s.hex ? " is-selected" : "")}
              style={{ background: s.hex }}
              onClick={() => set(s.hex)}
            />
          ))}
          {target === "accent" ? (
            <button
              type="button"
              role="radio"
              aria-checked={value === "rainbow"}
              aria-label="Rainbow"
              title="Rainbow: the accent slowly turns through every colour"
              className={"accent-chip is-rainbow" + (value === "rainbow" ? " is-selected" : "")}
              onClick={() => set("rainbow")}
            />
          ) : null}
          <label className={"accent-custom" + (value === custom[target] ? " is-selected" : "")} title="Pick any colour">
            <input
              type="color"
              aria-label={target === "accent" ? "Custom primary colour" : "Custom secondary colour"}
              value={custom[target]}
              onChange={(e) => {
                setCustom({ ...custom, [target]: e.target.value });
                set(e.target.value.toLowerCase());
              }}
            />
            <span>Custom</span>
          </label>
        </div>
        <p className="field-hint">
          {target === "accent"
            ? "The primary colour marks main buttons, links, the current page and focus."
            : "The secondary colour tints secondary buttons, chips, tags and selected options."}{" "}
          Brightness is adjusted for light and dark mode so text stays readable.
        </p>
      </div>
      <div className="theme-preview" aria-label="Choose which colour to set">
        <button
          className={"btn btn-primary btn-sm" + (target === "accent" ? " is-target" : "")}
          type="button"
          aria-pressed={target === "accent"}
          onClick={() => setTarget("accent")}
        >
          Primary
        </button>
        <button
          className={"btn btn-secondary btn-sm" + (target === "secondary" ? " is-target" : "")}
          type="button"
          aria-pressed={target === "secondary"}
          onClick={() => setTarget("secondary")}
        >
          Secondary
        </button>
        <a href="#theme" onClick={(e) => e.preventDefault()} tabIndex={-1}>
          A link
        </a>
        <span className="tag-inline">Tag</span>
        <StatusBadge status="healthy" label="Healthy" />
        <span className="meta">Click Primary or Secondary to choose which colour the swatches set.</span>
      </div>
    </div>
  );
}

/** StyleEditor picks the style (shape), density and corner radius. */
export function StyleEditor({ draft, onChange }: { draft: ThemePrefs; onChange: (t: ThemePrefs) => void }) {
  const style = draft.style ?? DEFAULT_STYLE;
  return (
    <div className="theme-editor">
      <div className="style-grid" role="radiogroup" aria-label="Styles">
        {STYLES.map((s) => (
          <button
            key={s.id}
            type="button"
            role="radio"
            aria-checked={style === s.id}
            className={"style-card" + (style === s.id ? " is-selected" : "")}
            onClick={() => onChange({ ...draft, style: s.id, radius: null })}
          >
            <ThemePreview theme={{ ...draft, style: s.id, radius: null }} compact />
            <span className="theme-card-text">
              <strong>{s.label}</strong>
              <span className="meta">{s.description}</span>
            </span>
          </button>
        ))}
      </div>
      <div className="theme-row">
        <span className="field-label">Spacing</span>
        <Segmented<Density>
          ariaLabel="Spacing"
          value={draft.density ?? "comfortable"}
          onChange={(density) => onChange({ ...draft, density })}
          options={[
            { id: "compact", label: "Compact" },
            { id: "comfortable", label: "Comfortable" },
            { id: "spacious", label: "Spacious" },
          ]}
        />
      </div>
      <div className="theme-row">
        <label className="field-label" htmlFor="theme-radius">
          Corner radius {typeof draft.radius === "number" ? `${draft.radius}px` : "(from the style)"}
        </label>
        <div className="theme-radius">
          <input
            id="theme-radius"
            type="range"
            min={0}
            max={24}
            step={1}
            value={typeof draft.radius === "number" ? draft.radius : 10}
            onChange={(e) => onChange({ ...draft, radius: Number(e.target.value) })}
          />
          <button className="btn btn-ghost btn-sm" type="button" disabled={typeof draft.radius !== "number"} onClick={() => onChange({ ...draft, radius: null })}>
            Use the style's
          </button>
        </div>
      </div>
    </div>
  );
}

/** TerminalColourEditor sets terminal colours; empty ones use the built-in palette. */
export function TerminalColourEditor({ draft, onChange }: { draft: ThemePrefs; onChange: (t: ThemePrefs) => void }) {
  const colours = draft.terminal ?? {};
  function set(key: string, v: string | null) {
    const next = { ...colours };
    if (v) {
      next[key] = v;
    } else {
      delete next[key];
    }
    onChange({ ...draft, terminal: Object.keys(next).length ? next : undefined });
  }
  return (
    <div className="terminal-colours">
      <div className="terminal-colour-sample" style={{ background: colours.background ?? "#11141b", color: colours.foreground ?? "#e8ecf4" }}>
        <span style={{ color: colours.green ?? "#9be34a" }}>root@web</span>:<span style={{ color: colours.blue ?? "#60a5fa" }}>~</span>$ ls
        <br />
        <span style={{ color: colours.cyan ?? "#22d3ee" }}>app</span> <span style={{ color: colours.yellow ?? "#eab308" }}>backup.tar</span>{" "}
        <span style={{ color: colours.red ?? "#ef4444" }}>error.log</span>
      </div>
      <div className="terminal-colour-grid">
        {TERMINAL_COLOR_KEYS.map((k) => (
          <label key={k} className="terminal-colour">
            <input
              type="color"
              aria-label={`Terminal ${k}`}
              value={/^#[0-9a-f]{6}$/i.test(colours[k] ?? "") ? colours[k] : "#000000"}
              onChange={(e) => set(k, e.target.value)}
            />
            <span>{k.replace(/([A-Z])/g, " $1").toLowerCase()}</span>
            {colours[k] ? (
              <button type="button" className="btn btn-ghost btn-xs" aria-label={`Reset ${k}`} onClick={() => set(k, null)}>
                Reset
              </button>
            ) : null}
          </label>
        ))}
      </div>
    </div>
  );
}

/**
 * CodeThemeEditor copies a template or the live theme as JSON, lets the
 * person edit or paste it, Tests it, shows a Preview and Applies it.
 */
export function CodeThemeEditor({
  live,
  onPreview,
  onApply,
  applyLabel = "Apply",
}: {
  live: ThemePrefs;
  onPreview: (t: ThemePrefs | null) => void;
  onApply: (t: ThemePrefs) => void;
  applyLabel?: string;
}) {
  const [code, setCode] = useState(() => themeToCode(live));
  const [template, setTemplate] = useState("current");
  const [check, setCheck] = useState<CodeCheck | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [copied, setCopied] = useState("");

  function load(id: string) {
    setTemplate(id);
    const t = id === "current" ? live : (CODE_TEMPLATES.find((x) => x.id === id)?.theme ?? live);
    setCode(themeToCode(t));
    setCheck(null);
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied("Copied. Paste it into your editor or an AI chat, then paste the result back here.");
    } catch {
      setCopied("Copying was blocked by the browser. Select the code and copy it by hand.");
    }
  }

  function test(): CodeCheck {
    const res = checkThemeCode(code);
    setCheck(res);
    return res;
  }

  function preview() {
    const res = test();
    if (res.ok && res.theme) {
      setPreviewing(true);
      onPreview(res.theme);
    }
  }

  function stopPreview() {
    setPreviewing(false);
    onPreview(null);
  }

  function apply() {
    const res = test();
    if (res.ok && res.theme) {
      setPreviewing(false);
      onApply(res.theme);
    }
  }

  const tested = check?.ok ? check.theme : null;
  return (
    <div className="theme-code">
      <div className="theme-code-bar">
        <label className="field-label" htmlFor="theme-template">
          Start from
        </label>
        <select id="theme-template" value={template} onChange={(e) => load(e.target.value)}>
          {CODE_TEMPLATES.map((t) => (
            <option key={t.id} value={t.id}>
              {t.label}
            </option>
          ))}
        </select>
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => void copy()}>
          Copy code
        </button>
        <span className="theme-spacer" />
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => test()}>
          Test
        </button>
        {previewing ? (
          <button className="btn btn-secondary btn-sm" type="button" onClick={stopPreview}>
            Stop preview
          </button>
        ) : (
          <button className="btn btn-secondary btn-sm" type="button" onClick={preview}>
            Preview
          </button>
        )}
        <button className="btn btn-primary btn-sm" type="button" onClick={apply}>
          {applyLabel}
        </button>
      </div>
      {copied ? (
        <p className="field-hint" role="status">
          {copied}
        </p>
      ) : null}
      <div className="theme-code-split">
        <Suspense
          fallback={
            <textarea className="code-fallback" aria-label="Theme code" value={code} onChange={(e) => setCode(e.target.value)} spellCheck={false} />
          }
        >
          <CodeEditor value={code} onChange={(v) => setCode(v)} label="Theme code" />
        </Suspense>
        <aside className="theme-code-side">
          {check ? (
            <div className={"banner " + (check.ok ? "banner-ok" : "banner-error")} role="status">
              <strong>{check.ok ? "Compatible and safe to use." : "This code cannot be used yet."}</strong>
              {check.errors.length ? (
                <ul>
                  {check.errors.map((e) => (
                    <li key={e}>{e}</li>
                  ))}
                </ul>
              ) : null}
              {check.warnings.length ? (
                <ul className="meta">
                  {check.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : (
            <p className="field-hint">
              Only UI tokens are allowed: colours, sizes, font names and terminal colours. Test checks the code before Preview or {applyLabel}.
            </p>
          )}
          <ThemePreview theme={tested ?? live} />
        </aside>
      </div>
    </div>
  );
}
