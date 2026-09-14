import { describe, expect, it } from "vitest";
import {
  isTermCopyChord,
  isTermInterruptChord,
  isTermPasteChord,
  normalizePasteText,
  pasteLineCount,
  shouldConfirmPaste,
} from "./paste";

describe("terminal paste helpers", () => {
  it("keeps comments, quotes, and blank lines", () => {
    const raw = "echo start\r\n# this is a comment\r\n\r\necho \"hello # world\"\n";
    expect(normalizePasteText(raw)).toBe('echo start\n# this is a comment\n\necho "hello # world"\n');
    expect(normalizePasteText(raw)).toContain("# this is a comment");
    expect(normalizePasteText(raw)).toContain('echo "hello # world"');
  });

  it("does not treat quoted hashes as a reason to rewrite the paste", () => {
    const text = 'echo "hello # world"\n# real comment\napt update';
    expect(normalizePasteText(text)).toBe(text);
    expect(shouldConfirmPaste(text)).toBe(true);
    expect(pasteLineCount(text)).toBe(3);
  });

  it("confirms only larger multiline pastes", () => {
    expect(shouldConfirmPaste("echo one")).toBe(false);
    expect(shouldConfirmPaste("echo one\necho two")).toBe(false);
    expect(shouldConfirmPaste("echo one\necho two\necho three")).toBe(true);
  });

  it("keeps Ctrl+Shift+C/V distinct from SIGINT", () => {
    expect(isTermCopyChord({ ctrlKey: true, shiftKey: true, key: "C" })).toBe(true);
    expect(isTermPasteChord({ ctrlKey: true, shiftKey: true, key: "V" })).toBe(true);
    expect(isTermInterruptChord({ ctrlKey: true, shiftKey: false, key: "c" })).toBe(true);
    expect(isTermInterruptChord({ ctrlKey: true, shiftKey: true, key: "c" })).toBe(false);
    expect(isTermCopyChord({ ctrlKey: true, shiftKey: false, key: "c" })).toBe(false);
  });
});
