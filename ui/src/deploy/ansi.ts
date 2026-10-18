/** One run of log text with its style. */
export type AnsiSpan = { text: string; bold?: boolean; dim?: boolean; color?: string };

const COLORS = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"];

// Every CSI sequence; only SGR (ending in m) changes the style.
// eslint-disable-next-line no-control-regex -- escape sequences are the point
const CSI = /\x1b\[([0-9;?]*)([A-Za-z])/g;

/**
 * logLines turns terminal output into lines. A carriage return without a
 * newline rewrites its line, as progress bars do, so only the last state of
 * such a line is kept.
 */
export function logLines(raw: string): string[] {
  const lines = raw.split("\n");
  return lines.map((line) => {
    const trimmed = line.endsWith("\r") ? line.slice(0, -1) : line;
    const i = trimmed.lastIndexOf("\r");
    return i >= 0 ? trimmed.slice(i + 1) : trimmed;
  });
}

/** ansiSpans splits one line into styled spans; other escapes are dropped. */
export function ansiSpans(line: string, start: Omit<AnsiSpan, "text"> = {}): { spans: AnsiSpan[]; end: Omit<AnsiSpan, "text"> } {
  const spans: AnsiSpan[] = [];
  let style = { ...start };
  let last = 0;
  const push = (text: string) => {
    if (text) {
      spans.push({ text, ...style });
    }
  };
  for (const m of line.matchAll(CSI)) {
    push(line.slice(last, m.index));
    last = (m.index ?? 0) + m[0].length;
    if (m[2] !== "m") {
      continue;
    }
    const codes = m[1] === "" ? [0] : m[1].split(";").map((c) => Number(c));
    for (const c of codes) {
      if (c === 0) {
        style = {};
      } else if (c === 1) {
        style.bold = true;
      } else if (c === 2) {
        style.dim = true;
      } else if (c === 22) {
        style.bold = false;
        style.dim = false;
      } else if (c >= 30 && c <= 37) {
        style.color = COLORS[c - 30];
      } else if (c >= 90 && c <= 97) {
        style.color = "bright-" + COLORS[c - 90];
      } else if (c === 39) {
        delete style.color;
      }
    }
  }
  // eslint-disable-next-line no-control-regex -- drop stray escapes
  push(line.slice(last).replace(/\x1b./g, ""));
  return { spans, end: style };
}
