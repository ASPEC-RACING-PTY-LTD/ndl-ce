import { describe, expect, it } from "vitest";
import type { UptimeMonitor } from "../api/client";
import type { DockerTunnel } from "../generated/openapi";
import { discoverMonitors } from "./discover";

const tunnels: DockerTunnel[] = [
  {
    machine_id: "ct-1",
    machine_name: "apps",
    ingress: [
      { hostname: "Shop.Example.com", service: "http://localhost:3000" },
      { hostname: "admin.example.com", service: "http://localhost:3001" },
      { hostname: "deploy.example.com", service: "ssh://localhost:2222" },
      { hostname: "*.example.com", service: "http://localhost:3000" },
      { service: "http_status:404" },
    ],
    links: [{ origin: "http://localhost:3000", name: "shop-web-1" }],
  },
  {
    machine_id: "ct-2",
    machine_name: "apps-b",
    ingress: [{ hostname: "shop.example.com", service: "http://localhost:3000" }],
    links: [{ origin: "http://localhost:3000", name: "shop-web-1" }],
  },
];

describe("discoverMonitors", () => {
  it("proposes one monitor per public web hostname with its containers", () => {
    const got = discoverMonitors(tunnels, []);
    expect(got.map((c) => c.hostname)).toEqual(["admin.example.com", "shop.example.com"]);
    const shop = got.find((c) => c.hostname === "shop.example.com")!;
    expect(shop.monitor).toMatchObject({ name: "shop.example.com", target: "https://shop.example.com", auto_restart: true, enabled: true });
    expect(shop.monitor.containers).toEqual([
      { machine: "apps", name: "shop-web-1" },
      { machine: "apps-b", name: "shop-web-1" },
    ]);
    expect(got.find((c) => c.hostname === "admin.example.com")!.monitor.containers).toEqual([]);
  });

  it("marks hostnames an existing monitor already watches", () => {
    const existing = [{ name: "Shop", target: "shop.example.com/health" }] as UptimeMonitor[];
    const got = discoverMonitors(tunnels, existing);
    expect(got.find((c) => c.hostname === "shop.example.com")!.existing).toBe("Shop");
    expect(got[got.length - 1].hostname).toBe("shop.example.com");
    expect(got.find((c) => c.hostname === "admin.example.com")!.existing).toBeUndefined();
  });
});
