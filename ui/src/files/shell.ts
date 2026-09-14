const META = new Set([" ", "\t", "\n", "\r", "'", '"', "\\", "$", "`", "!", "*", "?", "&", "|", ";", "(", ")", "<", ">", "[", "]", "{", "}", "~", "#"]);

export function shellEscape(value: string): string {
  if (value === "") {
    return "''";
  }
  let needs = false;
  for (const ch of value) {
    const code = ch.charCodeAt(0);
    if (code < 32 || code === 127 || META.has(ch)) {
      needs = true;
      break;
    }
  }
  if (!needs) {
    return value;
  }
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

export function shellEscapeAll(paths: string[]): string {
  return paths.map(shellEscape).join(" ");
}
