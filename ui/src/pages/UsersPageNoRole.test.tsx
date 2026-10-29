import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { UsersPage } from "./UsersPage";

vi.mock("../session", () => ({
  useSession: () => ({
    status: "ready",
    user: { user_id: "me", username: "admin", roles: ["superadmin"], grants: ["users.read", "users.create", "users.update", "users.roles.manage"] },
  }),
}));

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Users with no role", () => {
  it("shows No role and creates a user with role none", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const path = new URL(url, "http://localhost").pathname;
      if (path === "/api/v1/users" && (init?.method ?? "GET") === "GET") {
        return json(200, { items: [{ id: "u2", username: "guest", roles: [], status: "active" }], total: 1 });
      }
      if (path === "/api/v1/users") {
        return json(201, { id: "u3", username: "bob", roles: [], status: "active" });
      }
      return json(404, { error: "unmocked" });
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<UsersPage />);
    const table = await screen.findByRole("table");
    expect(within(table).getByText("No role")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Create user" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Username"), { target: { value: "bob" } });
    fireEvent.change(within(dialog).getByLabelText("Password"), { target: { value: "longpassword" } });
    fireEvent.change(within(dialog).getByLabelText("Role"), { target: { value: "none" } });
    expect(within(dialog).getByRole("option", { name: "No role (item access only)" })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Create" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(true));
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(JSON.parse(String(post?.[1]?.body))).toMatchObject({ username: "bob", role: "none" });
  });
});
