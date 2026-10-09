# Backup engine v2: content-addressed, deduplicating repository

This document describes the new backup engine in `internal/backup`. It replaces
the previous model that archived a whole workload into one tar stream, split it
into fixed-size pieces, and uploaded the whole thing on every backup. The new
engine is incremental and deduplicating: after the first baseline, a backup
costs roughly the changed data plus a metadata scan.

The engine is wired into `ndl-agent` and `ndl-control` for new Directory
system-container backups. Full Machine / Full LXC is the default capture scope
so a normal backup can restore the complete recoverable guest. Smart Application
Data remains available when an operator only wants application state. All three
scopes use this engine, not the legacy tar path. Legacy tar/backuppack artifacts
remain restorable.

## Goals recap

- First backup establishes a baseline; later backups are genuinely incremental.
- Unchanged data is not reread, recompressed, or reuploaded.
- Local capture is decoupled from remote upload; a slow R2 transfer never blocks
  capturing the next workload.
- Local disk use is hard-bounded; the host filesystem cannot be filled.
- Restore points stay complete and trustworthy; the workload is never frozen,
  paused, stopped, or restarted. Capture only reads the live directory tree.

## Pipeline

```
live directory tree (no freeze, no pause, no stop)
  -> content-defined chunks (FastCDC)
  -> keyed chunk id (HMAC over plaintext)
  -> dedupe lookup (skip chunks already stored)
  -> compression (only when it shrinks the chunk)
  -> authenticated encryption (XChaCha20-Poly1305)
  -> immutable local pack objects
  -> versioned, sealed manifest + Blueprint  == LOCAL RESTORE POINT COMPLETE
  -> bounded, persistent, restart-recoverable upload queue
  -> pack + manifest + location map on the remote  == REMOTE PROTECTED
```

## Content-defined chunking (`internal/backup/cdc`)

FastCDC (Xia et al., USENIX ATC 2016) with normalized chunking. A stricter mask
is applied before the average size and a looser mask after it, tightening the
size distribution around the target average. The gear table is generated
deterministically from a fixed seed (splitmix64), so boundaries are stable
across processes and releases. Changing the seed or parameters is a format
change. Defaults: min 256 KiB, avg 1 MiB, max 4 MiB.

Content-defined boundaries realign after an insertion or deletion, so unchanged
regions keep producing identical chunks. A test inserts bytes near the front of
a 12 MiB stream and confirms over 90 percent of chunks are unchanged, where
fixed-size chunking would share almost nothing.

## Chunk identity and encryption (`crypto.go`)

- Chunk id is `HMAC-SHA256(idKey, plaintext)`. A keyed hash still deduplicates
  within a repository but does not let an outside party confirm that a known
  file is present by matching a public content hash.
- Subkeys (`idKey`, `encKey`, `nonceKey`) are derived from a 32-byte master key
  with HKDF using distinct labels.
- Chunks are sealed with XChaCha20-Poly1305. The nonce is derived
  deterministically from the chunk id; because each unique plaintext is stored
  exactly once, a unique plaintext maps to a unique nonce, which keeps
  deduplication working while staying nonce-safe. The chunk id is bound as
  associated data. Manifests, caches, and location maps are sealed the same way.

Bucket server-side encryption is never treated as sufficient; everything is
encrypted before it leaves the host.

## Local repository (`repo.go`)

Content-addressed store built from immutable pack objects.

- `packs/<packID>.pack` holds concatenated sealed chunks. `packs/<packID>.idx`
  is a JSON sidecar mapping chunk id to offset and length. Packs are named by
  the hash of their contents.
- The in-memory chunk index is rebuilt by scanning `.idx` sidecars on open, so
  the repository is self-describing: losing process memory never loses data.
- Writes are atomic (temp file plus rename). A `.pack` without a committed
  `.idx` is an interrupted write and is discarded on open, so partial writes
  cannot corrupt the repository.
- Small chunks are aggregated into packs (default target 16 MiB) so the object
  store is not hit with millions of tiny requests and multipart upload becomes
  useful for large packs.

