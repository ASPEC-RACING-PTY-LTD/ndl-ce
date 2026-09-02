export function joinPath(base: string, name: string): string {
  const n = name.replace(/^\/+/, "");
  if (!base || base === "/" || base === ".") {
    return n || "/";
  }
  return `${base.replace(/\/+$/, "")}/${n}`;
}

export function relName(name: string): string {
  const n = name.trim().replace(/\\/g, "/");
  if (!n) {
    throw new Error("Name is required");
  }
  const parts = n.split("/").filter((p) => p !== "");
  if (parts.length === 0 || parts.some((p) => p === "." || p === "..")) {
    throw new Error("Name must stay inside the current directory");
  }
  return n.replace(/^\/+/, "");
}

export function parentPath(p: string): string {
  const clean = p.replace(/\/+$/, "");
  const i = clean.lastIndexOf("/");
  if (i <= 0) {
    return "/";
  }
  return clean.slice(0, i) || "/";
}

export function breadcrumbs(p: string): { label: string; path: string }[] {
  const clean = (p || "/").replace(/\/+$/, "") || "/";
  const parts = clean.split("/").filter(Boolean);
  const out = [{ label: "/", path: "/" }];
  let cur = "";
  for (const part of parts) {
    cur += `/${part}`;
    out.push({ label: part, path: cur });
  }
  return out;
}

export function displayPath(p: string): string {
  if (!p || p === ".") {
    return "/";
  }
  return p.startsWith("/") ? p : `/${p}`;
}

const HOST_PREFIX = "/var/lib/ndl/";

export function uploadDirFromCwd(cwd: string, jailRoot = ""): { path: string; fallback: boolean } {
  const raw = (cwd || "").trim();
  if (!raw) {
    return { path: "/tmp/ndl-drop", fallback: true };
  }
  const jail = jailRoot.replace(/\/+$/, "");
  if (jail && jail !== "/" && !jail.startsWith("guest") && raw.startsWith(jail)) {
    const rel = raw.slice(jail.length) || "/";
    return { path: rel.startsWith("/") ? rel : `/${rel}`, fallback: false };
  }
  if (raw.startsWith(HOST_PREFIX) && !jail) {
    return { path: "/tmp/ndl-drop", fallback: true };
  }
  if (raw.startsWith("/")) {
    return { path: raw, fallback: false };
  }
  return { path: `/${raw}`, fallback: false };
}

export const EDITOR_MAX_BYTES = 1024 * 1024;
export const PREVIEW_MAX_BYTES = 2 * 1024 * 1024;
