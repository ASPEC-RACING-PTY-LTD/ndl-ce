import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const putGameStartup = vi.fn().mockResolvedValue({});
vi.mock("../gameservers/api", async (orig) => ({
  ...(await orig<typeof import("../gameservers/api")>()),
  getGameStartup: vi.fn().mockResolvedValue({
    startup: "./srcds_run",
    variables: [
      { env: "MAP", name: "Map", value: "gm_flatgrass" },
      { env: "GAMEMODE", name: "Gamemode", value: "sandbox" },
      { env: "SERVER_NAME", name: "Server name", value: "Dev" },
    ],
  }),
  putGameStartup: (...args: unknown[]) => putGameStartup(...args),
}));

import { StartupPane } from "./GameServerDetailPage";

afterEach(cleanup);

describe("StartupPane", () => {
  // Settings saved elsewhere (the Config tab) after this tab loaded must not
  // be put back to the values it loaded.
  it("saves only what changed here", async () => {
    render(<StartupPane id="gs-1" />);
    const name = await screen.findByDisplayValue("Dev");
    fireEvent.change(name, { target: { value: "Dev 2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save startup" }));
    await waitFor(() => expect(putGameStartup).toHaveBeenCalledTimes(1));
    expect(putGameStartup).toHaveBeenCalledWith("gs-1", { env: { SERVER_NAME: "Dev 2" } });
  });
});
