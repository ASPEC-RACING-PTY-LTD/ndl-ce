import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { explainError } from "../errors";
import { ErrorNotice } from "./ErrorNotice";

describe("explainError", () => {
  it("turns raw host errors into plain messages and keeps the raw text", () => {
    const fd = explainError("failed_precondition: open /dev/null: too many open files");
    expect(fd.summary).toMatch(/ran out of open-file slots/i);
    expect(fd.detail).toContain("too many open files");
    expect(explainError("The operation failed. fork/exec /usr/bin/docker: no such file or directory").summary).toMatch(
      /docker is not installed/i,
    );
    expect(explainError("not authenticated").summary).toMatch(/session has expired/i);
    expect(explainError(new Error("rpc error: code = Unavailable desc = backup target is unavailable")).summary).toBe(
      "Backup target is unavailable.",
    );
    expect(explainError("").summary).toBe("Something went wrong.");
  });
});

describe("ErrorNotice", () => {
  afterEach(cleanup);

  it("shows the plain message and expands the technical details on demand", () => {
    render(<ErrorNotice title="Starting failed" error="failed_precondition: open /dev/null: too many open files" />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/starting failed: the host ran out of open-file slots/i);
    const raw = screen.getByText(/open \/dev\/null: too many open files/);
    expect(raw).not.toBeVisible();
    fireEvent.click(screen.getByText(/technical details/i));
    expect(raw.closest("details")).toHaveAttribute("open");
  });

  it("does not offer details that repeat the message", () => {
    render(<ErrorNotice error="Pool is full." />);
    expect(screen.queryByText(/technical details/i)).toBeNull();
  });
});
