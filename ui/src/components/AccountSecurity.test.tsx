import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { UploadDialog } from "./UploadDialog";
import { UserDetailDialog } from "./UserDetailDialog";
import type { ManagedUser } from "../api/client";

vi.mock("../session", () => ({
  useSession: () => ({
    status: "ready",
    user: { user_id: "other", username: "second", roles: ["admin"], grants: ["users.update", "users.delete", "users.roles.manage"] },
  }),
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("UploadDialog", () => {
  it("takes dropped files and uploads each one", async () => {
    const upload = vi.fn(async () => undefined);
    const done = vi.fn();
    render(<UploadDialog open destination="/root" onClose={() => undefined} upload={upload} onDone={done} />);
    const zone = screen.getByRole("button", { name: /click here to upload or drag and drop files/i });
    const file = new File(["hi"], "notes.txt", { type: "text/plain" });
    fireEvent.drop(zone, { dataTransfer: { files: [file] } });
    expect(await screen.findByText("notes.txt")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^upload 1$/i }));
    await waitFor(() => expect(upload).toHaveBeenCalledWith(file));
    expect(await screen.findByText(/^uploaded$/i)).toBeVisible();
    expect(done).toHaveBeenCalled();
  });
});

describe("UserDetailDialog", () => {
  it("locks the root account for another admin", () => {
    const root: ManagedUser = { id: "root-1", username: "owner", roles: ["admin"], status: "active", is_root: true };
    render(<UserDetailDialog user={root} onClose={() => undefined} onChanged={() => undefined} />);
    expect(screen.getByText(/this is the root account/i)).toBeVisible();
    expect(screen.getByLabelText(/^username$/i)).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save profile/i })).not.toBeInTheDocument();
  });
});
