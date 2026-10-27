# Edge forwarding

Edge forwarding publishes a service that runs on a node with no public
address of its own, such as a home server behind a router, on a node that
has one, such as a VPS in the cluster (the edge). Players connect to the
edge. Their traffic travels through a WireGuard tunnel to the node the
service runs on (the origin), and **the service sees every player's own
address**: bans, IP history, per-address limits and logs keep working. The
origin's own address is never shown to players or to Steam.

It is under Network > Edge forwarding, and on a game server under
Settings > Public address.

## How it works

```text
players ──► edge (VPS, public address)
              │  rewrites the destination only, never the source
              ▼
         WireGuard tunnel (the origin dials out; nothing is opened at home)
              ▼
         origin (home node) ──► game server, workload or host port
```

- **Edge.** An nftables table (`inet ndl_edge`) DNATs the published ports
  to the origin's tunnel address. The player's source address is left
  alone. Traffic the origin starts itself is source-NATed to the edge's
  address on the way out.
- **Origin.** Connections that arrive through the tunnel are marked, and
  their replies are routed back through it by a policy rule (`fwmark`, one
  routing table per tunnel), whatever the node's default route. A UDP
  server on the host network answers from an unconnected socket; its
  replies get the tunnel address directly so the edge matches them to the
  player.
- **Its own traffic** (on by default for game servers): new outbound
  connections from the game server's container leave through the edge too,
  so Steam lists the server at the edge's address and your home address is
  never seen. The tunnel's routing table ends in an unreachable route: if
  the tunnel goes down, the server gets no internet rather than falling
  back to the home line.
- **Keys.** Each node makes its own WireGuard key and keeps it under
  `/var/lib/ndl/net/edge/keys` (0600). Only public keys pass through the
  control plane. Tunnel `n` uses `10.66.0.(4n+1)` on the edge and
  `10.66.0.(4n+2)` on the origin, and UDP port `51870+n` on the edge.
- The agent restores everything at start, before the control plane is up,
  and checks it every 15 seconds: a removed interface, a flushed table or a
  game server container that came back with a new address is put back.
  When Docker's FORWARD policy is drop, accept rules for the tunnel are
  placed in `DOCKER-USER`.

## What is forwarded

| Kind        | Default ports                                               | Player addresses               |
| ----------- | ----------------------------------------------------------- | ------------------------------ |
| Game server | Every published port, except a Source server's TCP (RCON)   | Passed on                      |
| Workload    | The ports you list                                          | Hidden unless you choose Pass on |
| Host ports  | The ports you list, on the node you choose                  | Passed on                      |

A workload on a LAN bridge answers through your router, not its node, so
its traffic is hidden behind the node (source NAT) unless you turn on Pass
players' addresses on, which needs the workload to route through its node
(a NAT network). Ports are one per line: `udp 27015`, `tcp 25565`,
`udp 27000-27010`, or `udp 7777 to 27016` for a different port on the
edge. TCP 22 and 9444 on the edge are refused.

## Setting it up

1. Add the VPS to the cluster (Cluster > Add node).
2. Publish the service: Network > Edge forwarding > Publish a service, or
   Publish through another node on the game server. The tunnel comes up on
   its own within a few seconds.
3. Allow the published ports and the tunnel's UDP port in to the VPS, in
   the provider's firewall too.
4. Remove the home router's port forwards to the service.
5. Point the service's name at the VPS. On Cloudflare use a DNS only
   (grey cloud) A record; the proxy does not carry game traffic.
6. Turn on DDoS Protection: the service's limits are loaded on the edge
   too, so a flood is dropped on the VPS before it enters the tunnel.

Nothing needs changing in the game server: it keeps listening on its
usual port, with no `-ip` or `+ip` option.

## Versions

Both nodes need a No-dal with edge forwarding. A worker on an older release
shows "too old" on the Edge forwarding page with Update No-dal, which asks
its agent to update from its package repository (`POST
/api/v1/nodes/{id}/update` with `X-Nodal-Confirm: update-node`). Its guests
keep running.

## API

| Method and path                      | Does                                        |
| ------------------------------------ | ------------------------------------------- |
| `GET /api/v1/edge`                   | Nodes, tunnels and published services       |
| `POST /api/v1/edge/forwards`         | Publish a service                           |
| `PUT /api/v1/edge/forwards/{id}`     | Change one (only fields sent)               |
| `DELETE /api/v1/edge/forwards/{id}`  | Stop publishing it                          |
| `PUT /api/v1/edge/nodes/{id}`        | Set an edge node's public address           |
| `POST /api/v1/edge/apply`            | Push every node's part again now            |

Reading needs `network.read`; changes need `network.apply`. From the
terminal: `nodalctl edge show`, `nodalctl edge apply` and `nodalctl edge
publish --kind gameserver --ref SERVER_ID --edge NODE_ID [--ports
udp/27015,tcp/27020] [--egress false]`.

## Testing

`go test ./internal/edge/` checks validation and the rules. As root,
`NDL_EDGE_KERNEL=1 go test -run TestKernel ./internal/edge/` builds a
player, a NAT router, an edge, an origin and a container in throwaway
network namespaces, brings a real tunnel up and checks that players reach a
host-network UDP server, a published container and a TCP server through
the edge with their own address, that the container's outbound traffic
leaves with the edge's address, and that it is refused, not leaked, while
the tunnel is down.
