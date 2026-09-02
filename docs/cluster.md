# Cluster

A cluster starts as one writer. Additional nodes join with a single-use
join token. Hostname is not node identity. Pairing tokens are not join
tokens.

## Join

```text
nodalctl cluster join-token create
nodalctl cluster join --token TOKEN --url URL
```

The control node cannot be revoked. Workloads stay on the node that
owns them until migrate or restore selects a dest.

## Placement and migrate

Placement (Phase 31) chooses a node. Live migrate (Phase 32) is
`workload.migrate`. The dest agent is unwired until Execute is
connected. A nil migrate engine returns dest-agent-not-connected and
leaves the source running. That is not a completed two-box migrate.
`-incoming` is `defer` only. Restore disks are copied to the dest, not
onto the control node for a worker dest.

## HA

Phase 34 is single-writer HA foundations. Promotion is privileged.
STONITH is not implemented. Multi-master is not claimed.

WireGuard remote nodes are Phase 28. Overlay and VLAN/bond are Phase 27.

See also [recovery.md](recovery.md).
