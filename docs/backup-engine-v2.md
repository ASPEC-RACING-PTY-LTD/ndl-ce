# Backup engine v2: content-addressed, deduplicating repository

This document describes the new backup engine in `internal/backup`. It replaces
the previous model that archived a whole workload into one tar stream, split it
into fixed-size pieces, and uploaded the whole thing on every backup. The new
engine is incremental and deduplicating: after the first baseline, a backup
costs roughly the changed data plus a metadata scan.

The engine is a self-contained library today. Wiring it into `ndl-agent` and
`ndl-control`, the Backups UI, and database migrations is tracked as follow-up
(see "Integration status").

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

## Retention and garbage collection (`gc.go`)

Retention selects restore points to keep by daily, weekly, and monthly buckets
and operates on manifests, never directly on chunks. Deleting a restore point
removes its manifest; shared chunks are reclaimed only later by GC.

GC unions the chunk ids of every remaining manifest, then deletes only packs
whose chunks are all unreferenced. A chunk shared by another restore point
always survives. The operation is idempotent and safe to re-run after an
interruption.

## Workspace safety (`workspace.go`)

Before committing new pack data the engine enforces both a local repository size
ceiling and a host minimum-free-space reserve. If either would be violated it
stops accepting data and returns `ErrWorkspaceFull` rather than filling the host
filesystem. It never deletes production data or corrupts existing backups to
make room, and it does not preallocate huge files to reserve capacity.

## Consistency

Capture reads a live directory tree. There is no freeze, cgroup freezer, pause,
stop, or restart anywhere in the path. The default consistency is
crash-consistent. Optional application-aware pre and post hooks may raise a
backup to application-consistent; those hooks must never invoke host-level
freezing. Hook wiring is part of the agent integration follow-up.

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

## Integration status

Implemented and tested: the engine core (chunking, dedup repository, metadata
cache, manifest and Blueprint, restore, retention and GC, workspace safety,
decoupled upload queue, R2/S3 transport adapter, and remote disaster-recovery
restore).

Remaining follow-up, not in this change:

- Wire capture and restore into `ndl-agent` and `ndl-control`, replacing the old
  `internal/ctbackup` tar plus fixed-chunk `internal/backuppack` creation path
  for new backups. The old path stays only to restore legacy backups.
- Backups UI redesign for the new protection summary and local/remote status,
  plus Backup Workspace settings.
- Additive database migrations for the new restore-point state model.
- Docker inventory collection and application-aware hook execution on the agent.
- On-hardware end-to-end validation with real LXC workloads and a real R2 bucket
  (zero-freeze assertion via cgroup inspection, multipart resume, and the full
  disposable-workload matrix). These require a Debian hypervisor host and R2
  credentials and cannot run in a CI container.
```
