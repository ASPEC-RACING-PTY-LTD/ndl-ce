import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  listAllDatabases: vi.fn(),
  bulkDatabases: vi.fn(),
  getGameDatabaseSchema: vi.fn(),
  getGameDatabaseTable: vi.fn(),
  runGameDatabaseQuery: vi.fn(),
  getGameDatabaseUsers: vi.fn(),
}));
vi.mock("../gameservers/api", async (orig) => ({ ...(await orig<typeof import("../gameservers/api")>()), ...api }));
vi.mock("../confirm", () => ({ confirmAction: () => true }));

import { DatabasesPage } from "./DatabasesPage";

beforeEach(() => {
  api.listAllDatabases.mockResolvedValue({
    items: [
      { server_id: "s1", server_name: "JvS", game: "gmod", database: "jvs", user: "game_ab", state: "running", size_bytes: 2048, tables: 3 },
      { server_id: "s2", server_name: "Dev", game: "gmod", database: "dev", user: "game_cd", state: "stopped", size_bytes: 0, tables: 0 },
    ],
  });
  api.bulkDatabases.mockResolvedValue({
    results: [{ server_id: "s1", server_name: "JvS", ok: true, query: { statements: [{ sql: "UPDATE t SET x=1", sets: [], affected: 4 }], error_index: -1, elapsed_ms: 3 } }],
  });
  api.getGameDatabaseSchema.mockResolvedValue({
    database: "jvs",
    server_database: "jvs",
    databases: ["information_schema", "jvs"],
    tables: [{ name: "players", engine: "InnoDB", rows: 2, data_bytes: 16384, index_bytes: 0 }],
  });
  api.getGameDatabaseTable.mockResolvedValue({
    database: "jvs",
    table: "players",
    columns: [
      { name: "id", type: "int(11)", nullable: false, key: "PRI", default: null },
      { name: "note", type: "text", nullable: true, default: null },
    ],
    rows: [
      ["1", null],
      ["2", "NULL"],
    ],
    total: 2,
    offset: 0,
    limit: 100,
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("DatabasesPage", () => {
  it("runs SQL in the selected databases and shows each result", async () => {
    render(<DatabasesPage />);
    expect(await screen.findByText("JvS")).toBeVisible();
    fireEvent.click(screen.getByLabelText("Select JvS"));
    fireEvent.change(screen.getByLabelText(/SQL to run in each selected database/i), { target: { value: "UPDATE t SET x=1" } });
    fireEvent.click(screen.getByRole("button", { name: /run in 1 database$/i }));
    await waitFor(() => expect(api.bulkDatabases).toHaveBeenCalledWith(["s1"], "query", "UPDATE t SET x=1"));
    expect(await screen.findByText(/4 rows affected/)).toBeVisible();
  });

  it("opens the full manager for a running database, telling NULL from the text NULL", async () => {
    render(<DatabasesPage />);
    const row = (await screen.findByText("JvS")).closest("tr") as HTMLElement;
    fireEvent.click(within(row).getByRole("button", { name: "Manage" }));
    fireEvent.click(await screen.findByRole("button", { name: "players" }));
    await screen.findByText("Rows 1 to 2 of 2");
    const cells = screen.getAllByText("NULL");
    // One real NULL (styled) and one text value.
    expect(cells.filter((c) => c.classList.contains("db-null"))).toHaveLength(1);
    expect(cells.filter((c) => !c.classList.contains("db-null"))).toHaveLength(1);
  });
});
