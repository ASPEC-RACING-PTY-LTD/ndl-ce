# Cloudflare Tunnel Manager

The Cloudflare page (Infrastructure, Cloudflare) manages tunnels, public
hostnames and DNS for every connected Cloudflare account in one view. Every
row shows the account that owns it. There are no per-account dashboards.

## Connecting accounts

Use scoped API tokens. Global API keys are not accepted. Give a token:

- Account: Cloudflare Tunnel Edit (Read is enough to only view)
- Zone: DNS Edit and Zone: Zone Read, for the zones you publish
- Account: Account Settings Read, so its accounts are found

Account-owned tokens (`cfat_...`) cannot list accounts or verify at the user
endpoint, so enter the account ID with them. Any number of tokens and accounts
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
| `cloudflare.read` | viewer, operator, admin | See the inventory, diagnose routes |
| `cloudflare.manage` | operator, admin | Tunnels, routes, DNS, connectors, moves, publishing |
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

## Polling

The inventory is cached for two minutes and refreshed in the background, so
the page does not poll Cloudflare. A refresh makes roughly two calls per
tunnel plus one per account and zone. Each token is paced to stay well under
Cloudflare's limit of 1,200 requests per five minutes, which is shared with
the dashboard.

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
