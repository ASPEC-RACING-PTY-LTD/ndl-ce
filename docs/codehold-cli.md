# CodeHold CLI in containers

Every system container can have `ch`, CodeHold's command line client (a git
wrapper with its own credential store: `ch clone`, `ch pull`, `ch push`,
`ch auth login`, issues, pull requests). No-dal installs it as the
**CodeHold CLI (ch)** extra, the same way as Git or Docker, so it shows up
under each container's Extensions and in the create form's Extras step.

It is separate from [CodeHold runners](codehold.md). This step installs only
the `ch` binary. It does not register Actions runners.

## What the step does

In the container, through the same `lxc-attach` path as every other extra:

1. If `/usr/local/bin/ch --version` works, it stops here (unless this is an
   update).
2. If `curl` or the CA bundle is missing, it installs `curl` and
   `ca-certificates` with apt-get (apk on Alpine). Otherwise the package
   manager is not touched.
3. It downloads `<CodeHold address>/downloads/ch-linux-<amd64|arm64>` over
   HTTPS only (`curl --proto =https`, redirects to HTTPS only, three retries)
   to a temporary file next to `/usr/local/bin/ch`.
4. It sets the file to mode 0755 and runs `--version` on it. Only a binary
   that runs is renamed over `/usr/local/bin/ch`, in one step, so a failed or
   broken download never replaces a working `ch`. An update that downloads
   the same binary leaves the installed one alone and reports it as current.

The CPU type comes from the container's image. CodeHold's `install/ch.sh`
script is not used: No-dal runs fixed commands in containers, never a
downloaded script as root.

If the server can't be reached, the extra is marked failed with curl's
reason. The container is still created and everything else is still
installed. Retry it from the container's Extensions page.

`/usr/local/bin` is on the default `PATH` for root and normal users on
Debian, Ubuntu and Alpine, so `ch` works for everyone in the container.

## Signing in

Nobody is signed in by No-dal. Each user signs in themselves, inside the
container:

```sh
ch auth login --server https://git.aspecracing.com.au
```

No tokens or passwords are used, stored or logged by this step.

## Settings

On Workloads, choose **CodeHold CLI**. From there you can change:

- **CodeHold address.** The default is `https://git.aspecracing.com.au`. Only
  `https://` addresses are accepted.
- **Install on new containers.** This is on by default. Turn it off to stop
  new containers getting `ch`.
- **Containers at a time** (default 5) and **time limit per container**
  (default 600 seconds) for bulk installs.

To leave `ch` out of a single new container, untick **CodeHold CLI (ch)** in
the create form's Extras step. Over the API, send `"codehold_cli": false`
with `POST /api/v1/workloads`. Leaving it out follows the setting.

## Installing or updating it everywhere

In the same dialog:

1. Choose **Dry run**. It lists every system container, its status, whether
   `ch` is installed and which version, and what would happen. Nothing
   changes.
2. Choose **Install now**. With **Update containers that already have ch**
   ticked, the button reads **Install or update now**, and containers that
   already have `ch` get the server's current binary.

Containers that are not running are listed and skipped. Tick **Include
containers that are not running** to start each one for the install and stop
it again afterwards. Images other than Debian, Ubuntu or Alpine are listed as
unsupported.

The summary counts installed, updated, already current and failed
containers, with each failure's reason. Each run is recorded as a
`codehold-cli.rollout` task and in the audit log.

From a shell on the node:

```sh
nodalctl codehold-cli rollout --dry-run
nodalctl codehold-cli rollout                    # install where missing
nodalctl codehold-cli rollout --update           # also update existing installs
nodalctl codehold-cli rollout --include-stopped --concurrency 3 --timeout 300
nodalctl codehold-cli rollout --only ID,ID       # just these containers
nodalctl codehold-cli status                     # the latest run
nodalctl codehold-cli settings --auto-install false
```

The AI assistant can plan the same run. A question such as "is ch installed on
my LXCs?" becomes a dry run. "Install or update the CodeHold CLI on all
containers" becomes a rollout that waits for your approval.

API: `GET /api/v1/codehold-cli`, `PUT /api/v1/codehold-cli/settings`,
`POST /api/v1/codehold-cli/rollout` (`dry_run`, `update`, `include_stopped`,
`concurrency`, `timeout_seconds`, `workload_ids`) and
`GET /api/v1/codehold-cli/rollout`. A dry run needs `compute.read`.
Installing and changing settings need `compute.modify`.

## CodeHold behind Cloudflare Access

Downloads need no sign-in today. If CodeHold is ever put behind Cloudflare
Access, downloads will need an Access service token. The hook for it is
`codeholdCLIAccess` in `internal/httpapi/codehold_cli.go`, and it is off. To
turn it on, have it return the client ID and secret from secret storage, for
example the sealed Access token the runners integration already keeps. The
agent then writes them to a 0600 file in the container's `/tmp`, through
stdin. curl reads them with `-H @file`, and the file is deleted after the
download. The token never appears on a command line or in a log.

## Extending it

Extras that are downloads rather than distribution packages are marked
`Download` in `internal/guestextras`. They are installed by
`installDownloadExtra` in `internal/lxc/guest_extras.go`. A later opt-in
`codehold-runner` extra would add a catalogue entry (not a default) and a
case there.
