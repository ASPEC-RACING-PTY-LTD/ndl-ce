import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../api/client", async (orig) => ({
  ...(await orig<typeof import("../api/client")>()),
  // An idle host as 1.5.0 reported it: lists it had nothing in came back null.
  getLiveUpdates: vi.fn().mockResolvedValue({
    enabled: true,
    current: "",
    queued: null,
    history: [],
    trusted_keys: ["c4fa52c6ce254ce8"],
    busy: null,
    applied_migrations: null,
    package_version: "1.5.0",
    version: "1.5.0",
    build: "1.5.0",
  }),
}));

import { LiveUpdatesPanel } from "./LiveUpdates";

afterEach(cleanup);

describe("LiveUpdatesPanel", () => {
  it("renders an idle host whose lists are null", async () => {
    render(<LiveUpdatesPanel owner />);
    expect(await screen.findByText("Nothing running")).toBeTruthy();
    expect(screen.getByText("On")).toBeTruthy();
  });
});
