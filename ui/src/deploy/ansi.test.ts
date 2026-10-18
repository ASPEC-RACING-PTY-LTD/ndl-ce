import { describe, expect, it } from "vitest";
import { ansiSpans, logLines } from "./ansi";

describe("logLines", () => {
  it("keeps the last state of a line rewritten with carriage returns", () => {
    expect(logLines("a\r\nprogress 10%\rprogress 90%\nend")).toEqual(["a", "progress 90%", "end"]);
  });
});

describe("ansiSpans", () => {
  it("styles bold and coloured text and drops other escapes", () => {
    const { spans } = ansiSpans("\x1b[1m$ git pull\x1b[0m \x1b[31merr\x1b[39m\x1b[2Kok");
    expect(spans).toEqual([
      { text: "$ git pull", bold: true },
      { text: " " },
      { text: "err", color: "red" },
      { text: "ok" },
    ]);
  });
  it("carries a style to the next line", () => {
    const first = ansiSpans("\x1b[32mgreen");
    expect(ansiSpans("still", first.end).spans[0]).toEqual({ text: "still", color: "green" });
  });
});
