# Platform backup and disaster recovery

Workload backups protect what runs on NDL-CE. The platform backup protects
NDL-CE itself, so that after losing the host you can install NDL-CE on a new
one, connect the same bucket and get everything back.

## What is kept

Once a day, and when an Owner chooses **Backups → Disaster recovery → Back up
platform now**, NDL-CE builds one archive:

- a `pg_dump` of the control database: accounts, roles, settings, secrets,
  workload and game server definitions, storage pools, networks and their
  mappings, backup targets, policies and the catalogue of restore points;
- the files in `/etc/ndl`;
- the backup repository key, without which no workload restore point can be
  decrypted;
- a manifest: schema level, host, workloads with their restore points,
  targets (without credentials), pools, networks and nodes.

The archive is encrypted on the host with AES-256-GCM and a random 32-byte
snapshot key, then stored in the platform target (the first object storage
target, or the one chosen) under the fixed prefix `ndl-platform/`:

| Object | Holds |
| --- | --- |
| `ndl-platform/pointer.json` | Plaintext index: current and previous envelope, published snapshots. No secrets. |
| `ndl-platform/envelopes/<time>-<id>.json` | The snapshot key wrapped for each Owner. |
| `ndl-platform/snapshots/<time>-<id>.ndlp` | Encrypted snapshots. |
| `ndl-platform/tmp/` | Uploads not yet verified. |

Workload wipes (`backups/`) never touch `ndl-platform/`.

## Who can unlock it

The snapshot key is wrapped once per Owner (every user with the Owner role)
with a key derived from that Owner's password with Argon2id (64 MiB, 3
passes, random salt). Owners are identified in the envelope by a hash of
their sign-in name, never by name. A wrap is made or refreshed when an Owner:

- claims setup,
- signs in, and their wrap is missing or does not open with the password
  they just used,
- has their password reset.

The password is used only for that and is never stored. Owners that are
removed, disabled or demoted are left out of the next envelope.

Access to the bucket and the ability to decrypt are separate: the bucket's
credentials reveal only ciphertext and salted wraps. There is no recovery
key; at least one Owner must remember their password.

## How it is written

Nothing already published is overwritten before its replacement is verified.

- **Envelopes** are written as new objects, read back, compared byte for byte,
  checked against the local key (and unlocked with the Owner's password when
  it is at hand), and only then does the pointer switch to them. The previous
  envelope stays as a fallback; older ones are deleted. Only the last two
  verified envelopes exist.
- **Snapshots** are uploaded to `tmp/`, read back and checked against their
  SHA-256, decrypted and compared, then written under `snapshots/`, read back
  and checked again, and only then added to the pointer. 7 daily and 4 weekly
  snapshots are kept.

## Recoverability

After each platform backup NDL-CE checks every workload a policy protects:

- the quick check looks for the manifest and location map of its newest
  offsite restore points (the newest three);
- once a week (and with **Verify deeply**) it downloads and authenticates the
  manifest and location map and looks for a sample of the packs they
  reference.

Each workload is **recoverable**, **stale** (its newest offsite restore point
is older than 48 hours) or **missing**. The Backups page and the dashboard show
"N of M workloads recoverable". A failed platform backup, a failed envelope
and fewer recoverable workloads raise an event and are sent to the
notification channels.

## Rotating the key

**Rotate key** (Owner, with their password) makes a new snapshot key, wraps it
for that Owner, publishes the envelope and backs up at once. Other Owners can
unlock the new key after their next sign-in. Snapshots of the old key are
deleted only after one with the new key is verified; until then the previous
envelope still opens them.

Limitations, by design:

- Copies of envelopes or snapshots that someone already downloaded cannot be
  reached by NDL-CE. They stay readable with the passwords that unlocked them
  at the time, whatever is rotated or deleted later.
- After a password change NDL-CE publishes the envelope twice, both times
  verified, so neither the current envelope nor the fallback holds the old
  password's wrap. Copies taken before that are covered by the point above.

## Recovering

See [Recovery](recovery.md).
