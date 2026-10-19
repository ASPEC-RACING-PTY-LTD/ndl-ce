# Cloudflare Tunnel Manager

The Cloudflare page (Network, then Cloudflare at the top) manages tunnels,
public hostnames and DNS for every connected Cloudflare account in one view.
Every row shows the account that owns it. There are no per-account
dashboards.

Cloudflare is an optional feature, off on a fresh install; turn it on in Add
Features. Turning it off only hides the page and the workload panels. Saved
tokens, tunnels, routes and running connectors are kept and keep working.

## Hostnames

Hostnames are grouped under their tunnel. A tunnel's heading row shows its
status, connectors and where they run, and its menu (or a right-click) holds
the tunnel actions: add a hostname to it, rename, deploy, restart or update a
connector, move all its hostnames, and delete. Tunnels without hostnames are
listed too. Columns sort when their heading is clicked; the pencil at the end
of the heading row chooses the columns and whether to group by tunnel. These
choices are saved to your profile.

## Connecting accounts

Use scoped API tokens. Global API keys are not accepted. Give a token:

- Account: Cloudflare Tunnel Edit (Read is enough to only view)
- Zone: DNS Edit and Zone: Zone Read, for the zones you publish
- Account: Account Settings Read, so its accounts are found

Account-owned tokens (`cfat_...`) cannot list accounts or verify at the user
endpoint, so enter the account ID with them.

Each token card under Accounts has a Permissions dropdown. It lists what No-dal
does with Cloudflare, whether the token allows it, and for anything missing
the exact permission to add (type, permission, access and resources) with
the steps to edit the token. The permission names, descriptions and token
steps are read from Cloudflare's documentation (its Markdown copies of the API
token permissions and Create API token pages), cached for twelve hours, with a
built-in copy when the documentation cannot be reached. Write access cannot be
tested without changing something, so it is confirmed the first time it is
used.

DNS is tried on every refresh, so a permission added to the token in
Cloudflare is used straight away; the token's card catches up without
checking it again. When Cloudflare refuses DNS for an account, the page says
so first, above everything else: which account, what it means, and a button
that opens the permissions to add. A few zones are tried before deciding, so
a refused token is not asked once per zone. While DNS is refused, a token
(for example one with only Zero Trust or Tunnel permissions) can still add
and edit hostnames: the route is saved, DNS is left alone, and the reply says
which permission to add. Moving hostnames
between tunnels needs DNS access, since the CNAME must be repointed. Any number of tokens and accounts
can be connected. When a token is added or rotated, No-dal verifies it and
records which accounts, zones and permissions it reaches; nothing is saved if
verification fails. A rotation that would lose access to an account asks
first.

Tokens are encrypted with AES-256-GCM using a key in
`/var/lib/ndl/control/keys/cloudflare.key` (readable only by the control
plane) and stored in the `secrets` schema, bound to their credential. They
are never returned to the browser, logged, or written to the audit log; only
the last four characters are shown. Removing a token from No-dal changes
nothing in Cloudflare.

Every Cloudflare call uses a token that was found to reach that account or
zone. If a token is rejected it is marked invalid and the next token for the
account is tried; a missing permission moves on to the next token; rate
limits and outages stop the call and are reported.

## Permissions in No-dal

| Grant | Default roles | Allows |
| --- | --- | --- |
| `cloudflare.read` | user, admin, superadmin | See the inventory, diagnose routes |
| `cloudflare.manage` | admin, superadmin | Tunnels, routes, DNS, connectors, moves, publishing |
| `cloudflare.credentials` | admin | Add, rotate and remove API tokens |

Every change is recorded in the audit log.

## Hostnames and DNS

Adding or editing a hostname edits the tunnel's remote configuration and, by
default, creates or fixes the hostname's proxied CNAME to
`<tunnel id>.cfargotunnel.com`. Before anything is written, existing records
are checked:

- an unrelated record (A, AAAA, TXT, MX or a CNAME elsewhere) at the name is a
  conflict and is never changed;
- a CNAME to another tunnel is only repointed when you confirm it (or Move);
- a domain in a different account from the tunnel is refused, because
  Cloudflare does not serve a CNAME to another account's tunnel (error 1014).

If the DNS change fails, the new route is removed again. Configuration edits
read the current config, apply the change and write it back only if the
config version has not changed meanwhile, so other routes and settings are
never overwritten. Origin settings No-dal does not model are kept.

Cloudflare has no "disabled" flag for a route. Switching a route off removes
it from the tunnel and No-dal keeps a copy so it can be switched back on. DNS
is left in place.

Routes created in No-dal cannot use `unix://` origins or link-local
addresses (such as `169.254.169.254`).

## Tunnels and connectors

Tunnels created in No-dal are remotely managed (`config_src: cloudflare`).
Tunnels managed by a config file on their connector are shown read-only; their
routes are listed when the connector runs on this node.

A connector can be deployed on the host or in a system container. cloudflared
is installed from Cloudflare's signed apt repository when missing, and runs as
`cloudflared-ndl-<id>.service` with the tunnel token in a root-only
environment file. The token is fetched from Cloudflare for that request only
and passed to the agent; the control plane never stores it. Update upgrades
the cloudflared package and restarts its services. Remove only touches units
No-dal created. A connector on the host can reach every container's address,
so one shared tunnel can serve many workloads.

Deleting a tunnel requires its name, and is refused while it still has
routes, DNS records pointing at it, or connected connectors.

## Moving routes between tunnels

Select hostnames and Move. The preview blocks a route when the destination
is config-file managed or has no connector, already has the hostname, the
zone is in another account, or the origin is `localhost` on a different
machine; when the destination's connector runs on this node, reaching the
origin from it is tested. Each route is then moved in order: added to the
destination, DNS repointed, the site checked through Cloudflare, and only then
removed from the source. A failed check puts DNS back and removes the copy.
Source tunnels are never deleted.

## Workloads

A workload's page lists the hostnames that reach it and has Publish, which
fills in its address. Creating a system container can optionally publish it
once it has an address. Nothing is published without that choice. When a
container's address changes, its routes are flagged and can be updated in
one step. The account and tunnel last used for a workload are remembered.

## Diagnostics

Diagnose checks a route layer by layer: connectors connected to the tunnel,
DNS, the origin as reached from each connector on this node (a TCP connect
only, and only to the route's own origin address), and the public URL. The
summary names the failing layer.

## Polling and speed

The inventory is cached for two minutes and refreshed in the background, so
the page does not poll Cloudflare. A refresh reads each account's tunnels and
zones, then every tunnel's routes and every zone's DNS records, eight at a
time. Connectors come from the tunnel list itself. Each
token keeps one connection pool and a rate budget that allows short bursts
and then stays well under Cloudflare's limit of 1,200 requests per five
minutes, which is shared with the dashboard.

The page shows the last inventory saved in the browser at once, then asks the
server whether anything changed: an unchanged inventory is answered with 304
and no body, and only rows whose data changed are redrawn.

## Limitations

- Routes of config-file tunnels cannot be edited through the API.
- Cloudflare's tunnel connection list does not identify which machine a
  connector runs on. Connectors on this node are matched by the tunnel ID
  cloudflared logs at start; others appear only as counts.
- Remotely managed tunnels report their routes to the local connector in its
  log; No-dal reads that line to link hostnames to containers.
- Cross-account moves need the zone in the destination tunnel's account.
- Live Cloudflare calls are covered by tests against a mock of the API, not
  against Cloudflare itself.
