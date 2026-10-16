# Caddy and Nginx

Network has a Caddy page and an Nginx page when those features are on (Add
Features; both are off on a fresh install). Each finds the proxy on this node
and in running system containers and lists its sites, grouped by machine:
the names a site answers, where it sends requests, its TLS mode, the file it
comes from and the workload behind it.

Turning either feature off only hides its page and stops the scan. The proxy
and its sites are not changed.

## Sites No-dal manages

Sites added here get a file of their own, starting with a "Managed by No-dal"
line:

- Caddy: `/etc/caddy/ndl.d/<name>.caddy`. The first site adds
  `import /etc/caddy/ndl.d/*.caddy` to the end of `/etc/caddy/Caddyfile`. A
  Caddyfile that is one site without braces is not changed; wrap it in a site
  block first.
- Nginx: `/etc/nginx/conf.d/ndl-<name>.conf`, which Debian's `nginx.conf`
  already includes. If it does not, No-dal refuses and says which line to add.

Other sites are shown read-only and never edited. A domain another site
already serves is refused.

Every change is written, then checked with the proxy's own validator
(`caddy validate` or `nginx -t`) and loaded with a reload. If the check or the
reload fails, the previous files are put back and the validator's output is
shown. Domains, upstreams and certificate paths are matched against strict
patterns before anything is written, so a value cannot add configuration of
its own. Upstreams must be `http://` or `https://` with a port; link-local
addresses such as `169.254.169.254` are refused.

Files inside a container are read and written through the container's root
with `openat2(RESOLVE_IN_ROOT)`, so links inside the container cannot lead to
files on the host.

TLS: Caddy sites use automatic HTTPS, Caddy's internal CA, or plain HTTP.
Nginx sites use plain HTTP, or HTTPS with certificate files already on that
machine (port 80 then redirects to HTTPS).

## Install and service

Install puts the proxy on a machine with apt and starts it. Reload, restart,
start and stop act on its systemd service; reload, restart and start check the
configuration first. Check configuration shows the validator's output.

Changes need the network.apply grant (operators and admins); viewing needs
network.read.