## Filesystem metadata cache (`cache.go`)

A persistent per-workload cache maps path to size, mtime, ctime, inode, mode,
uid, and gid, plus the prior chunk references. A file's chunks are reused only
when every tracked attribute matches and every referenced chunk is still present
in the repository. The engine never trusts mtime alone. The cache is sealed, is
rebuildable from repository metadata, and its loss only makes the next backup
slower, never makes restore impossible.

## Manifest and Blueprint (`manifest.go`)

Each restore point has a versioned, sealed manifest recording the repository
version, chunk algorithm and parameters, workload identity, consistency mode,
per-file metadata with ordered chunk ids, capture statistics, and a Blueprint.
The Blueprint is the machine manifest: workload id, name, hostname, type, OS,
arch, base image, CPU, memory, storage, autostart, nesting, features, network
interfaces, mounts, and a Docker inventory (engine and compose versions, named
volumes, bind mounts, images). It carries no secrets.

## Restore point state (local vs remote)

A small unsealed `.state` sidecar tracks local completion and remote status
(`local-only`, `queued`, `uploading`, `verifying`, `protected`, `failed`). A
locally complete restore point is explicitly not yet protected off-host.

## Upload queue (`queue.go`, `target.go`, `objtarget.go`)

Uploads run independently of capture. Capture writes only to the local
repository and enqueues pack jobs; it returns immediately. The queue is a
bounded worker pool fed from an on-disk job directory:

- Persistent and restart-recoverable: pending jobs survive a process restart and
  resume automatically (a fresh queue picks up the persisted job files).
- Retries with exponential backoff and jitter; a job file is never lost on
  failure.
- Backpressure through a bounded channel; no unbounded goroutines or memory.
- On completion a restore point is promoted to `protected` after all its packs
  are present remotely and the manifest plus a location map are uploaded.
  Verification uses the repository index and a bounded number of HEADs at the
  end, not a per-chunk HEAD on the hot path.

`Target` is a generic interface (Cloudflare R2, other S3-compatible stores, or a
local target). `TransportTarget` adapts the existing `internal/objstore`
transport, which selects multipart upload for large packs automatically. R2
Local Uploads is an optional bucket-side configuration that can improve latency;
the engine works whether or not it is enabled and does not require it.

## Remote restore / disaster recovery (`remote.go`)

Pack objects are stored under a global, content-named key so a chunk
deduplicated across workloads is uploaded once and addressable by any restore
point. Each protected restore point also uploads a sealed location map naming
exactly the packs and offsets it needs. `FetchRemote` downloads the manifest and
location map, fetches only the required packs (including packs written by other
workloads via dedup), synthesizes local indexes, and restores, all without a
repository-wide listing. This is exercised end to end in tests.

## Retention and garbage collection (`gc.go`, `remote_gc.go`)

Retention keeps restore points by daily, weekly and monthly rotation and works
on manifests, never directly on chunks. Rules apply in that order. Each keeps
the newest point of its most recent buckets and skips a bucket that a point
kept by an earlier rule already covers. Keep 1 daily, 1 weekly and 1 monthly
therefore keeps today's point, the newest point of an earlier week and the
newest point of an earlier month. The newest point is always kept. Control
(`retainBackupIDs`) and the engine (`RetainNewest`) share this code.

Control applies retention after every policy attempt, successful or not, so a
run of failing backups cannot leave a full repository full. An artifact
record is removed only after its data was deleted. A failed deletion keeps the
record, is shown under Backups, Backup storage, and is retried on the next run.

GC unions the chunk ids of every remaining manifest. A pack with no
referenced chunk is deleted. A pack that is mostly dead (live bytes below
`DefaultRepackLiveRatio` of its size) is compacted: its live chunks are copied
into a new pack before the old pack is removed. Without compaction a pack
keeps all its bytes while a single chunk in it is referenced. Compaction is
skipped, and reported, when it would cross the free-space reserve. If any
manifest cannot be read, GC stops without deleting anything.

