# CodeHold runners

CodeHold runners lets this node host CodeHold Actions runners. No-dal joins
CodeHold as a **host controller** (the third way in
[runner-controller.md](runner-controller.md)): CodeHold keeps each runner's
desired state, No-dal makes the node match it and reports back. Runners are
created, started, stopped and removed in CodeHold; No-dal follows. Nothing
needs to change in CodeHold.

It is optional and off on a fresh install. Turn it on in Add Features; the
CodeHold Runners page appears under Compute.

Runners that run next to CodeHold in its own instance are CodeHold's
business and are not touched.

## Connecting

1. In CodeHold, open Administration, Actions, Host controllers and create a
   one-time enrollment token (`chce_...`).
2. On the CodeHold Runners page select Connect CodeHold. A CodeHold running on
   this node (in Docker on the host or in a system container) is found
   automatically and its local address filled in, so No-dal and the runners
   reach it directly instead of through public routing. Any other address
   works too, and runners can be given a different one.
3. Paste the token. If CodeHold sits behind Cloudflare Access, add the Access
   service token's client ID and secret.

No-dal exchanges the token for the controller credential (`chc_...`). The
credential, its predecessor during a rotation and the Access secret are
AES-256-GCM sealed with `/var/lib/ndl/control/keys/codehold.key` and stored in
the `secrets` schema. They are never returned to the browser or logged.

## What happens for each runner

The control plane keeps a sync with CodeHold open (CodeHold holds each request
up to 25 seconds until something changes) and reports this node's capacity and
every runner's state. For each runner CodeHold wants:

- **Running, new:** a system container `codehold-<name>-<id>` is created with
  2 vCPU, 2 GB of memory and a 50 GB disk (changeable under Runner settings),
  nesting on and Docker installed as an extension. The `codehold-runner` agent
  is installed as `/usr/local/bin/codehold-runner` and runs as
  `codehold-runner.service` in supervised mode
  (`run --registration-file /etc/codehold-runner/registration.token`).
- **Registration:** the one-time registration token from CodeHold is written to
  that root-only file inside the container; the agent registers with it and
  deletes it. It never appears on a command line or in a log.
- **Rejected credentials:** when the agent has no working credentials (exit
  status 78, or its `unauthorized` marker), No-dal reports `auth_failed` and
  hands over the new token CodeHold issues.
- **Stopped / running:** the container is stopped or started.
- **Name or limits changed:** CodeHold sends the new limits to the agent; the
  agent is restarted once it is idle.
- **Removed:** the container is deleted, then `removed` is reported. Runner
  containers CodeHold no longer lists (removed while No-dal was away) are
  deleted too.

Containers are created, started, stopped and deleted through the same code as
any other workload, and audited. A failed setup is retried after five minutes,
or at once with Retry setup now.

**Capacity** offered to CodeHold is the node's CPUs and three quarters of its
memory unless set under Runner settings. CodeHold reserves each runner's
per-job limits times its jobs at a time and refuses runners that do not fit.

**Credential rotation:** a replacement credential from CodeHold is saved and
used from the next sync; the previous one is kept until the new one is
confirmed and used again if the new one is refused. If CodeHold rejects the
credential outright the page says so and asks for a new enrollment token.

## The runner program

By default the `codehold-runner` binary is copied from the CodeHold container
on this node (`/app/downloads/codehold-runner-linux-amd64`) through the Docker
API, without starting anything in it and without any network download. When
CodeHold is not on this node, set an HTTPS address under Runner settings. A
SHA-256 checksum can be required; it is off by default, in which case the
program is trusted because it comes from that HTTPS address.

## Turning it off

If runners still have containers here, turning the feature off asks first, for
every runner, whether its container is **shut down and kept** or **shut down
and deleted**. The integration turns off only once all of that succeeded.
Kept containers stay as ordinary stopped workloads; turning CodeHold runners on
again hands them back to CodeHold.

**Force turn off** stops every runner's container at once and deletes them
after a grace period of seven days. Until then the dashboard shows a warning
(which can be dismissed) with Keep them, to cancel the deletion, and Delete
now.

Disconnect forgets the connection; it is refused while runners still have
containers here.

## Permissions

Viewing needs `compute.read`; connecting, settings, retrying, turning off and
the grace-period actions need `feature.manage`.
