import { describe, expect, it } from "vitest";
import type { Network } from "../../api/phase4";
import { preferredGuestNetwork, sortGuestNetworks } from "./NetworkPicker";

const lan: Network = { id: "lan", name: "lan", kind: "lan-bridge", status: "available", danger: "dangerous" };
const iso: Network = { id: "iso", name: "cert-ce10-phys-4-net", kind: "isolated-nat", status: "available", danger: "safe" };
const iso2: Network = { id: "iso2", name: "aa-isolated", kind: "isolated-nat", status: "available", danger: "safe" };

describe("preferredGuestNetwork", () => {
  it("does not default a new guest onto the LAN when an isolated network exists", () => {
    const pick = preferredGuestNetwork([lan, iso]);
    expect(pick?.id).toBe("iso");
  });

  it("falls back to the only usable network if every option is the LAN", () => {
    expect(preferredGuestNetwork([lan])?.id).toBe("lan");
  });
});

describe("sortGuestNetworks", () => {
  it("lists isolated networks before the LAN", () => {
    expect(sortGuestNetworks([lan, iso, iso2]).map((n) => n.id)).toEqual(["iso2", "iso", "lan"]);
  });
});
