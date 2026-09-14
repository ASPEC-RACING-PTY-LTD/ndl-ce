export const PASTE_CONFIRM_LINES = 3;

export function normalizePasteText(text: string): string {
  return text.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

export function pasteLineCount(text: string): number {
  return normalizePasteText(text).split("\n").length;
}

export function shouldConfirmPaste(text: string): boolean {
  return pasteLineCount(text) >= PASTE_CONFIRM_LINES;
}

export function isTermCopyChord(ev: { ctrlKey: boolean; shiftKey: boolean; altKey?: boolean; metaKey?: boolean; key: string }): boolean {
  return ev.ctrlKey && ev.shiftKey && !ev.altKey && !ev.metaKey && ev.key.toLowerCase() === "c";
}

export function isTermPasteChord(ev: { ctrlKey: boolean; shiftKey: boolean; altKey?: boolean; metaKey?: boolean; key: string }): boolean {
  return ev.ctrlKey && ev.shiftKey && !ev.altKey && !ev.metaKey && ev.key.toLowerCase() === "v";
}

export function isTermInterruptChord(ev: { ctrlKey: boolean; shiftKey: boolean; altKey?: boolean; metaKey?: boolean; key: string }): boolean {
  return ev.ctrlKey && !ev.shiftKey && !ev.altKey && !ev.metaKey && ev.key.toLowerCase() === "c";
}
