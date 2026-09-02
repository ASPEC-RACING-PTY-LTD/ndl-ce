# Install No-dal

Phase 29 still qualifies **Debian 13 amd64** as the only Tier 1 host.
Ubuntu LTS is detected and refused. Qualification gaps (Netplan versus
systemd-networkd, packaging, DKMS) are documented. Do not treat Ubuntu
as supported until those tests pass.

Host OS, VM guest OS, system-container distribution, and OCI image are
different objects. This page is the **host** install.

The three host install paths install the same `nodal` metapackage
(`ndl-control`, `ndl-agent`, `ndl-ui`, `nodalctl`), start the same
units, and finish at the same `/setup` page. Community Edition does
not require a license key. See [ce-1.0.md](ce-1.0.md).

- Path A: existing Debian 13 amd64, add the signed repo, `apt-get install -y nodal`.
- Path B: one-line bootstrap below.
- Path C: Debian installer ISO in `packaging/iso`, same metapackage after reboot.
  First-boot setup is HTTP on port 8080 until you generate or import a
  certificate. After that the management URL is HTTPS. The ISO is not
  booted in this tree. TLS is not the default listen.

## One-line bootstrap

Inspect the script, then run it as root:

```text
curl -fsSL https://get.no-dal.com | sudo sh
```

The script checks the host, installs the signing key and signed apt
repo over HTTPS, runs `apt-get install -y nodal`, waits for
`http://127.0.0.1:8080/api/v1/health`, and prints:

```text
Open http://ADDR:8080/setup
```

`ADDR` is the first IPv4 from `hostname -I`, or `127.0.0.1`. Until TLS
is enabled the setup URL is HTTP on port 8080. After you generate or
import a certificate, the management URL is `https://ADDR/setup`.
HTTP then redirects to HTTPS except for ACME HTTP-01 challenges.

Override the repo if needed:

```text
NODAL_APT_KEY_URL=https://packages.no-dal.com/gpg
NODAL_APT_REPO=https://packages.no-dal.com/debian
```

The script does not create users, start units, or touch PostgreSQL.
Package postinst does that.

## Manual repository install

1. Install the repo signing key over HTTPS into
   `/usr/share/keyrings/nodal.gpg` (or `.asc` if the key is armored).
   Default URL: `https://packages.no-dal.com/gpg`.
2. Add the signed repo `https://packages.no-dal.com/debian` for
   Debian 13 (`trixie`) with `Signed-By` pointing at that keyring.
3. `apt-get update`
4. `apt-get install -y nodal`
5. Open `http://ADDR:8080/setup` and claim the first admin. Then enable
   TLS from Certificates (`nodalctl cert generate --cn ADDR --confirm enable-tls`)
   and use `https://ADDR/setup` going forward.

Result matches the one-line path: same packages, same units, same
`/setup` flow. No factory password is created.

Optional feature packages (`nodal-feature-oci`, `nodal-feature-gpu`,
`nodal-feature-k8s`, `nodal-feature-distributed-storage`,
`nodal-feature-ai`) are not installed by `apt-get install nodal`. Enable
them later from Settings, Features or `nodalctl feature enable oci`.
Kubernetes enable does not start kubelet.

