package storage

import "testing"

func TestNormalizeRejectsTraversalToForbidden(t *testing.T) {
	_, err := Normalize("/var/lib/ndl/storage/../../etc")
	if err == nil {
		t.Fatal("expected traversal reject")
	}
	_, err = Normalize("relative/path")
	if err != ErrNotAbsolute {
		t.Fatalf("relative: %v", err)
	}
	got, err := Normalize("/var/lib/ndl/storage/default")
	if err != nil || got != "/var/lib/ndl/storage/default" {
		t.Fatalf("safe path: %q %v", got, err)
	}
}

func TestForbiddenRoots(t *testing.T) {
	for _, p := range []string{"/", "/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run", "/root", "/tmp", "/tmp/ndl", "/var/lib/ndl", "/var/lib/ndl/agent", "/var/lib/postgresql/16"} {
		if !Forbidden(p) {
			t.Fatalf("must reject %s", p)
		}
	}
	for _, p := range []string{"/var/lib/ndl/storage", "/var/lib/ndl/storage/local", "/mnt/data", "/srv/ndl"} {
		if Forbidden(p) {
			t.Fatalf("must allow %s", p)
		}
	}
}

func TestOverlaps(t *testing.T) {
	if !Overlaps("/var/lib/ndl/storage/a", "/var/lib/ndl/storage/a/b") {
		t.Fatal("nested")
	}
	if Overlaps("/var/lib/ndl/storage/a", "/var/lib/ndl/storage/b") {
		t.Fatal("siblings")
	}
}

func TestJoinUnderRejectsEscape(t *testing.T) {
	if _, err := JoinUnder("/var/lib/ndl/storage/p", "../etc/passwd"); err == nil {
		t.Fatal("escape")
	}
	got, err := JoinUnder("/var/lib/ndl/storage/p", "volumes/vm-disk/id.qcow2")
	if err != nil || got != "/var/lib/ndl/storage/p/volumes/vm-disk/id.qcow2" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestDisplayNameSanitizes(t *testing.T) {
	if DisplayName("../../etc/passwd") != "passwd" {
		t.Fatal(DisplayName("../../etc/passwd"))
	}
	if DisplayName("") != "upload" {
		t.Fatal("empty")
	}
}

func TestDirectoryCapabilitiesNoIncrementalSend(t *testing.T) {
	c := DirectoryCapabilities(true, false)
	if c.IncrementalSend {
		t.Fatal("Directory must not advertise incremental send")
	}
	if !c.Snapshots {
		t.Fatal("Directory VM disks support external qcow2 overlay snapshots")
	}
}
