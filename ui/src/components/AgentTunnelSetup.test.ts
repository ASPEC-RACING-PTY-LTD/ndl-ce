import { describe, expect, it } from "vitest";
import { joinCommand } from "../pages/ClusterPage";
import { tunnelHost } from "./AgentTunnelSetup";

describe("agent tunnels", () => {
  it("read the hostname of a Cloudflare agent address", () => {
    expect(tunnelHost("cloudflare:agent.example.com")).toBe("agent.example.com");
    expect(tunnelHost("Cloudflare: agent.example.com ")).toBe("agent.example.com");
    expect(tunnelHost("203.0.113.10:9444")).toBeNull();
    expect(tunnelHost(undefined)).toBeNull();
  });

  it("advertise the tunnel address when joining", () => {
    expect(joinCommand("https://panel.example.com", "tok", undefined, "cloudflare:agent.example.com")).toContain("--advertise cloudflare:agent.example.com");
  });
});
