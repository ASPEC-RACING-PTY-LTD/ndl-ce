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

if (typeof HTMLDialogElement !== "undefined") {
  const proto = HTMLDialogElement.prototype;
  if (typeof proto.showModal !== "function") {
    proto.showModal = function showModal() {
      this.setAttribute("open", "");
    };
  }
  if (typeof proto.close !== "function") {
    proto.close = function close() {
      this.removeAttribute("open");
    };
  }
}
