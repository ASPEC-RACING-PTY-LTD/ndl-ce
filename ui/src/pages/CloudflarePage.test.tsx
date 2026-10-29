import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DISMISS_FOR_MS, DISMISS_PREF, PERMANENT, isDismissed, withDismissal, withoutDismissals } from "../cloudflare/dismiss";
import { getPref, resetPrefsForTest, setPref } from "../prefs";
import { SessionProvider } from "../session";
import { CloudflarePage } from "./CloudflarePage";

const me = { user_id: "user-1", username: "admin", roles: ["superadmin"], edition: "ce", ux_level: "guided", expert_ack: false };

type Route = { status: number; body?: unknown } | ((init?: RequestInit, url?: URL) => { status: number; body?: unknown });

function mockApi(routes: Record<string, Route>) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push(`${method} ${url.pathname}${url.search}`);
    const hit = routes[`${method} ${url.pathname}`] ?? routes[url.pathname];
    if (!hit) {
      return new Response(JSON.stringify({ error: `unmocked ${url.pathname}` }), { status: 404, headers: { "Content-Type": "application/json" } });
    }
    const res = typeof hit === "function" ? hit(init, url) : hit;
    return new Response(JSON.stringify(res.body ?? {}), { status: res.status, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  resetPrefsForTest();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const T1 = "11111111-1111-4111-8111-111111111111";
const T2 = "22222222-2222-4222-8222-222222222222";
const A = "a".repeat(32);
const B = "b".repeat(32);

function route(host: string, account: string, accountName: string, tunnel: string, tunnelName: string, dns: string) {
  return {
    key: `${tunnel}|${host}|`,
    hostname: host,
    service: "http://192.168.2.38:3000",
    origin: { protocol: "http", host: "192.168.2.38", port: 3000 },
    options: {},
    enabled: true,
    editable: true,
    account_id: account,
    account_name: accountName,
    tunnel_id: tunnel,
    tunnel_name: tunnelName,
    tunnel_status: "healthy",
    zone_id: "z",
    zone_name: "example.com",
    dns: { action: dns, hostname: host, reason: dns === "conflict" ? "existing A record(s) use this name" : undefined },
  };
}

function record(id: string, name: string, tunnel: string, tunnelName: string, status: string) {
  return {
    zone_id: "z",
    zone_name: "example.com",
    account_id: A,
    account_name: "Racing",
    record: { id, name, type: "CNAME", content: `${tunnel}.cfargotunnel.com`, proxied: true },
    tunnel_id: tunnel,
    tunnel_name: tunnelName,
    status,
    managed: true,
  };
}

const inventory = {
  fetched_at: new Date().toISOString(),
  refreshing: false,
  errors: [],
  accounts: [
    { id: A, name: "Racing", credentials: [{ id: "c1", name: "racing", status: "active" }], zones: [] },
    { id: B, name: "Hosting", credentials: [{ id: "c2", name: "hosting", status: "active" }], zones: [] },
  ],
  tunnels: [
    { id: T1, name: "racing-shared", account_tag: A, account_name: "Racing", status: "healthy", config_src: "cloudflare", remote_managed: true, connectors: [{ id: "x", version: "2026.10.1", arch: "amd64", conns: [{ colo_name: "syd01", origin_ip: "1" }] }], route_count: 1, local: [] },
    { id: T2, name: "hosting-shared", account_tag: B, account_name: "Hosting", status: "down", config_src: "cloudflare", remote_managed: true, connectors: [], route_count: 1, local: [] },
  ],
  routes: [route("shop.racing.example", A, "Racing", T1, "racing-shared", "ok"), route("app.hosting.example", B, "Hosting", T2, "hosting-shared", "create")],
  dns: [record("r1", "shop.racing.example", T1, "racing-shared", "routed"), record("r2", "old.racing.example", T1, "racing-shared", "no-route")],
};

const creds = [
  {
    id: "c1",
    name: "ARPL",
    account_owned: true,
    account_hint: A,
    token_last4: "e085",
    status: "limited",
    created_at: "2026-10-16T00:00:00Z",
    accounts: [{ id: A, name: "Racing", zones: 45, tunnels: true, dns: false, listed: false, problems: ["cannot read DNS records (needs DNS Read or Edit)"] }],
  },
];

const deniedInventory = {
  ...inventory,
  accounts: inventory.accounts.map((a) =>
    a.id === A ? { ...a, dns_access: "denied", dns_reason: "Cloudflare refused to show DNS records in Racing. The saved token needs Zone: DNS Read." } : { ...a, dns_access: "ok" },
  ),
};

function base(inv: unknown = inventory): Record<string, Route> {
  return {
    "/api/v1/setup/status": { status: 200, body: { open: false } },
    "/api/v1/me": { status: 200, body: me },
    "/api/v1/cloudflare/inventory": { status: 200, body: inv },
    "/api/v1/cloudflare/credentials": { status: 200, body: { items: creds } },
    "/api/v1/cloudflare/machines": { status: 200, body: { items: [{ id: "host", name: "Host", kind: "host" }], supported: true } },
  };
}

function renderPage() {
  return render(
    <SessionProvider>
      <CloudflarePage />
    </SessionProvider>,
  );
}

describe("warning dismissals", () => {
  const now = Date.parse("2026-10-30T00:00:00Z");
  it("hides a dismissed warning for 3 days, then shows it again", () => {
    const d = withDismissal({}, "w", false, now);
    expect(isDismissed(d, "w", now + DISMISS_FOR_MS - 1)).toBe(true);
    expect(isDismissed(d, "w", now + DISMISS_FOR_MS + 1)).toBe(false);
    expect(isDismissed(d, "other", now)).toBe(false);
  });
  it("keeps a permanent dismissal and drops run-out ones", () => {
    let d = withDismissal({}, "old", false, now);
    d = withDismissal(d, "forever", true, now);
    d = withDismissal(d, "new", false, now + DISMISS_FOR_MS + 1);
    expect(d).toEqual({ forever: PERMANENT, new: new Date(now + DISMISS_FOR_MS + 1).toISOString() });
    expect(isDismissed(d, "forever", now + 100 * DISMISS_FOR_MS)).toBe(true);
    expect(withoutDismissals(d, ["forever"])).not.toHaveProperty("forever");
  });
});

describe("Cloudflare page", () => {
  it("shows hostnames as compact boxes that open to manage the hostname and its DNS", async () => {
    let deleted = "";
    mockApi({
      ...base(),
      "POST /api/v1/cloudflare/routes/diagnose": {
        status: 200,
        body: { summary: "Internal service: the connector cannot reach the origin.", checks: [{ area: "origin", status: "fail", detail: "192.168.2.38:3000 is not reachable from Host" }] },
      },
      "POST /api/v1/cloudflare/routes/delete": (init) => {
        deleted = String(init?.body ?? "");
        return { status: 200, body: { deleted: true, note: "DNS record removed" } };
      },
    });
    renderPage();
    const box = await screen.findByRole("article", { name: "shop.racing.example" });
    expect(screen.getByRole("region", { name: /tunnel racing-shared/i })).toContainElement(box);
    // No separate DNS or Accounts views any more.
    expect(screen.queryByRole("button", { name: /^dns$/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^accounts$/i })).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
    // A DNS record no hostname uses has a box of its own under its tunnel.
    expect(within(screen.getByRole("region", { name: /tunnel racing-shared/i })).getByRole("article", { name: /dns record old.racing.example/i })).toBeVisible();

    // Closed, the box shows no DNS records; opened, it shows the record and the actions.
    expect(within(box).queryByRole("list", { name: /dns records/i })).toBeNull();
    const toggle = within(box).getByRole("button", { name: /shop\.racing\.example/i });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const dns = within(box).getByRole("region", { name: /dns for shop.racing.example/i });
    expect(within(dns).getByText("DNS ok")).toBeVisible();
    expect(within(dns).getByText(`${T1}.cfargotunnel.com`)).toBeVisible();
    expect(within(box).getByText("Goes to")).toBeVisible();

    // The hostname with no DNS record offers to fix it.
    const other = screen.getByRole("article", { name: "app.hosting.example" });
    fireEvent.click(within(other).getByRole("button", { name: /app\.hosting\.example/i }));
    // Only one box is open at a time.
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(other).getByRole("button", { name: /^fix dns$/i })).toBeVisible();
    expect(within(within(other).getByRole("region", { name: /dns for/i })).getByText(/no tunnel dns record/i)).toBeVisible();

    // Right-click opens the same actions.
    fireEvent.contextMenu(box);
    fireEvent.click(await screen.findByRole("menuitem", { name: /^diagnose$/i }));
    const diag = await screen.findByRole("dialog", { name: /diagnose shop.racing.example/i });
    expect(await within(diag).findByText(/internal service/i)).toBeVisible();
    fireEvent.click(within(diag).getByRole("button", { name: /close/i }));

    fireEvent.click(toggle);
    fireEvent.click(within(box).getByRole("button", { name: /^delete$/i }));
    const del = await screen.findByRole("dialog", { name: /delete shop.racing.example/i });
    fireEvent.click(within(del).getByRole("button", { name: /^delete$/i }));
    await waitFor(() => expect(deleted).toContain('"confirm":"shop.racing.example"'));
    expect(JSON.parse(deleted)).toMatchObject({ delete_dns: true, tunnel_id: T1 });
  });

  it("filters by account and collapses tunnels", async () => {
    mockApi(base());
    renderPage();
    await screen.findByRole("article", { name: "app.hosting.example" });
    fireEvent.change(screen.getByLabelText(/account filter/i), { target: { value: A } });
    expect(screen.queryByRole("article", { name: "app.hosting.example" })).toBeNull();
    fireEvent.change(screen.getByLabelText(/account filter/i), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /hide hosting-shared hostnames/i }));
    expect(screen.queryByRole("article", { name: "app.hosting.example" })).toBeNull();
  });

  it("opens Manage accounts and forces a sync from Cloudflare", async () => {
    const calls = mockApi({
      ...base(),
      "POST /api/v1/cloudflare/credentials/c1/validate": { status: 200, body: creds[0] },
    });
    renderPage();
    await screen.findByRole("article", { name: "shop.racing.example" });
    fireEvent.click(screen.getByRole("button", { name: /^manage accounts$/i }));
    const dlg = await screen.findByRole("dialog", { name: /manage accounts/i });
    expect(await within(dlg).findByRole("article", { name: /token arpl/i })).toBeVisible();
    expect(within(dlg).getByRole("button", { name: /connect account/i })).toBeVisible();
    const before = calls.length;
    fireEvent.click(within(dlg).getByRole("button", { name: /force sync from cloudflare/i }));
    await screen.findByText(/synced from cloudflare/i);
    const after = calls.slice(before);
    expect(after).toContain("POST /api/v1/cloudflare/credentials/c1/validate");
    expect(after).toContain("GET /api/v1/cloudflare/inventory?refresh=1");
  });

  it("shows what a tunnel-only token cannot do from the DNS warning", async () => {
    mockApi({
      ...base(deniedInventory),
      "/api/v1/cloudflare/permissions": {
        status: 200,
        body: {
          source: "cloudflare-docs",
          fetched_at: new Date().toISOString(),
          docs_updated: "Oct 12, 2026",
          docs_url: "https://developers.cloudflare.com/fundamentals/api/reference/permissions/",
          create_url: "https://developers.cloudflare.com/fundamentals/api/get-started/create-token/",
          account_tokens_url: "https://developers.cloudflare.com/fundamentals/api/get-started/account-owned-tokens/",
          dashboard_url: "https://dash.cloudflare.com/profile/api-tokens",
          steps: ["Select Create Token."],
          permissions: [
            { id: "tunnel_read", scope: "Account", group: "Cloudflare Tunnel", level: "Read", description: "Grants access to view Cloudflare Tunnels.", in_docs: true },
            { id: "dns_read", scope: "Zone", group: "DNS", level: "Read", description: "Grants read access to DNS.", in_docs: true },
            { id: "dns_edit", scope: "Zone", group: "DNS", level: "Edit", description: "Grants write access to DNS.", in_docs: true },
            { id: "account_settings_read", scope: "Account", group: "Account Settings", level: "Read", description: "Account resources.", in_docs: true },
          ],
          use_cases: [
            { id: "accounts", title: "Find your accounts", why: "", needs: ["account_settings_read"], resources: "All accounts", check: "accounts", user_tokens_only: true },
            { id: "tunnels_read", title: "See tunnels and hostnames", why: "", needs: ["tunnel_read"], resources: "the account", check: "tunnels" },
            { id: "dns_read", title: "Show DNS status", why: "", needs: ["dns_read"], resources: "All zones", check: "dns" },
            { id: "dns_edit", title: "Create and fix DNS records", why: "", needs: ["dns_edit"], resources: "All zones" },
          ],
        },
      },
    });
    renderPage();
    const warning = await screen.findByRole("alert");
    expect(within(warning).getByText(/dns can't be read for racing/i)).toBeVisible();
    fireEvent.click(within(warning).getByRole("button", { name: /show what to add/i }));
    const dlg = await screen.findByRole("dialog", { name: /manage accounts/i });
    const list = await within(dlg).findByRole("list", { name: /what this token can do/i });
    const item = (t: RegExp) => within(list).getByText(t).closest("li")!;
    expect(within(item(/see tunnels/i)).getByText(/allowed/i)).toBeVisible();
    expect(within(item(/create and fix dns/i)).getByText(/missing/i)).toBeVisible();
    expect(within(item(/find your accounts/i)).getByText(/not needed/i)).toBeVisible();
    expect(within(dlg).getByRole("link", { name: /api tokens in the cloudflare dashboard/i })).toHaveAttribute("href", `https://dash.cloudflare.com/${A}/api-tokens`);
  });
});

describe("Cloudflare warnings", () => {
  const id = `dns:${A}:denied`;

  it("dismisses a warning and remembers when", async () => {
    mockApi(base(deniedInventory));
    renderPage();
    const warning = await screen.findByRole("alert");
    fireEvent.click(within(warning).getByRole("button", { name: /dismiss warning/i }));
    expect(screen.queryByRole("alert")).toBeNull();
    const saved = getPref<Record<string, string>>(DISMISS_PREF, {})[id];
    expect(Date.now() - Date.parse(saved)).toBeLessThan(60_000);
    // It can be brought back.
    fireEvent.click(screen.getByRole("button", { name: /show again/i }));
    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("stays hidden within 3 days and comes back after", async () => {
    setPref(DISMISS_PREF, { [id]: new Date(Date.now() - DISMISS_FOR_MS + 3_600_000).toISOString() });
    mockApi(base(deniedInventory));
    renderPage();
    await screen.findByRole("article", { name: "shop.racing.example" });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText(/1 warning dismissed/i)).toBeVisible();
    cleanup();

    setPref(DISMISS_PREF, { [id]: new Date(Date.now() - DISMISS_FOR_MS - 60_000).toISOString() });
    renderPage();
    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("dismisses permanently from the right-click menu", async () => {
    mockApi(base(deniedInventory));
    renderPage();
    const warning = await screen.findByRole("alert");
    fireEvent.contextMenu(warning);
    fireEvent.click(await screen.findByRole("menuitem", { name: /dismiss permanently/i }));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(getPref<Record<string, string>>(DISMISS_PREF, {})[id]).toBe(PERMANENT);
    cleanup();

    // Long after, it still does not come back.
    const later = Date.now() + 30 * DISMISS_FOR_MS;
    vi.spyOn(Date, "now").mockReturnValue(later);
    renderPage();
    await screen.findByRole("article", { name: "shop.racing.example" });
    await act(async () => {});
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