Expiring a restore point that was uploaded also removes its manifest and
location map from the target. Its packs become sweep candidates. The
maintenance run (`v2-gc`, once after each policy run) deletes a candidate pack
only when no remote location map this host can read references it and no local
restore point, including one still uploading, has chunks in it. Only packs this
host expired are ever considered. Restore points written with another
repository key are reported, never deleted.

## Repository key

The repository key is stored in the repository (`master.key`) and outside it at
`<data dir>/keys/backup-master.key`. If the repository directory is deleted,
the preserved key is reused, so remote backups stay restorable. If the two keys
differ, the repository key is used, the preserved file is left untouched and a
warning is shown.

## Crash and failure recovery

Opening the repository removes half-written `.tmp` files, packs without a
committed index and interrupted `remote-restore-*` staging. It also rebuilds
the state file of a committed manifest whose capture crashed before writing it,
and marks that point Recovered. A failed or cancelled capture schedules GC for
when no capture is running, which reclaims the packs it had committed. Upload
jobs for expired restore points or compacted packs are dropped. A job that
exhausts its retries is parked as `.failed` instead of being retried every tick,
and is re-armed on restart or when the restore point is enqueued again.

## Workspace safety (`workspace.go`)

Before committing new pack data the engine enforces a local repository size
ceiling and a host minimum-free-space reserve. The ceiling counts everything in
the repository, not only packs. On the agent the reserve is never below the
host disk protection critical threshold for the repository's filesystem, so a
backup stops before disk protection would have to step in. When either limit
would be crossed, the engine stops accepting data and returns `ErrWorkspaceFull`
rather than filling the host filesystem. It never deletes production data or
corrupts existing backups to make room.

The repository can be moved to another directory, for example on a large pool,
through `POST /backups/workspace/relocate`. Only an empty repository can move,
so no restore point is stranded. `GET /backups/storage` reports repository
usage, pools, retention, the last cleanup, failed cleanups, restore points
without records and backups of deleted workloads. `GET /backups/targets/{id}/usage`
measures what a target holds without changing it.

## Consistency

Capture reads a live directory tree. There is no freeze, cgroup freezer, pause,
stop, or restart anywhere in the path. The default consistency is
crash-consistent. Optional application-aware pre and post hooks
(`/etc/ndl/hooks/backup-pre` and `/etc/ndl/hooks/backup-post`) may raise a
backup to application-consistent. Those hooks run inside the guest via
`ctbackup.RunGuestHook` and must never invoke host-level freezing.

## Instrumentation

Every capture records files scanned, unchanged, and changed; logical bytes;
bytes read; chunks total, new, and reused; physical new data; packs committed;
and duration. Example from `TestDemoIncrementalNumbers` on a 20 MiB workload:

```
[baseline]    bytesRead=20.0MiB chunks total=22 new=22 reused=0  (0.0% dedup)  physicalNew=20.0MiB
[incremental] bytesRead=4.0MiB  chunks total=22 new=2  reused=20 (90.9% dedup) physicalNew=989.4KiB
```

The 16 MiB unchanged file is not reread, and a 200 KiB change stores under 1 MiB
of new physical data instead of another full upload.

## Tests

`go test ./internal/backup/...` (also passes under `-race`):

- FastCDC determinism, size bounds, boundary realignment, bounded memory.
- Capture and restore round-trip preserving content, mode, and symlinks.
- Incremental reuse of unchanged files via the metadata cache.
- Content-defined chunk reuse after a mid-file edit.
- Cross-workload deduplication.
- Deletion represented in the latest restore point; earlier point still holds
  the file.
- Tampered pack fails authentication on restore.
- Shared chunk survives deletion of one restore point; GC reclaims only
  unreferenced packs; retention selection.
- Upload decoupled from capture; restart recovery; transient-failure retry.
- Remote protect and disaster-recovery restore through the transport adapter,
  including deduplicated chunks.
- Workspace ceiling and host free-space reserve.
- Repeated-cycle goroutine plateau and staging reclamation.

## Capture scope

