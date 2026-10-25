import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AppNotification } from "../../api/client";
import { ApprovalButtons, isApprovalAsk } from "./ApprovalButtons";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const ask: AppNotification = {
  id: "n1",
  kind: "backup.approval",
  title: "Back up new workload?",
  body: "deceasedcraft was created after your All workloads backup policies.",
  link: "/backups?approve=w1",
  created_at: "2026-10-26T00:00:00Z",
  read: false,
  read_at: null,
  subject_id: "w1",
  approval: "pending",
};

function stubDecision(status: number, body: unknown) {
  const calls: { url: string; body: string }[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), body: String(init?.body ?? "") });
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

describe("ApprovalButtons", () => {
  it("only asks while the workload is pending", () => {
    expect(isApprovalAsk(ask)).toBe(true);
    expect(isApprovalAsk({ ...ask, approval: "approved" })).toBe(false);
    expect(isApprovalAsk({ ...ask, kind: "update.impact" })).toBe(false);
  });

  it("approves from the tick and reports the new state", async () => {
    const calls = stubDecision(200, { workload_id: "w1", state: "approved" });
    const onDecided = vi.fn();
    render(<ApprovalButtons n={ask} onDecided={onDecided} />);
    fireEvent.click(screen.getByRole("button", { name: "Approve backup" }));
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith("n1", "approved"));
    expect(calls[0].url).toContain("/backups/approvals/w1");
    expect(JSON.parse(calls[0].body)).toEqual({ decision: "approve" });
  });

  it("denies from the cross, and takes someone else's decision on conflict", async () => {
    stubDecision(409, { error: "this workload was already approved", state: "approved" });
    const onDecided = vi.fn();
    render(<ApprovalButtons n={ask} onDecided={onDecided} />);
    fireEvent.click(screen.getByRole("button", { name: "Deny backup" }));
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith("n1", "approved"));
  });

  it("shows the outcome instead of buttons once decided", () => {
    render(<ApprovalButtons n={{ ...ask, approval: "denied" }} onDecided={() => undefined} />);
    expect(screen.getByText("Denied")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Approve backup" })).toBeNull();
  });
});
