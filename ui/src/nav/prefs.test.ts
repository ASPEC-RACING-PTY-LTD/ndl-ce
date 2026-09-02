import { afterEach, describe, expect, it } from "vitest";
import {
  isMainNavPreferred,
  loadGroupState,
  loadLastView,
  preferMainNav,
  saveGroupState,
  saveLastView,
  stripMainNavOnLoad,
} from "./prefs";

afterEach(() => {
  window.history.replaceState({}, "", "/");
  localStorage.clear();
});

describe("nav prefs", () => {
  it("remembers last operational view and group collapse without secrets", () => {
    saveLastView("terminal");
    expect(loadLastView()).toBe("terminal");
    saveGroupState({ vm: false, host: true });
    expect(loadGroupState()).toEqual({ vm: false, host: true });
  });

  it("treats main-nav preference as an SPA override that refresh strips", () => {
    window.history.replaceState({ ndlNav: "main", keep: 1 }, "", "/workloads/wl-a");
    expect(isMainNavPreferred(window.history.state)).toBe(true);
    stripMainNavOnLoad();
    expect(isMainNavPreferred(window.history.state)).toBe(false);
    expect((window.history.state as { keep?: number }).keep).toBe(1);

    preferMainNav();
    expect(isMainNavPreferred(window.history.state)).toBe(true);
    expect(window.location.pathname).toBe("/workloads/wl-a");
  });
});
