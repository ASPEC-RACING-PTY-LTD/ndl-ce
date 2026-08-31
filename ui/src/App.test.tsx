import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { App } from "./App";
import type { GetHealthPath } from "./generated/openapi";

describe("App", () => {
  it("shows the CI works page", () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: /no-dal community edition/i })).toBeVisible();
    expect(screen.getByText(/ci works/i)).toBeVisible();
  });

  it("does not render fake infrastructure data", () => {
    const { container } = render(<App />);
    const text = container.textContent ?? "";
    expect(text).not.toMatch(/192\.168\./);
    expect(text).not.toMatch(/workload/i);
    expect(text).not.toMatch(/\bvm\b/i);
    expect(text).not.toMatch(/\bnode\b/i);
    expect(text).not.toMatch(/storage/i);
    expect(text).not.toMatch(/backup/i);
    expect(text).not.toMatch(/cluster/i);
    expect(text).not.toMatch(/metric/i);
  });

  it("imports generated OpenAPI path types", () => {
    const path: GetHealthPath = "/api/v1/health";
    expect(path).toBe("/api/v1/health");
  });
});
