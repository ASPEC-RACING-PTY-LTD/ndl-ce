# DDoS protection

DDoS Protection is an optional module (Add Features > DDoS Protection). It
filters floods aimed at the services ndl-ce runs, on the node's own uplink,
before they reach a game server, a workload or the host. It lives under
Network > DDoS Protection.

## What it can and cannot stop

The filter runs on the node. It stops attacks your connection can carry:
query floods, per-source packet floods, SYN and connection floods,
reflection (amplification) traffic, fragments and malformed packets. These
are the usual attacks on game servers, which aim to crash or lag the server
rather than fill the line.

A flood bigger than the uplink fills it before it reaches the node, and no
on-host filter can help with that. Only the provider, or a scrubbing service
in front of it, can. Web services can also go behind Cloudflare (Network >
Cloudflare).

## How it works

`ndl-agent` loads two No-dal-owned nftables tables, both called `ndl_shield`:

- `netdev ndl_shield` hooks **ingress** on the uplink, at priority -500. It
  sees every packet from outside first: before conntrack, before Docker's
  port publishing (DNAT) and before the LAN bridge hands frames to system
  containers. Almost every check happens here, without conntrack, so a
  flood costs as little as possible.
- `inet ndl_shield` hooks **prerouting** at priority -150, after conntrack
  and before Docker NAT. It holds only the per-source open-connection
  limits, which need conntrack, and is only loaded when a target uses one.

Neither table hooks `input` or touches the host's own rules. Both are
replaced in one `nft -f` transaction after `nft -c` checks them, so a rule
that fails the check changes nothing. The spec is kept in
`/var/lib/ndl/net/shield/`. The agent loads it again at start (before the
control plane is up) and every 30 seconds checks whether the tables are
missing or the node's addresses or uplink changed (DHCP, a new NIC).

**The uplink** is found from the default routes. A bridge (such as the LAN
bridge `ndl…`) is replaced by its physical ports, because the ingress hook
of a bridge device does not see traffic bridged to guests. You can name the
interfaces yourself in Settings.

## Targets

| Kind         | Matches                                                    |
| ------------ | ---------------------------------------------------------- |
| Game server  | The node's own addresses, on the server's published ports  |
| Workload     | The workload's addresses, on chosen ports or every port    |
| Host service | The node's own addresses, on chosen ports                  |
| Custom       | Any destinations (addresses or CIDRs) and ports you enter  |

**Every game server is protected by default** with the profile for its game,
on the ports it publishes. When ports change, the rules follow within 15
seconds. Editing a default target stores it with your changes; Reset to
default goes back. Workloads follow their address, so a DHCP change is
picked up too.

## Profiles and limits

A profile fills in a target's limits. Every limit can be changed, and zero
turns it off.

| Profile       | For                                         | Main limits                                                        |
| ------------- | ------------------------------------------- | ------------------------------------------------------------------ |
| Source engine | Garry's Mod, CS, TF2, L4D and other srcds   | 400 pkt/s per source, 5 queries/s, no fragments, no empty packets  |
| Minecraft     | Java and Bedrock                            | 6 new and 6 open connections per source                            |
| UDP game      | Rust, ARK, Valheim, FiveM and other games   | 600 pkt/s per source, 10 queries/s                                 |
| TCP game      | Games that talk over TCP                    | 10 new and 10 open connections per source                          |
| Web service   | HTTP and HTTPS                              | 50 SYN/s and 128 open connections per source                       |
| Under attack  | Tight limits while an attack is on          | 200 pkt/s, 2 queries/s, packets over 1400 bytes dropped            |

The limits are:

- **Packets per second per source** and **burst**. An IPv6 source is its
  /64, so an attacker cannot rotate through one prefix.
- **Server queries per second per source**: Steam A2S queries (`T`, `U`,
  `V`, `W`, `i`) and the Source connect challenge (`q`). These are matched
  by payload. Query floods are the most common attack on Source servers.
- **New connections per second** (SYN rate) and **open connections** per
  source, for TCP.
- **Total packets per second** to the target. This is a last resort that
  keeps the node responsive; real players over it are dropped too.
