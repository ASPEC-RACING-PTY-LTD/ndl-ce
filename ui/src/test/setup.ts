import "@testing-library/jest-dom/vitest";

if (typeof globalThis.EventSource === "undefined") {
  globalThis.EventSource = class {
    onmessage: ((ev: MessageEvent) => void) | null = null;
    close(): void {}
    constructor() {}
  } as unknown as typeof EventSource;
}

function ensureLocalStorage(): void {
  try {
    if (typeof globalThis.localStorage?.getItem === "function") {
      return;
    }
  } catch {
    // Replace a broken Storage implementation.
  }
  const data = new Map<string, string>();
  const storage: Storage = {
    get length() {
      return data.size;
    },
    clear() {
      data.clear();
    },
    getItem(key) {
      return data.has(key) ? (data.get(key) ?? null) : null;
    },
    key(index) {
      return [...data.keys()][index] ?? null;
    },
    removeItem(key) {
      data.delete(key);
    },
    setItem(key, value) {
      data.set(String(key), String(value));
    },
  };
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: storage,
  });
  if (typeof window !== "undefined") {
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: storage,
    });
  }
}

ensureLocalStorage();
