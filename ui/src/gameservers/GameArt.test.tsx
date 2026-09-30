import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { GameArt } from "./CatalogueBrowser";

afterEach(cleanup);

describe("GameArt", () => {
  it("shows the image, then a monogram when the image cannot load", () => {
    const { container } = render(<GameArt url="https://cdn.cloudflare.steamstatic.com/steam/apps/252490/header.jpg" kind="banner" title="Rust" size="tile" />);
    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    expect(img).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(container.firstElementChild).toHaveClass("is-banner");
    fireEvent.error(img as HTMLImageElement);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("RU");
    expect(container.firstElementChild).toHaveClass("is-mono");
  });

  it("uses a monogram when a game has no artwork", () => {
    const { container } = render(<GameArt title="Minecraft: Java Edition" size="tile" />);
    expect(container.textContent).toBe("MJ");
  });
});
