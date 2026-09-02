const LANG: Record<string, string> = {
  txt: "plaintext",
  md: "markdown",
  markdown: "markdown",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  py: "python",
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "typescript",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "ini",
  ini: "ini",
  conf: "ini",
  cnf: "ini",
  env: "ini",
  xml: "xml",
  html: "html",
  htm: "html",
  css: "css",
  scss: "scss",
  go: "go",
  rs: "rust",
  c: "c",
  h: "c",
  cpp: "cpp",
  cc: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  java: "java",
  sql: "sql",
  dockerfile: "dockerfile",
};

export function languageFromName(name: string): string {
  const base = name.split("/").pop() ?? name;
  const lower = base.toLowerCase();
  if (lower === "dockerfile" || lower.startsWith("dockerfile.")) {
    return "dockerfile";
  }
  if (lower === "compose.yaml" || lower === "compose.yml" || lower === "docker-compose.yml" || lower === "docker-compose.yaml") {
    return "yaml";
  }
  const i = lower.lastIndexOf(".");
  if (i <= 0) {
    return "plaintext";
  }
  return LANG[lower.slice(i + 1)] ?? "plaintext";
}
