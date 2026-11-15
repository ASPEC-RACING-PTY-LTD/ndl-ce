import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const putGameStartup = vi.fn().mockResolvedValue({});
const putGameConfig = vi.fn().mockResolvedValue({});
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
  getGameConfig: vi.fn().mockResolvedValue({
    settings: [
      { id: "map", label: "Start map", kind: "text", env: "MAP" },
      { id: "sv_password", label: "Join password", kind: "password" },
      { id: "sv_region", label: "Region", kind: "number" },
    ],
    values: { map: "rp_ag_jvs", sv_password: "", sv_region: "255" },
  }),
  putGameConfig: (...args: unknown[]) => putGameConfig(...args),
}));

import { ConfigPane, StartupPane } from "./GameServerDetailPage";

afterEach(cleanup);

describe("StartupPane", () => {
  // Settings saved elsewhere (the Config tab) after this tab loaded must not
  // be put back to the values it loaded.
  it("saves only what changed here", async () => {
    render(<StartupPane id="gs-1" />);
    const name = await screen.findByDisplayValue("Dev");
    // Map is edited under Server settings, so Startup does not repeat it.
    await waitFor(() => expect(screen.queryByDisplayValue("gm_flatgrass")).toBeNull());
    expect(screen.getByDisplayValue("sandbox")).toBeInTheDocument();
    fireEvent.change(name, { target: { value: "Dev 2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save startup" }));
    await waitFor(() => expect(putGameStartup).toHaveBeenCalledTimes(1));
    expect(putGameStartup).toHaveBeenCalledWith("gs-1", { env: { SERVER_NAME: "Dev 2" } });
  });
});

describe("ConfigPane", () => {
  it("saves only what changed here and masks passwords", async () => {
    render(<ConfigPane id="gs-1" />);
    const region = await screen.findByDisplayValue("255");
    fireEvent.change(region, { target: { value: "5" } });
    expect(screen.getByLabelText("Join password")).toHaveAttribute("type", "password");
    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
    await waitFor(() => expect(putGameConfig).toHaveBeenCalledTimes(1));
    expect(putGameConfig).toHaveBeenCalledWith("gs-1", { sv_region: "5" });
  });
});
