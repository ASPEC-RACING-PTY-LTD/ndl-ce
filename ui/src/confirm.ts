/**
 * SuperAdmins are trusted to mean what they click: while one is signed in,
 * confirmation prompts answer yes on their own. Everyone else is asked.
 * Prompts that collect something (a password, a name, a location) still
 * open; only plain "are you sure" questions are skipped.
 */
let autoConfirm = false;

export function setAutoConfirm(on: boolean) {
  autoConfirm = on;
}

export function autoConfirms(): boolean {
  return autoConfirm;
}

/** confirmAction replaces window.confirm. */
export function confirmAction(message: string): boolean {
  return autoConfirm || window.confirm(message);
}
