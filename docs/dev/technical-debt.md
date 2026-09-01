# Technical debt ledger

Non-blocking findings from autonomous CE implementation.
Review before Dogfood Host, Homelab Migration Candidate, Feature-Complete Beta, and CE 1.0.

## Phase 4

- MEDIUM. nft INPUT default-drop is deferred to Phase 27.
  Why not blocking: isolated NAT already uses typed nft; default-drop is the later host firewall phase.
- LOW. Management NIC detection uses the default-route interface.
  Why not blocking: matches Phase 4 safety goal on the single-NIC appliance.
- LOW. Network apply confirm HMAC is keyed with the cluster UUID.
  Why not blocking: confirm is still required; stronger binding can wait.

## Phase 5

- MEDIUM. Agent does not independently confine CT rootfs paths to storage roots.
  Why not blocking: the API joins pool locators; defense-in-depth can land with later storage backends.
- MEDIUM. Agent does not re-enforce privileged/idmap policy. HTTP RBAC does.
  Why not blocking: southbound is peer-cred to the control plane only.
- LOW. Some control-plane rootfs joins still use path.Join. Phase 6 jail join uses storage.JoinUnder.
- LOW. AppArmor is set unconfined when securityfs is missing (Docker e2e).
- LOW. Official simplestreams index is HTTPS-only; the tarball is gpgv-verified.
- LOW. lxcfs is skipped under container virtualization.

## Phase 6

- MEDIUM. Agent CapabilityBoundingSet now includes CAP_SYS_ADMIN for typed lxc-attach setns only.
  Why not blocking: there is still no generic host execution RPC; DevicePolicy remains closed.
- LOW. Browser tickets use the `ndl.ticket.` WebSocket subprotocol because browsers cannot set `X-Nodal-Ticket`. Query-string tickets are rejected.
- LOW. Folder archive download was left for Phase 17.
- LOW. VM Terminal and Files remain Phase 20 and return 422.
- LOW. Docker Desktop overlay backing identity changes across host reboot, so Directory pools on this e2e guest can show unavailable. Real disk UUIDs are the product path.