Every backup policy has an explicit capture mode. All three modes use this
engine (content-defined chunking, deduplication, compression, encryption,
local repository, asynchronous remote upload, Blueprint, integrity
verification). Full Machine is a full *scope*, not a return to the old
whole-rootfs tar format.

- `full` (default): complete recoverable guest filesystem minus technical mounts
  (`proc`, `sys`, `dev`, `run`). Repeated OS data is inexpensive because later
  backups reuse content-addressed chunks.
- `smart`: protect irreplaceable application and workload data
  (databases, Docker volumes and bind mounts, uploads, persistent state,
  relevant configuration, Blueprint). Reproducible trees such as
  `node_modules`, package caches, image layers, and ordinary logs are
  normally excluded. Git working trees default to excluded and are warned
  because they may contain uncommitted or local-only state. Uncertain
  classification is protected or flagged. Smart cannot rebuild a complete
  guest by itself.
- `custom`: user include/exclude plus detected-category overrides.

Scope discovery uses filesystem metadata (stat) and does not require a
backup run. It never reads secret file contents to classify a path. The
policy editor does not walk the fleet on open; Smart and Full skip
preview entirely, and Custom discovers one workload when the operator
asks.

## Integration status

Implemented: engine core; agent live capture and restore (`v2-capture`,
`v2-restore`, `v2-preview`, `v2-status`); control persistence
(`migrations/0047_backup_engine_v2.sql`, `migrations/0050_backup_workspace_hardening.sql`);
policy capture modes with Full Machine default; bounded capture concurrency;
honest application-consistency reporting; filesystem metadata fidelity;
Backups UI (protection summary, repository statistics, workspace controls);
Docker persistence inventory without secret values; Blueprint-aware
restore-as-new that stays stopped until started.

## Current creation versus legacy compatibility

CURRENT BACKUP CREATION for Directory system containers is Backup Engine V2
(`v2-capture`). ZFS guests still use `zfs send`. Directory VMs still snapshot
and flatten qcow2 from a consistent snapshot, never a live qcow2 FastCDC read.

LEGACY COMPATIBILITY: `archive`, `sync-tree`, `pack`, and `unpack` remain so
historical tar/backuppack artifacts can be restored. `executeDirectoryCTBackup`
must not be called for new capture. Do not migrate or delete old artifacts
merely to simplify the new engine.

On-hardware validation with a real production container (read-only) and
disposable mutation/restore tests is performed on the live host when this
change is landed, not in CI. The disposable engine lifecycle lives in
`internal/backup/e2e_cert_test.go`.
```

## Datastores, jobs and offsite copies

Backup locations are chosen by storage pool (`GET /backups/locations`). A pool
on its own disk is recommended; the host root disk, workload disk folders,
No-dal state and folders overlapping another target are refused, and the root
disk needs `allow_root_filesystem`. The local repository moves the same way
(`POST /backups/workspace/relocate` with `pool_id`), only while it is empty.

Maintenance runs daily and verification weekly from the nightly tick, whether
or not any backup ran. Verification checks every chunk is present and decrypts
a sample of each restore point. Protected backups
(`POST /backups/artifacts/{id}/protect`) are skipped by retention.

A policy can keep fewer restore points offsite than locally
(`offsite_keep_daily/weekly/monthly`). Restore points outside the offsite
rotation lose only their remote copy; the local copy stays.

The repository key can be exported once confirmed (`POST /backups/key/export`);
until it is, the Backups page asks for it to be saved.

## Consistent sources

Containers on ZFS pools are captured from a temporary ZFS snapshot
(`<dataset>/.zfs/snapshot/<tag>`), destroyed afterwards. VMs on Directory pools
are captured through this engine: the backup takes a qcow2 overlay, captures the
frozen image beneath it (flattened first if it has a backing chain), then merges
the overlay back with QMP `block-commit` (or `qemu-img commit` when stopped).
Overlays left by earlier backups are merged before the next one, so the chain no
longer grows. Changed-block tracking with dirty bitmaps is not implemented yet;
each VM backup reads the whole disk and deduplication keeps only changed chunks.
