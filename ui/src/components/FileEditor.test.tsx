import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FileEditor } from "./FileEditor";
import type { FileContent } from "../api/phase6";

function installXHR(status: number, body: unknown) {
  class FakeXHR {
    status = status;
    responseText = JSON.stringify(body);
    withCredentials = false;
    upload = { onprogress: null as ((ev: ProgressEvent) => void) | null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    open() {}
    send() {
      queueMicrotask(() => this.onload?.());
    }
    abort() {
      this.onabort?.();
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
}

const textFile: FileContent = {
  name: "notes.txt",
  type: "file",
  size: 2,
  path: "notes.txt",
  content: "hi",
  editable: true,
  binary: false,
  mtime: "2026-09-01T00:00:00Z",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("FileEditor", () => {
  it("refuses binary files and offers download", () => {
    render(
      <FileEditor
        kind="workload"
        id="wl-1"
        dir="/"
        file={{ ...textFile, name: "blob.bin", binary: true, editable: false, content: undefined }}
        canWrite
        onClose={() => {}}
        onSaved={() => {}}
        onError={() => {}}
      />,
    );
    expect(screen.getByText(/looks binary/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /download/i })).toBeVisible();
    expect(screen.queryByTestId("monaco")).not.toBeInTheDocument();
  });

  it("rejects huge files instead of opening the editor", () => {
    render(
      <FileEditor
        kind="workload"
        id="wl-1"
        dir="/"
        file={{ ...textFile, too_large: true, editable: false, content: undefined, size: 3 * 1024 * 1024 }}
        canWrite
        onClose={() => {}}
        onSaved={() => {}}
        onError={() => {}}
      />,
    );
    expect(screen.getByText(/too large/i)).toBeVisible();
    expect(screen.getByRole("button", { name: /download/i })).toBeVisible();
    expect(screen.queryByTestId("monaco")).not.toBeInTheDocument();
  });

  it("marks dirty state and saves", async () => {
    installXHR(201, { ok: true });
    const saved: string[] = [];
    render(
      <FileEditor
        kind="workload"
        id="wl-1"
        dir="/"
        file={textFile}
        canWrite
        onClose={() => {}}
        onSaved={(path) => saved.push(path)}
        onError={() => {}}
      />,
    );
    fireEvent.change(screen.getByTestId("monaco"), { target: { value: "hello" } });
    expect(screen.getByText("notes.txt *")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saved).toEqual(["notes.txt"]));
  });

  it("warns when the file changed on disk", async () => {
    installXHR(409, { error: "file changed on disk" });
    render(
      <FileEditor
        kind="workload"
        id="wl-1"
        dir="/"
        file={textFile}
        canWrite
        onClose={() => {}}
        onSaved={() => {}}
        onError={() => {}}
      />,
    );
    fireEvent.change(screen.getByTestId("monaco"), { target: { value: "new" } });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    expect(await screen.findByRole("heading", { name: /file changed on disk/i })).toBeVisible();
    expect(screen.getByRole("button", { name: /^overwrite$/i })).toBeVisible();
  });

  it("is read-only when writes are unavailable", () => {
    render(
      <FileEditor
        kind="workload"
        id="wl-1"
        dir="/"
        file={{ ...textFile, editable: true }}
        canWrite={false}
        onClose={() => {}}
        onSaved={() => {}}
        onError={() => {}}
      />,
    );
    expect(screen.getByText(/read-only/i)).toBeVisible();
    expect(screen.queryByRole("button", { name: /^save$/i })).not.toBeInTheDocument();
    expect(screen.getByTestId("monaco")).toHaveAttribute("readonly");
  });
});
