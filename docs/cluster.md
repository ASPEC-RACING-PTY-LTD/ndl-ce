# Cluster

A cluster starts as one writer, the control node. Any other Debian 13
amd64 machine, on your network or a VPS anywhere, joins as a worker with a
single-use join token. Hostname is not node identity; the node UUID is.
Pairing tokens are not join tokens.

## Add a node

From the web UI: **Cluster > Add node**. It shows the two commands to run
on the new machine and watches it connect.

From the terminal:

```text
# On the control node
nodalctl cluster join-token create

# On the new machine
curl -fsSL https://raw.githubusercontent.com/ASPEC-RACING-PTY-LTD/ndl-ce/main/packaging/bootstrap/get-nodal.sh | sudo sh
sudo nodalctl cluster join --url https://CONTROL --token TOKEN \
  --fingerprint SHA256 [--advertise PUBLIC_IP]
```

- `--url` is the control node's address as the new machine sees it. Over
  the internet use HTTPS: nodalctl refuses to send a join token over plain
  http to a public address.
- `--fingerprint` pins the control node's HTTPS certificate, so a
  self-signed control node is safe to join over the internet. The join
  token response and the Add node dialog include it.
- `--advertise` is where the control node dials this worker's agent. It
  defaults to the address the join came from, on port 9444.
- Behind Cloudflare Access, save a service token under **Cluster > Join
  settings** and Add node puts it in the command. From the terminal, pass
  it with `--access-client-id` and `--access-client-secret` (or set
  `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET`). The Access
  application needs a Service Auth policy that includes the token. The
  secret is stored sealed and only shown inside a new join command. A join
  relayed by Cloudflare does not show the worker's address, so also pass
  `--advertise`.

The join always goes to `--url`, even on a machine that already runs
No-DAL. It saves the node certificate under `/var/lib/ndl` and restarts
`ndl-agent`, which then opens its TLS port. Guests are their own systemd
units and keep running.

Allow inbound TCP 9444 on the worker from the control node (for a VPS, in
the provider's firewall or security group).

## Agent link

The control node drives a worker's agent over mutual TLS on TCP 9444:

- The worker serves the node certificate issued at join and accepts only
  the control certificate from this cluster's CA. Other workers'
  certificates are refused.
- The control node accepts only the certificate issued to that node, and
  refuses one that has been revoked.

No VPN or tunnel is needed. The control node checks every worker every 15
seconds and records its status, host, CPU and memory. The Cluster page
shows them, with **Check** to test the link now and **Address** to change
where the agent is dialed (`nodalctl node dest-listen --id ID --addr
HOST:PORT`).

## Update a worker

A worker installs No-dal from its own package repository. **Update No-dal**
(or `POST /api/v1/nodes/{id}/update` with `X-Nodal-Confirm: update-node`)
asks its agent to refresh the package lists and run the platform update in
its own systemd unit; the agent restarts during it and guests keep running.
From the terminal: `nodalctl node update --id ID --confirm update-node`, and
`nodalctl node update --id ID` for its last run.
The cluster's rolling update does the same for every worker.

## Remove a node

**Revoke** retires the node's certificate, so the control node stops
dialing it. **Delete** removes the node record; a node that still has
workloads is refused. The control node cannot be revoked.

## Placement and moves

Placement chooses a node for new workloads. Existing workloads stay on the
node that owns them until a move or a restore selects another node.

## HA

Single-writer HA: one writer holds the lease. Promotion is privileged.
Fence records that the old writer is isolated, then promote takes the
lease.

See also [recovery.md](recovery.md).
