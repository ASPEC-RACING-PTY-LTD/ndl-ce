# Debian installer ISO

This is install path C. Paths A (existing Debian), B (one-line
bootstrap), and C (this ISO) install the same `nodal` metapackage and
finish at the same `/setup` page.

The image is Debian 13 amd64. It does not install Ubuntu. Ubuntu LTS
is not a qualified host in this phase.

Build with mkosi when extra disks and loop devices are available:

```text
mkosi --directory packaging/iso
```

Cloud runners do not boot the ISO. The config is the contract: Debian
13, `nodal` metapackage, no Netplan dual-write.
