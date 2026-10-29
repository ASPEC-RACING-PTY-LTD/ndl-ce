import { describe, expect, it } from "vitest";
import { canEditNodeNicknames, nodeLabel, setNodeNicknames, withNickname } from "./nodeNames";

describe("node nicknames", () => {
  it("show the nickname in place of the node name and keep the real one", () => {
    setNodeNicknames({ n1: "Home lab" }, true);
    const shown = withNickname({ id: "n1", name: "ndl-ce" });
    expect(shown).toMatchObject({ name: "Home lab", real_name: "ndl-ce" });
    // Applying twice keeps the node's own name, not the nickname.
    expect(withNickname(shown)).toMatchObject({ name: "Home lab", real_name: "ndl-ce" });
    expect(canEditNodeNicknames()).toBe(true);
  });

  it("label nodes known by ID or by their own name", () => {
    setNodeNicknames({ n1: "Home lab" });
    withNickname({ id: "n1", name: "ndl-ce" });
    expect(nodeLabel("n1")).toBe("Home lab");
    expect(nodeLabel(undefined, "ndl-ce")).toBe("Home lab");
    expect(nodeLabel("n2", "vps")).toBe("vps");
    expect(nodeLabel("n3")).toBe("n3");
  });

  it("go back to the node name when the nickname is cleared", () => {
    setNodeNicknames({}, false);
    expect(withNickname({ id: "n1", name: "Home lab", real_name: "ndl-ce" })).toEqual({ id: "n1", name: "ndl-ce" });
    expect(canEditNodeNicknames()).toBe(false);
  });
});
