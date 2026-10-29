import { describe, expect, it } from "vitest";
import { networkRoute } from "./NetworkHubPage";
import { hostNICs } from "./NetworkPage";

describe("network area", () => {
  it("opens Edge forwarding, DDoS Protection and Cluster from their old addresses", () => {
    expect(networkRoute("/network")).toEqual({ tab: "network" });
    expect(networkRoute("/network/edge")).toEqual({ tab: "network", section: "edge" });
    expect(networkRoute("/network/ddos")).toEqual({ tab: "network", section: "ddos" });
    expect(networkRoute("/settings/cluster")).toEqual({ tab: "cluster" });
    expect(networkRoute("/network/cluster")).toEqual({ tab: "cluster" });
    expect(networkRoute("/network/cloudflare")).toEqual({ tab: "cloudflare" });
    expect(networkRoute("/network/nope")).toBeNull();
    expect(networkRoute("/storage")).toBeNull();
  });

  it("lists only the host's own interfaces", () => {
    const names = hostNICs([
      { name: "lo", kind: "loopback", addresses: ["127.0.0.1/8"] },
      { name: "enp6s0", kind: "physical" },
      { name: "ndl685ff937", kind: "virtual", addresses: ["192.168.2.82/24"] },
      { name: "ndle2be8fdee", kind: "virtual", addresses: ["10.66.0.6/30"] },
      { name: "docker0", kind: "virtual", addresses: ["172.17.0.1/16"] },
      { name: "br-9dc9a5dfa882", kind: "virtual", addresses: ["172.18.0.1/16"] },
      { name: "veth365f9e8", kind: "virtual" },
      { name: "vnet3", kind: "virtual" },
      { name: "ndlb0a1", kind: "virtual" },
    ]).map((n) => n.name);
    expect(names).toEqual(["enp6s0", "ndl685ff937", "ndle2be8fdee"]);
  });
});
