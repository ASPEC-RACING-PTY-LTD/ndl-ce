import { useEffect, useState } from "react";
import { stripMainNavOnLoad } from "./nav/prefs";

export function normalizePath(pathname: string): string {
  if (pathname.length > 1 && pathname.endsWith("/")) {
    return pathname.replace(/\/+$/, "");
  }
  return pathname;
}

export function currentPath(): string {
  return normalizePath(window.location.pathname);
}

export function navigate(to: string, options?: { replace?: boolean; state?: unknown }): void {
  const next = normalizePath(to);
  const state = options?.state === undefined ? {} : options.state;
  if (options?.replace) {
    window.history.replaceState(state, "", next);
  } else {
    window.history.pushState(state, "", next);
  }
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function usePath(): string {
  const [path, setPath] = useState(currentPath);

  useEffect(() => {
    const onPop = () => {
      setPath(currentPath());
    };
    window.addEventListener("popstate", onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
    };
  }, []);

  return path;
}

export function useHistoryState(): unknown {
  const [state, setState] = useState(() => {
    stripMainNavOnLoad();
    return window.history.state;
  });

  useEffect(() => {
    const onPop = () => {
      setState(window.history.state);
    };
    window.addEventListener("popstate", onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
    };
  }, []);

  return state;
}
