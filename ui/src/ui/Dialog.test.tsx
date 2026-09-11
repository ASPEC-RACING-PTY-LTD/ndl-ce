import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dialog } from "./Dialog";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Dialog backdrop dismissal", () => {
  it("does not close when the pointer starts inside and releases on the backdrop", () => {
    const onClose = vi.fn();
    render(
      <Dialog open title="Token" onClose={onClose}>
        <p>Select this sentence for a copy.</p>
      </Dialog>,
    );
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    const panel = dialog.querySelector(".dialog-panel") as HTMLElement;
    fireEvent.pointerDown(panel);
    fireEvent.pointerUp(dialog);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("does not close while text is selected", () => {
    const onClose = vi.fn();
    render(
      <Dialog open title="Token" onClose={onClose}>
        <p>Highlight me</p>
      </Dialog>,
    );
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "Highlight" } as Selection);
    fireEvent.pointerDown(dialog);
    fireEvent.pointerUp(dialog);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("closes on an intentional backdrop press", () => {
    const onClose = vi.fn();
    render(
      <Dialog open title="Token" onClose={onClose}>
        <p>Body</p>
      </Dialog>,
    );
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "" } as Selection);
    fireEvent.pointerDown(dialog);
    fireEvent.pointerUp(dialog);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    render(
      <Dialog open title="Token" onClose={onClose}>
        <p>Body</p>
      </Dialog>,
    );
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    fireEvent.keyDown(dialog, { key: "Escape" });
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it("keeps a nested select usable without dismissing", () => {
    const onClose = vi.fn();
    render(
      <Dialog open title="Create token" onClose={onClose}>
        <label htmlFor="scope">Scope</label>
        <select id="scope" className="field-input">
          <option>Read-only debug</option>
        </select>
      </Dialog>,
    );
    fireEvent.mouseDown(screen.getByLabelText(/scope/i));
    fireEvent.change(screen.getByLabelText(/scope/i), { target: { value: "Read-only debug" } });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: /create token/i })).toBeVisible();
  });
});
