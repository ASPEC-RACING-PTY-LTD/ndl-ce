import { getPref, subscribePrefs } from "./prefs";

// Themes. Every colour in the stylesheets comes from a few tokens in
// styles.css; a theme sets the tint of the neutral greys and the accent hue
// and strength. Lightness stays with light or dark mode, so text keeps its
// contrast whatever colour is picked.

export type ThemeMode = "system" | "light" | "dark";

export type ThemePrefs = {
  mode: ThemeMode;
  theme: string;
  /** accent overrides the theme's accent: a hex colour, or "rainbow". */
  accent?: string | null;
};

export type ThemePreset = {
  id: string;
  label: string;
  description: string;
  /** Hue and strength of the neutral greys (strength 1 is the default tint). */
  hue: number;
  tint: number;
  accentHue: number;
  accentChroma: number;
};

export const THEME_KEY = "theme";

export const THEMES: ThemePreset[] = [
  { id: "nodal", label: "No-dal", description: "Slate with the No-dal green.", hue: 230, tint: 1, accentHue: 132, accentChroma: 0.19 },
  { id: "midnight", label: "Midnight", description: "Deep blue with a bright blue accent.", hue: 262, tint: 1.7, accentHue: 255, accentChroma: 0.15 },
  { id: "graphite", label: "Graphite", description: "Pure greys with an amber accent.", hue: 230, tint: 0, accentHue: 75, accentChroma: 0.15 },
  { id: "forest", label: "Forest", description: "Green-tinted greys with a leafy accent.", hue: 160, tint: 1.3, accentHue: 152, accentChroma: 0.16 },
  { id: "ocean", label: "Ocean", description: "Blue-green with a cyan accent.", hue: 215, tint: 1.5, accentHue: 200, accentChroma: 0.13 },
  { id: "dusk", label: "Dusk", description: "Violet greys with a purple accent.", hue: 290, tint: 1.4, accentHue: 300, accentChroma: 0.17 },
  { id: "rose", label: "Rose", description: "Warm greys with a pink accent.", hue: 350, tint: 1.1, accentHue: 355, accentChroma: 0.17 },
  { id: "ember", label: "Ember", description: "Warm greys with an orange accent.", hue: 45, tint: 0.9, accentHue: 40, accentChroma: 0.17 },
];

export const ACCENT_SWATCHES: { label: string; hex: string }[] = [
  { label: "Green", hex: "#9be34a" },
  { label: "Blue", hex: "#3b82f6" },
  { label: "Cyan", hex: "#22c5e0" },
  { label: "Purple", hex: "#a855f7" },
  { label: "Pink", hex: "#ec4899" },
  { label: "Red", hex: "#ef4444" },
  { label: "Orange", hex: "#f97316" },
  { label: "Yellow", hex: "#eab308" },
];

export const DEFAULT_THEME: ThemePrefs = { mode: "system", theme: "nodal", accent: null };

export function presetFor(id: string): ThemePreset {
  return THEMES.find((t) => t.id === id) ?? THEMES[0];
}

