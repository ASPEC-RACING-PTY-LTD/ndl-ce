import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../api/client", async (orig) => ({
  ...(await orig<typeof import("../api/client")>()),
  listInstanceTemplates: vi.fn(async () => ({ items: [] })),
  listTemplates: vi.fn(async () => ({ items: [] })),
  listWorkloads: vi.fn(async () => ({ items: [] })),
}));

vi.mock("../session", () => ({
  useSession: () => ({ status: "ready", user: { user_id: "u1", username: "u", roles: ["superadmin"], grants: [] } }),
}));

import { TemplatesPage } from "./TemplatesPage";

afterEach(() => {
  window.history.replaceState({}, "", "/");
});

describe("Templates page", () => {
  it("has no create buttons of its own", async () => {
    window.history.replaceState({}, "", "/templates");
    render(<TemplatesPage />);
    await waitFor(() => expect(screen.getAllByText("None yet.").length).toBeGreaterThan(0));
    expect(screen.queryByRole("button", { name: /new template|create vm template|create template/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /import a disk image/i })).toBeNull();
  });
});
