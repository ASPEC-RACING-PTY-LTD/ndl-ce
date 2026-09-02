function store(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

export function storageGet(key: string): string | null {
  return store()?.getItem(key) ?? null;
}

export function storageSet(key: string, value: string): void {
  try {
    store()?.setItem(key, value);
  } catch {
    // Ignore quota or private-mode failures.
  }
}