export function parseThemePrefs(raw: unknown): ThemePrefs {
  if (!raw || typeof raw !== "object") {
    return DEFAULT_THEME;
  }
  const r = raw as Partial<ThemePrefs>;
  const mode = r.mode === "light" || r.mode === "dark" || r.mode === "system" ? r.mode : "system";
  const theme = THEMES.some((t) => t.id === r.theme) ? (r.theme as string) : "nodal";
  const accent = r.accent === "rainbow" || (typeof r.accent === "string" && /^#[0-9a-f]{6}$/i.test(r.accent)) ? r.accent : null;
  return { mode, theme, accent };
}

/** hexToOklch converts an sRGB colour to OKLCH lightness, chroma and hue. */
export function hexToOklch(hex: string): { l: number; c: number; h: number } {
  const n = parseInt(hex.replace("#", ""), 16);
  const lin = (v: number) => {
    const x = v / 255;
    return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  };
  const r = lin((n >> 16) & 255);
  const g = lin((n >> 8) & 255);
  const b = lin(n & 255);
  const l_ = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m_ = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s_ = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const L = 0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_;
  const A = 1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_;
  const B = 0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_;
  const c = Math.sqrt(A * A + B * B);
  let h = (Math.atan2(B, A) * 180) / Math.PI;
  if (h < 0) {
    h += 360;
  }
  return { l: L, c, h };
}

export type ResolvedTheme = { hue: number; tint: number; accentHue: number; accentChroma: number; rainbow: boolean };

export function resolveTheme(p: ThemePrefs): ResolvedTheme {
  const preset = presetFor(p.theme);
  const out: ResolvedTheme = { hue: preset.hue, tint: preset.tint, accentHue: preset.accentHue, accentChroma: preset.accentChroma, rainbow: false };
  if (p.accent === "rainbow") {
    out.rainbow = true;
    out.accentChroma = 0.19;
  } else if (p.accent) {
    const { c, h } = hexToOklch(p.accent);
    out.accentHue = Math.round(h);
    // Very grey picks stay grey; others are kept vivid enough to read as a colour.
    out.accentChroma = c < 0.03 ? 0.02 : Math.min(0.22, Math.max(0.1, c));
  }
  return out;
}

/** applyTheme sets the theme tokens on the page. */
export function applyTheme(p: ThemePrefs, root: HTMLElement = document.documentElement): void {
  const t = resolveTheme(p);
  if (p.mode === "system") {
    delete root.dataset.mode;
  } else {
    root.dataset.mode = p.mode;
  }
  root.style.setProperty("--ndl-hue", String(t.hue));
  root.style.setProperty("--ndl-tint", String(t.tint));
  root.style.setProperty("--accent-c", String(t.accentChroma));
  if (t.rainbow) {
    root.dataset.accent = "rainbow";
    root.style.removeProperty("--accent-h");
  } else {
    delete root.dataset.accent;
    root.style.setProperty("--accent-h", String(t.accentHue));
  }
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute("content", isDark(p.mode) ? "#141820" : "#f3f5f8");
  }
}

export function isDark(mode: ThemeMode): boolean {
  if (mode === "system") {
    return typeof window === "undefined" || !window.matchMedia?.("(prefers-color-scheme: light)").matches;
  }
  return mode === "dark";
}

/** previewColors gives CSS colours for drawing a theme swatch. */
export function previewColors(p: ThemePrefs, dark: boolean) {
  const t = resolveTheme(p);
  const n = (l: number, c: number) => `oklch(${l} ${c * t.tint} ${t.hue})`;
  return dark
    ? {
        canvas: n(0.145, 0.016),
        surface: n(0.185, 0.018),
        line: n(0.3, 0.02),
        text: n(0.96, 0.01),
        muted: n(0.62, 0.02),
        accent: `oklch(0.84 ${t.accentChroma} ${t.accentHue})`,
      }
    : {
        canvas: n(0.965, 0.01),
        surface: n(0.99, 0.006),
        line: n(0.86, 0.016),
        text: n(0.2, 0.02),
        muted: n(0.48, 0.02),
        accent: `oklch(0.44 ${t.accentChroma * 0.79} ${t.accentHue})`,
      };
}

/** savedTheme reads the cached theme preference. */
export function savedTheme(): ThemePrefs {
  return parseThemePrefs(getPref<unknown>(THEME_KEY, null));
}

let previewing: ThemePrefs | null = null;

/** previewTheme shows a theme without saving it; null returns to the saved one. */
export function previewTheme(p: ThemePrefs | null): void {
  previewing = p;
  applyTheme(p ?? savedTheme());
}

/** startThemeSync applies the saved theme now and whenever it changes. */
export function startThemeSync(): void {
  let last = "";
  const sync = () => {
    if (previewing) {
      return;
    }
    const t = savedTheme();
    const key = JSON.stringify(t);
    if (key !== last) {
      last = key;
      applyTheme(t);
    }
  };
  sync();
  subscribePrefs(sync);
  window.matchMedia?.("(prefers-color-scheme: light)").addEventListener?.("change", () => applyTheme(previewing ?? savedTheme()));
}
