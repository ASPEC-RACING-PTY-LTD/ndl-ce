import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { confirmAction, setAutoConfirm } from "./confirm";

afterEach(() => {
  setAutoConfirm(false);
  cleanup();
  vi.restoreAllMocks();
});

describe("SuperAdmin confirmation bypass", () => {
  it("asks everyone else and answers yes for a SuperAdmin", () => {
    const ask = vi.spyOn(window, "confirm").mockReturnValue(false);
    expect(confirmAction("Delete?")).toBe(false);
    expect(ask).toHaveBeenCalledTimes(1);
    setAutoConfirm(true);
    expect(confirmAction("Delete?")).toBe(true);
    expect(ask).toHaveBeenCalledTimes(1);
  });

  it("confirms a plain dialog for a SuperAdmin, but not one with something to fill in", () => {
    setAutoConfirm(true);
    const plain = vi.fn();
    render(
      <ConfirmDialog open title="Wipe" onConfirm={plain} onClose={() => {}}>
        <p>Wipe everything?</p>
      </ConfirmDialog>,
    );
    expect(plain).toHaveBeenCalledTimes(1);
    const form = vi.fn();
    render(
      <ConfirmDialog open title="Rotate" onConfirm={form} onClose={() => {}}>
        <input aria-label="Your password" />
      </ConfirmDialog>,
    );
    expect(form).not.toHaveBeenCalled();
  });

  it("does nothing for others", () => {
    const plain = vi.fn();
    render(
      <ConfirmDialog open title="Wipe" onConfirm={plain} onClose={() => {}}>
        <p>Wipe everything?</p>
      </ConfirmDialog>,
    );
    expect(plain).not.toHaveBeenCalled();
  });
});
