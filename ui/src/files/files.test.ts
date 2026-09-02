import { describe, expect, it } from "vitest";
import { breadcrumbs, joinPath, parentPath, relName, uploadDirFromCwd } from "./paths";
import { languageFromName } from "./language";
import { shellEscape, shellEscapeAll } from "./shell";

describe("file paths", () => {
  it("joins and parents", () => {
    expect(joinPath("/", "etc")).toBe("etc");
    expect(joinPath("/var", "log")).toBe("/var/log");
    expect(parentPath("/var/log")).toBe("/var");
    expect(parentPath("etc")).toBe("/");
  });

  it("refuses empty and parent-directory names", () => {
    expect(() => relName("")).toThrow(/required/i);
    expect(() => relName("..")).toThrow(/inside/i);
    expect(() => relName("foo/../etc")).toThrow(/inside/i);
    expect(relName("readme.txt")).toBe("readme.txt");
    expect(relName("foo/bar")).toBe("foo/bar");
  });

  it("builds breadcrumbs", () => {
    expect(breadcrumbs("/etc/ndl")).toEqual([
      { label: "/", path: "/" },
      { label: "etc", path: "/etc" },
      { label: "ndl", path: "/etc/ndl" },
    ]);
  });

  it("maps host-visible cwd onto the jail", () => {
    const jail = "/var/lib/ndl/storage/p/volumes/ct";
    expect(uploadDirFromCwd(`${jail}/root`, jail)).toEqual({ path: "/root", fallback: false });
    expect(uploadDirFromCwd("", jail)).toEqual({ path: "/tmp/ndl-drop", fallback: true });
  });
});

describe("languageFromName", () => {
  it("recognizes common extensions and compose yaml", () => {
    expect(languageFromName("a.py")).toBe("python");
    expect(languageFromName("a.js")).toBe("javascript");
    expect(languageFromName("a.tsx")).toBe("typescript");
    expect(languageFromName("a.go")).toBe("go");
    expect(languageFromName("a.rs")).toBe("rust");
    expect(languageFromName("compose.yaml")).toBe("yaml");
    expect(languageFromName("docker-compose.yml")).toBe("yaml");
    expect(languageFromName("Dockerfile")).toBe("dockerfile");
    expect(languageFromName("weird")).toBe("plaintext");
  });
});

describe("shellEscape", () => {
  it("quotes spaces quotes and metacharacters", () => {
    expect(shellEscape("plain")).toBe("plain");
    expect(shellEscape("file name.txt")).toBe("'file name.txt'");
    expect(shellEscape("it's")).toBe("'it'\\''s'");
    expect(shellEscape("a$(rm)")).toBe("'a$(rm)'");
    expect(shellEscapeAll(["/tmp/a b", `/tmp/x"y`])).toBe(`'/tmp/a b' '/tmp/x"y'`);
  });
});
