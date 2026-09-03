# Audit: remaining correctness (slice f388)

Inspected on branch `cursor/audit-remaining-f388` against current `main`.

## Files inspected

**Overlay / snapshot / template:** `internal/httpapi/phase10.go`, `phase10_test.go`, `phase11.go` (backup overlay create), `phase18.go`, `phase18_test.go`, `internal/qemu/overlay.go`, `overlay_test.go`, `internal/storage/qemuimg.go`

**Network:** `internal/httpapi/phase4.go`, `phase27.go`, `phase27_test.go`, `phase28.go`, `phase28_test.go`, `internal/ndnet/engine.go`, `nft.go`, `policy.go`, `forward.go`, `vlan.go`, `bond.go`, `overlay.go`, `wireguard.go`, `advanced.go`, `nat_test.go`

**HA / rolling / DR / placement:** `internal/httpapi/phase31.go`, `phase33.go`, `phase34.go`, `phase34_test.go`

**Guest jail:** `internal/httpapi/phase6.go`, `phase19.go`, `phase20.go`, `internal/agentrpc/files.go`, `term.go`, `files_test.go`, `internal/iojail/jail.go`

**CT / OCI vs VM:** `internal/httpapi/phase5.go`, `phase8.go`, `phase18.go`, `phase21.go`, `internal/lxc/engine.go`

---

## Proven defects

### 1. Network policy apply: full-set HTTP loop, single-rule nft replace

| | |
|---|---|
| **Where** | `internal/httpapi/phase27.go` `applyPolicy`; `internal/ndnet/policy.go` `applyPolicy` / `RenderBridgePolicy` |
| **2xx vs GET/apply** | `POST .../policies/{id}/apply` returns **200** and marks **every** stored policy `status=available`. Host nft `bridge ndl-policy` retains **only the last** rule from the loop. GET list then shows multiple policies as available while earlier rules are absent from nft. |
| **Pattern** | Invents multi-policy enforcement. Not fail-closed for the set; no re-read of nft contents. Catalog status is written from each soft `StatusAvailable` result. |
| **Evidence** | HTTP comment says it applies the full stored set. Engine `applyPolicy` does `delete table bridge ndl-policy` then loads a file that contains **one** policy’s rules. Loop in `applyPolicy` (httpapi) calls that once per row. Test `TestPhase27ApplyOnePolicyKeepsOthers` only asserts catalog row count, not nft contents. |

### 2. Rolling drain step always `succeeded` when migrate did not empty the node

| | |
|---|---|
| **Where** | `internal/httpapi/phase34.go` `execRollingDrain` |
| **2xx vs GET/apply** | `POST .../cluster/update` returns **200**. Drain step is created with `Status: RollingSucceeded` up front. Failed/missing migrate only updates the operation message (`destAgentMissing` / migrate error); step status stays succeeded. GET plan shows drain succeeded while guests may still be on the node. |
| **Pattern** | Invents drain success. Reason string is honest (`maintenance recorded; guests keep running; remote dest agent is not connected`), but **status** is not fail-closed and is not re-read from placement. Plan may still become `unavailable` from worker **update** steps; drain itself never fails. |

---

## None in this slice (checked)

### Overlay / snapshot / flatten / rollback / template

- Flatten leftover catalog treated as rollback targets; tip `--flat-` / `--rb-` resets chain depth / clears `parent_id` inheritance.
- `-tmpl.qcow2` is **not** a chain reset; template `BackendRef` freezes parent; tip is unique per template.
- `dest == backing` refused in qemu overlay create/rollback and `QEMUCreateBackingArgv`.
- No other HTTP overlay path sets `OverlayPath == BackingPath` (phase10 create/rollback/flatten, phase18 template, phase11 backup snapshot all use unique dests).
- Rollback with missing backing file: `createOverlayFile` → qemu-img error → HTTP conflict before locator update (**fail-closed**, not invent success).
- Backup overlay create (phase11) uses the same `overlayChainDepth` / parent rules.

### Isolated-nat / VLAN / bond / WireGuard / forwarding

- Isolated-nat apply fails closed on nft/forwarding errors; owned tables only (`destroy table inet ndl_nat_*`); does not `flush ruleset`; forwarding persist is owned sysctl drop-in with restore-when-unused.
- VLAN/bond engine paths return errors on apply failure (not soft-available).
- Overlay prep remaps agent `available` → HTTP `pending` with local-prep reason.
- WireGuard create persists peers with `unavailable` until handshake; GET nodes stay NotReady (honest, not invent Ready).

### HA / placement / DR

- Promote acquires lease only; replica status stays operator-managed / unavailable with explicit reason (not invent Postgres promotion).
- Placement remote create records `unavailable` + remote-apply reason (not invent running).
- Maintain/drain warns that dest agent is required (honest warning on 200).
- Phase33 DR export is read-only manifest (no invent failover).

### Guest files / terminal jail

- Control plane `CleanRel` + agent `OpenBeneath` / CT last-applied rootfs (client `jail_root` not trusted when engine present). `JoinUnder` rejects `..`. Fail-closed on escape.

### CT / OCI vs VM

- OCI clone refused (422). CT clone implemented for directory root only; ZFS/LVM clone explicitly unimplemented.
- CT create has single root disk only (no silent extra-disk drop vs VM’s explicit 422 for data disks on clone/template).
- CT/OCI/VM start all set `DesiredPower` after successful agent lifecycle (same persist pattern).

---

## Summary

Two proven defects in this slice: (1) network policy apply status/nft mismatch for multiple policies, (2) rolling drain step status inventing success. Overlay/template, isolated-nat/forwarding/nft ownership, HA promote honesty, guest jail, and CT/OCI lifecycle in this pass: **no additional proven invent-success holes** beyond those two.
