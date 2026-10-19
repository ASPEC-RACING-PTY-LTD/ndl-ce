# Project deployments

**Docker → Deployments** updates Docker Compose projects that live in
system containers. You push to a repository; NDL-CE shows which projects
have new commits; you deploy one, several or all of them with a button, and
watch the output live. No terminal needed.

Docker Management must be on. Deployments run in system containers only,
never on the host or in VMs.

## Projects

A project is a folder in a system container plus the commands that update
it. One container can hold any number of projects.

- **Found in containers** lists running Compose projects (from Docker's
  `com.docker.compose.project.working_dir` label) that have no commands yet.
  **Add** fills in the container and folder.
- **Add project** adds any folder by hand, even one that is not running.

New projects start with the commands in **Settings** (by default
`git pull origin main && docker compose up -d --build --force-recreate`).
Everything is editable.

### Commands

Each command runs with `bash` in the working folder, in order, as written:
`&&`, pipes, scripts and anything else bash accepts. There is no list of
allowed commands. They run:

- **as the folder's owner** unless **Run as** names a user, so Git uses that
  user's credentials and files keep their owner;
- with a login-like environment (`HOME`, `USER`, `PATH` including
  `/usr/local/bin`) plus the project's **Environment** variables. Variable
  values are stored as secrets, never shown again, and passed to the
  container on standard input, never on a command line;
- with `GIT_TERMINAL_PROMPT=0`, so a missing credential fails instead of
  hanging, and `BUILDKIT_PROGRESS=plain` (unless set) for readable build
  logs;
- under a **time limit** (30 minutes by default).

A deployment **succeeds when every command exits with 0**. The first command
that fails stops it, unless that command is marked **Continue on error**.
NDL-CE never rolls a deployment back and never decides success from health
checks. After a successful deployment the project's containers' state and
Docker health are recorded next to it as information only.

For CodeHold repositories, use `ch pull origin main` in the commands, or run
`ch auth setup-git` once in the container so plain `git` uses CodeHold's
stored credentials.

## Finding updates

With **Check the Git remote for new commits** on, NDL-CE asks the remote for
the branch's latest commit every 10 minutes (per project or in Settings),
with `git ls-remote`. That is one small request; nothing is downloaded.
Only when the remote has a commit the folder does not have is it fetched
(`git fetch`, which never changes the working tree), to count how far behind
the folder is and show the newest commit's message and author.

The provider decides which credentials Git uses:

| Provider | How |
| --- | --- |
| **Auto** (default) | CodeHold when `ch` is in the container and the remote is the CodeHold server (from Workloads → CodeHold CLI), or no CodeHold address is set and the remote is not GitHub; GitHub when the remote is github.com and `gh` is installed; otherwise Git. |
| **CodeHold** | `ch ls-remote` / `ch fetch`, with ch's encrypted credentials. |
| **GitHub** | Git with `gh auth git-credential` as the credential helper. |
| **Git** | Plain Git and whatever credentials the user has. |

If the chosen CLI is missing, Git is used and the project says so. If the
remote cannot be reached, it is tried again with plain Git, then with the
**Fallback remote** (for example a GitHub mirror of a CodeHold repository).

Each project shows **Up to date**, **N behind**, **N ahead** (the folder has
commits the remote does not) and **Uncommitted changes**. These are
information: nothing is blocked. **Check** asks at once; **Check all** checks
every project in the background.

Finding an update never deploys it, unless the project has **Deploy new
commits automatically** on. Then each new commit is deployed once,
optionally only inside a window such as `02:00-05:00` (the host's time; it
may run past midnight). A commit found outside the window deploys when the
window opens. A failed automatic deployment is not retried for the same
commit.

## Deploying

- **Deploy** on a project, **Deploy selected**, or **Deploy all updates**.
- Each project is its own deployment; one failing never affects another.
- At most **3 at once in total** and **1 at once per container** (both in
  Settings). The rest wait in order.
- A deployment waits while a **backup of the same container** is running.
  **Deploy anyway** starts it; a project can also be set to never wait.
  Backups are never required.
- The progress bar has one segment per command, with the time so far against
  the project's usual time. The output streams live with its colours.
