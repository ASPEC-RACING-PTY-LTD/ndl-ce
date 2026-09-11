import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BrandMark } from "./BrandMark";

afterEach(() => {
  cleanup();
});

describe("BrandMark", () => {
  it("uses the 72px auth mark with an accessible name", () => {
    render(<BrandMark size="auth" />);
    const img = screen.getByRole("img", { name: /^no-dal$/i }) as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/logo.png");
    expect(img.getAttribute("width")).toBe("72");
    expect(img.getAttribute("height")).toBe("72");
  });

  it("keeps the sidebar mark decorative inside a labelled parent", () => {
    render(<BrandMark size="sidebar" decorative />);
    const img = document.querySelector("img.brand-logo-sidebar") as HTMLImageElement;
    expect(img).toBeTruthy();
    expect(img.alt).toBe("");
    expect(img.getAttribute("width")).toBe("28");
    expect(img.getAttribute("height")).toBe("28");
  });
});