- **Smallest UDP payload** and **largest packet**.
- **Drop reflection traffic**: UDP from DNS, NTP, SSDP, memcached, CLDAP,
  SNMP and other reflection ports. Game traffic never comes from them.
- **Drop fragments**: Source splits its own packets, so IP fragments to it
  are attack traffic.

Over a per-source limit, the target either **drops the excess** or **bans
the source** for a while (5 minutes by default). UDP sources can be faked,
so an attacker could fake a player's address to get them banned. To limit
that, a ban only covers the one target and always expires.

## Edge nodes

A service published through another node ([Edge forwarding](edge-forwarding.md))
is attacked at that node's address, so its target's rules are loaded on the
edge node too, on the edge's public ports. A flood is dropped on the edge
and never enters the tunnel; the service still sees real player addresses,
so its per-source limits work on both nodes. The edge uses the same mode,
trusted and blocked networks, hardening and hand-added bans, but does not
trust its own neighbours (on a VPS they are other customers). Its counters
are added to the target's, and the DDoS page lists each edge node with its
status.

## Trusted, blocked and bans

- **Trusted networks** are always let through and never banned. By default
  the node's own networks (your LAN) are trusted, so local players and
  admins are never limited. If your router rewrites the source address of
  port-forwarded traffic, every player looks local; turn that off then.
- **Blocked networks** are dropped on every target, for good.
- **Bans** you add by hand cover every target for up to 7 days. Unban lifts
  every ban of the address, both hand-added and automatic.

## Modes

- **Enforce** drops and bans.
- **Monitor only** counts what would be dropped without dropping or banning
  anything. Use it to check limits against real traffic first.

## Kernel hardening

When on, the agent makes these settings stricter (it never loosens one the
host already has stricter). The originals are kept and put back when
hardening or the module is turned off.

| Setting                          | Value          |
| -------------------------------- | -------------- |
| `net.ipv4.tcp_syncookies`        | 1              |
| `net.ipv4.tcp_max_syn_backlog`   | at least 8192  |
| `net.ipv4.tcp_synack_retries`    | at most 3      |
| `net.netfilter.nf_conntrack_max` | at least 262144 |

They are written to `/etc/sysctl.d/50-ndl-shield.conf` so they survive a
reboot.

## Attacks and alerts

Every 15 seconds the control plane reads the counters. When a target drops
more packets per second than the attack threshold (1000 by default), an
attack is recorded and everyone with `ddos.read` gets a bell notification.
The attack ends after a minute below a quarter of the threshold, with a
summary: how long it lasted, its peak, packets dropped and the main reason.
Network > DDoS Protection > Attacks keeps the last 500.

## Permissions

- `ddos.read`: see targets, traffic, bans and attacks. Admin and User.
- `ddos.manage`: change targets, limits, settings and bans. Admin.

## Turning it off

Turning the module off removes both tables and puts back the kernel
settings at once. Settings, targets and attack history are kept for when it
is turned on again.

## API

| Method and path                  | Does                                                  |
| -------------------------------- | ----------------------------------------------------- |
| `GET /api/v1/ddos`               | Settings, targets with traffic, bans, filter status   |
| `PUT /api/v1/ddos/settings`      | Change settings (only fields sent)                    |
| `POST /api/v1/ddos/targets`      | Protect a target                                      |
| `PUT /api/v1/ddos/targets/{id}`  | Change a target (`auto:gs:<id>` stores a default one) |
| `DELETE /api/v1/ddos/targets/{id}` | Stop protecting a target                            |
| `POST /api/v1/ddos/bans`         | Ban an address (`addr`, `minutes`, `reason`)          |
| `DELETE /api/v1/ddos/bans/{addr}` | Lift every ban of an address                         |
| `GET /api/v1/ddos/events`        | Attack history                                        |
| `POST /api/v1/ddos/apply`        | Load the rules again now                              |

## Testing

`go test ./internal/shield/` checks rendering, validation, uplink detection,
hardening and status parsing. As root,
`NDL_SHIELD_KERNEL=1 go test -run TestKernel ./internal/shield/` loads the
rules into the real kernel inside throwaway network namespaces and floods
them: query flood, reflection, empty packets, a per-source flood with a
ban, and a connection limit. The host's own networking is never touched.