- **Cancel** stops a queued deployment, or a running one together with every
  process it started (see below).
- Live updates of NDL-CE wait for running deployments.

### Cancelling

In containers with systemd (Debian and Ubuntu images), the commands run in a
transient unit, `ndl-deploy-<id>.service`, with `KillMode=control-group`.
Cancelling stops the unit: every process in its cgroup gets SIGTERM, and
anything still running 15 seconds later gets SIGKILL, including children
that detached with `setsid` or ignore SIGTERM. Nothing outside the unit is
touched. Docker builds are cancelled with the `docker compose` client;
containers already started by Compose belong to Docker and keep running.

In containers without systemd, the process tree is frozen, sent SIGTERM and,
after the grace period, SIGKILL. A process that has already left the tree
cannot be found this way.

If the node agent restarts while a deployment runs, the deployment is marked
**Interrupted** and its unit is stopped.

## Logs and history

Every deployment keeps its **complete** output as a file on the node, in
`/var/lib/ndl/deployments` (Settings → Log folder; any absolute folder outside
system folders, for example on a data disk). Logs are never shortened. The
page shows the last 5,000 lines; **Download log** has all of it.

Retention removes whole deployments (record and log) that are older than
**30 days**, beyond the newest **50** per project, or past a **10 GB** total,
all set in Settings (0 turns a rule off). Running deployments and each
project's latest deployment are always kept.

History shows who or what started each deployment (manual, bulk, webhook,
auto), the commit before and after, how long it took, the exit code and the
log.

## Notifications

Each project chooses events: a deployment **fails**, **succeeds**, **new
commits are found**, a **bulk deployment finishes** (one summary). They go
to the notification channels under Alerts (all of them, or the ones the
project picks). The JSON has `event`, the project, container, commits,
`exit_code`, `failed_command`, `error` and the last 20 lines of output, plus
`text` and `content` with a one-line summary, so Slack and Discord incoming
webhooks show it as is.

## Push webhooks

**Settings → Push webhook → Turn on** creates a secret (shown once) and a URL:
`https://<NDL-CE address>/api/v1/hooks/deploy/<project id>`.

- CodeHold, Gitea, Forgejo and GitHub: add a push webhook with that URL,
  content type JSON and the secret. NDL-CE checks `X-Hub-Signature-256` or
  `X-Gitea-Signature`.
- CI jobs: `curl -X POST -H "Authorization: Bearer <secret>" <url>`.

A push to another branch is ignored. A push checks the project at once; it
deploys only when the project deploys automatically. Pings answer OK. Bad
signatures are refused and audited. **New secret** replaces it; **Turn off**
disables the URL.

The URL must be reachable from the Git server, for example through your
Cloudflare tunnel.

## Permissions and audit

| | User | Admin | SuperAdmin |
| --- | --- | --- | --- |
| See projects, progress, history, logs | yes | yes | yes |
| Deploy, bulk deploy, check, cancel, deploy anyway | | yes | yes |
| Add or remove projects; change commands, user, environment, Git, automation, notifications, webhooks, settings | | | yes |

The audit log records project changes (with the commands), every deployment
start and finish (project, container, folder, user, trigger, result, exit
code), cancels, bulk runs, automatic deployments and webhook calls.

## API

- `GET /api/v1/docker/deploy/projects`, `POST` to add one;
  `GET|PUT|DELETE /api/v1/docker/deploy/projects/{id}`
- `POST /api/v1/docker/deploy/projects/{id}/deploy` (`{"force": true}` to
  skip the backup wait), `/check`, `/webhook`, `GET .../deployments`
- `POST /api/v1/docker/deploy/bulk` with `{"due": true}` or
  `{"project_ids": [...]}`; `POST /api/v1/docker/deploy/check`
- `GET /api/v1/docker/deployments[?active=1]`, `GET .../{id}`,
  `GET .../{id}/log?offset=N`, `GET .../{id}/log/download`,
  `POST .../{id}/cancel`, `POST .../{id}/force`
- `GET|PUT /api/v1/docker/deploy/settings`
- `POST /api/v1/hooks/deploy/{id}` (webhook, signed)
