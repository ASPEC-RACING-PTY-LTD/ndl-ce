import {
  DEFAULT_STYLE,
  STYLES,
  TERMINAL_COLOR_KEYS,
  THEMES,
  parseThemePrefs,
  unsafeTokenValue,
  validTokenName,
  type ThemePrefs,
} from "../theme";

// The theme code editor works on a JSON document of UI tokens only: mode,
// preset, accent and secondary colours, style, density, radius, CSS
// variables and terminal colours. It never holds markup or script, and the
// server checks it again with the same rules.

const KEYS = ["name", "mode", "theme", "accent", "secondary", "style", "density", "radius", "font", "tokens", "terminal"];

/** Tokens the stylesheets read, offered in templates and the editor's hints. */
export const KNOWN_TOKENS = [
  "--bg-canvas",
  "--bg-surface",
  "--bg-elevated",
  "--bg-hover",
  "--fg-primary",
  "--fg-secondary",
  "--fg-tertiary",
  "--line",
  "--accent",
  "--accent-fg",
  "--accent-soft",
  "--ok",
  "--warn",
  "--danger",
  "--info",
  "--radius",
  "--radius-lg",
  "--control-radius",
  "--font",
  "--mono",
  "--space-3",
  "--space-4",
  "--space-5",
  "--control-h",
  "--row-h",
];

export type CodeCheck = {
  ok: boolean;
  errors: string[];
  warnings: string[];
  theme?: ThemePrefs;
};

const HEX = /^#[0-9a-f]{6}$/i;

/** checkThemeCode tests pasted theme code: whether it parses, is complete enough and is safe. */
export function checkThemeCode(text: string): CodeCheck {
  const errors: string[] = [];
  const warnings: string[] = [];
  if (text.length > 16 * 1024) {
    return { ok: false, errors: ["The code is larger than 16 KB."], warnings };
  }
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch (err) {
    return { ok: false, errors: [`Not valid JSON: ${err instanceof Error ? err.message : String(err)}`], warnings };
  }
  if (!doc || typeof doc !== "object" || Array.isArray(doc)) {
    return { ok: false, errors: ["The code must be one JSON object, like the template."], warnings };
  }
  const r = doc as Record<string, unknown>;
  for (const k of Object.keys(r)) {
    if (!KEYS.includes(k)) {
      errors.push(`"${k}" is not a theme setting. Allowed: ${KEYS.join(", ")}.`);
    }
  }
  if (r.mode !== undefined && !["system", "light", "dark"].includes(String(r.mode))) {
    errors.push(`"mode" must be "system", "light" or "dark".`);
  }
  if (r.theme !== undefined && !THEMES.some((t) => t.id === r.theme)) {
    warnings.push(`"theme" ${JSON.stringify(r.theme)} is not a preset; No-dal is used. Presets: ${THEMES.map((t) => t.id).join(", ")}.`);
  }
  for (const key of ["accent", "secondary"]) {
    const v = r[key];
    if (v !== undefined && v !== null && !(key === "accent" && v === "rainbow") && !(typeof v === "string" && HEX.test(v))) {
      errors.push(`"${key}" must be a colour like "#3b82f6"${key === "accent" ? ', "rainbow"' : ""} or null.`);
    }
  }
  if (r.style !== undefined && !STYLES.some((x) => x.id === r.style)) {
    errors.push(`"style" must be one of ${STYLES.map((x) => x.id).join(", ")}.`);
  }
  if (r.density !== undefined && !["comfortable", "compact", "spacious"].includes(String(r.density))) {
    errors.push(`"density" must be "comfortable", "compact" or "spacious".`);
  }
  if (r.radius !== undefined && r.radius !== null && (typeof r.radius !== "number" || r.radius < 0 || r.radius > 32)) {
    errors.push(`"radius" must be a number from 0 to 32.`);
  }
  for (const key of ["name", "font"]) {
    const v = r[key];
    if (v !== undefined && (typeof v !== "string" || unsafeTokenValue(v))) {
      errors.push(`"${key}" must be plain text.`);
    }
  }
  const checkMap = (key: "tokens" | "terminal") => {
    const v = r[key];
    if (v === undefined) {
      return;
    }
    if (!v || typeof v !== "object" || Array.isArray(v)) {
      errors.push(`"${key}" must be an object of names and values.`);
      return;
    }
    for (const [name, val] of Object.entries(v as Record<string, unknown>)) {
      if (key === "tokens" && !validTokenName(name)) {
        errors.push(`Token "${name}" must be a CSS variable name such as --accent.`);
        continue;
      }
      if (key === "terminal" && !(TERMINAL_COLOR_KEYS as readonly string[]).includes(name)) {
        errors.push(`Terminal colour "${name}" is not one of ${TERMINAL_COLOR_KEYS.join(", ")}.`);
        continue;
      }
      if (typeof val !== "string") {
        errors.push(`${key}.${name} must be text.`);
        continue;
      }
      const why = unsafeTokenValue(val);
      if (why) {
        errors.push(`${key}.${name}: ${why}.`);
      } else if (key === "tokens" && !KNOWN_TOKENS.includes(name)) {
        warnings.push(`Token ${name} is not used by the built-in styles; it only matters if something reads it.`);
      }
    }
  };
  checkMap("tokens");
  checkMap("terminal");
  if (errors.length > 0) {
    return { ok: false, errors, warnings };
  }
  const theme = parseThemePrefs(r);
  return { ok: true, errors, warnings, theme };
}

/** themeToCode is the editable JSON for a theme. */
export function themeToCode(t: ThemePrefs): string {
  const out: Record<string, unknown> = {
    mode: t.mode,
    theme: t.theme,
    accent: t.accent ?? null,
    secondary: t.secondary ?? null,
    style: t.style ?? DEFAULT_STYLE,
  };
  if (t.density) {
    out.density = t.density;
  }
  if (typeof t.radius === "number") {
    out.radius = t.radius;
  }
  out.tokens = t.tokens ?? {};
  if (t.terminal) {
    out.terminal = t.terminal;
  }
  return JSON.stringify(out, null, 2);
}

/** Templates to start from. Each is complete enough to edit by hand. */
export const CODE_TEMPLATES: { id: string; label: string; theme: ThemePrefs }[] = [
  { id: "current", label: "Your live theme", theme: { mode: "system", theme: "nodal" } },
  ...THEMES.map((t) => ({ id: t.id, label: `${t.label} preset`, theme: { mode: "system", theme: t.id, style: DEFAULT_STYLE } as ThemePrefs })),
  {
    id: "high-contrast",
    label: "High contrast (example tokens)",
    theme: {
      mode: "dark",
      theme: "graphite",
      style: "boxy",
      tokens: { "--bg-canvas": "#000000", "--bg-surface": "#0b0b0b", "--fg-primary": "#ffffff", "--line": "#5a5a5a" },
    },
  },
  {
    id: "solarized",
    label: "Solarized terminal (example)",
    theme: {
      mode: "dark",
      theme: "ocean",
      style: "bubble",
      terminal: {
        background: "#002b36",
        foreground: "#839496",
        cursor: "#93a1a1",
        black: "#073642",
        red: "#dc322f",
        green: "#859900",
        yellow: "#b58900",
        blue: "#268bd2",
        magenta: "#d33682",
        cyan: "#2aa198",
        white: "#eee8d5",
      },
    },
  },
];
