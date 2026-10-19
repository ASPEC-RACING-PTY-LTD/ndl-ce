# Live updates

Live updates put a new web UI, control plane or agent on a host without
building or installing packages and without pushing to Git. Packages remain
the way to release; live updates are for the changes in between.

## What can be updated, and how

| Part | How it is replaced | Interruption |
| --- | --- | --- |
| Web UI | The control plane follows `/var/lib/ndl/live/ui`, a link to the active release, on every request. Switching the link is the update. | None. Open pages show "NDL-CE was updated. Reload". Chunks of the previous release stay available, so a page loaded before the switch keeps working until it reloads. |
| Control plane (`ndl-control`) | The binary is replaced in one rename, then the service restarts. | The local control socket is held by systemd, so API calls queue during the restart (about 1–2 s). With `ndl-live enable -http-socket`, the web port (8080) is held too, so browsers queue instead of failing. Terminal and event streams reconnect. |
| Agent (`ndl-agent`) | The same, after the control plane is told nothing is running. | Workloads run as their own systemd units and keep running. Backup upload queues are on disk and resume. |
| Database schema | Not by live update by default. | A rollback cannot undo a migration, so a bundle whose control plane would apply one is refused unless it was published with `-allow-migrations`. |
| `/etc/ndl` environment files | Never. | They hold the database DSN and secrets. Settings in the database already change instantly. |
| PostgreSQL, LXC, QEMU, OCI runtime, kernel | Never. | |

Only one control plane ever runs: it holds the writer lease in PostgreSQL,
and the old process releases it as it stops; the new one takes it before it
serves.

## Safety

- **Signed.** A bundle is a gzip tar of `manifest.json`, `manifest.sig` and
  `files/`. The Ed25519 signature covers the exact manifest bytes, and the
  manifest pins every file's SHA-256 and size. Only keys in
  `/etc/ndl-live/keys/*.pub` (root-owned) are trusted. Bundles may install
  only `ui/…`, `bin/ndl-control` and `bin/ndl-agent`; paths outside those,
  links and extra files are refused.
- **Verified twice.** The control plane verifies an upload before queueing
  it; `ndl-live`, running as root, verifies it again before applying it.
- **Compatible.** A bundle built for another major.minor than the installed
  package is refused, as is one that does not know a migration the database
  already has.
- **Never under running work.** An update waits while a backup run is
  active, a backup is copying data, the repository is moving, or any
  change request is in progress. It is retried every 5 minutes.
- **Checked before and after.** Each new binary must run `version` and report
  the bundle's build before it is installed. After a restart the control
  plane must answer as that build, and the agent must answer through it,
  within 90 seconds.
- **Rolled back automatically.** If anything fails, every replaced binary is
  put back from `/var/lib/ndl/live/saved/<id>/`, restarted and checked, and
  the previous UI is restored.
- **Recovers from interruption.** The step in progress is written to
  `/var/lib/ndl/live/state.json` before it starts. After a crash or power
  cut, the next run (at boot, or every 5 minutes) restores the previous
  version.
- **Packages win.** Installing a package release replaces the binaries and
  switches back to the packaged UI (`ndl-live package-installed`).

## Turning it on

Live updates ship installed but **off**. On the host, as root:

```bash
ndl-live trust /path/to/ndl-live-signing.pub
ndl-live enable                # apply queued updates automatically
ndl-live enable -http-socket   # optional: also hold the web port across restarts (restarts the control plane once)
```

`ndl-live disable` stops automatic application; the running version stays.

## Publishing an update

Once, on the build machine:

```bash
go run ./cmd/ndl-live keygen ~/.config/ndl-live
```

Keep `ndl-live-signing.key` private; give hosts the `.pub` file.

Then, after a change:

```bash
scripts/ndl-live-publish.sh ui                 # only the web UI
scripts/ndl-live-publish.sh control agent --notes "fix upload signing"
scripts/ndl-live-publish.sh                    # everything
```

It builds what you name, stamps the build ID into it, signs the bundle into
`out/live/` and then:

- on the host itself, as root, queues it with `ndl-live install`;
- elsewhere, with `NDL_LIVE_URL` and a SuperAdmin API token in
  `NDL_LIVE_TOKEN`, uploads it to `POST /api/v1/live-updates/bundles`;
- otherwise leaves the file for you to upload under **Updates → Live
  updates → Upload update**.

## Status, history and rollback

**Updates → Live updates** (SuperAdmins) shows whether automatic updates are on,
what is running, anything queued or waiting and what it waits for, trusted
keys, and the history with results and errors. **Roll back last update**
undoes the newest applied update. On the host, `ndl-live status` prints the
same state and `ndl-live rollback` rolls back directly.

## Files

| Path | What |
| --- | --- |
| `/etc/ndl-live/keys/*.pub` | Trusted signing keys |
| `/etc/ndl-live/enabled` | Present when automatic updates are on |
| `/var/lib/ndl/live/inbox/` | Queued bundles and rollback requests (writable by the control plane) |
| `/var/lib/ndl/live/releases/<id>/` | Verified releases; the newest five are kept |
| `/var/lib/ndl/live/saved/<id>/` | The binaries each update replaced, for rollback |
| `/var/lib/ndl/live/ui` | Link to the live web UI; absent means the packaged UI |
| `/var/lib/ndl/live/state.json` | Current release, step in progress, history |
| `ndl-live.path`, `ndl-live.timer`, `ndl-live.service` | Apply on queue, retry every 5 minutes, recover after boot |
| `ndl-control-http.socket` | Optional systemd-held web port |
