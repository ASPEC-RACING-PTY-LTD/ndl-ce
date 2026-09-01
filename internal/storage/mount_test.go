package storage

import "testing"

const sampleMounts = `
36 1 8:1 / / rw - ext4 /dev/sda1 rw,uuid=ROOTFS
45 36 8:16 / /mnt/data rw - ext4 /dev/sdb1 rw,uuid=DATAFS
50 36 0:47 / /mnt/share rw - nfs 192.168.1.5:/export rw
`

func TestCoveringMountPrefersLongest(t *testing.T) {
	ms := ParseMountinfo(sampleMounts)
	m, ok := CoveringMount("/mnt/data/pool", ms)
	if !ok || m.MountPoint != "/mnt/data" {
		t.Fatalf("%+v", m)
	}
	root, ok := CoveringMount("/var/lib/ndl/storage/local", ms)
	if !ok || root.MountPoint != "/" {
		t.Fatalf("root cover %+v", root)
	}
}

func TestSharedFS(t *testing.T) {
	if !SharedFS("nfs4") || SharedFS("ext4") {
		t.Fatal("shared detection")
	}
}

func TestSameBacking(t *testing.T) {
	a := BackingIdentity{FSUUID: "DATAFS", Dev: 10}
	b := BackingIdentity{FSUUID: "ROOTFS", Dev: 1}
	if SameBacking(a, b) {
		t.Fatal("uuid mismatch")
	}
	if !SameBacking(a, BackingIdentity{FSUUID: "DATAFS", Dev: 99}) {
		t.Fatal("uuid should win")
	}
}

func TestQEMUCreateArgvTyped(t *testing.T) {
	argv, err := QEMUCreateArgv("/usr/bin/qemu-img", FormatQCOW2, "/pool/vol.qcow2", 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 6 || argv[1] != "create" || argv[2] != "-f" || argv[3] != "qcow2" {
		t.Fatalf("%v", argv)
	}
	if _, err := QEMUCreateArgv("/usr/bin/qemu-img", "cow;rm -rf", "/x", 1<<20); err == nil {
		t.Fatal("injected format")
	}
	if _, err := QEMUCreateArgv("/usr/bin/qemu-img", FormatQCOW2, "/x", 0); err == nil {
		t.Fatal("zero size")
	}
}
