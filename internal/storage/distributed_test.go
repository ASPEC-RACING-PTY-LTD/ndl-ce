package storage

import (
	"strings"
	"testing"
)

func TestParseDistributedLocatorAndKeyNeverInArgv(t *testing.T) {
	mons, pool, err := ParseDistributedLocator("ceph.example:6789,10.0.0.8/rbd")
	if err != nil {
		t.Fatal(err)
	}
	if pool != "rbd" || len(mons) != 2 || mons[0] != "ceph.example:6789" {
		t.Fatalf("%v %s", mons, pool)
	}
	if _, _, err := ParseDistributedLocator("not-a-locator"); err == nil {
		t.Fatal("locator")
	}
	key := "AQBfakekeyvalue0123456789abcd=="
	if _, err := ParseCephxKey(key); err != nil {
		t.Fatal(err)
	}
	argv, err := RBDListArgv("admin", DistributedSecret+"/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.keyring", strings.Join(mons, ","), pool)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != RBDBin || ArgvContainsSecret(argv, key) || containsArg(argv, "bash") {
		t.Fatalf("%v", argv)
	}
	create, err := RBDCreateArgv("admin", DistributedSecret+"/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.keyring", strings.Join(mons, ","), pool, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 1<<30)
	if err != nil || ArgvContainsSecret(create, key) {
		t.Fatalf("%v %v", create, err)
	}
}

func TestFakeRBDHandleAndClusterDown(t *testing.T) {
	up := true
	e := DistributedEngine{SkipHostCmds: true, ClusterUp: &up}
	res, err := e.Apply(t.Context(), DistributedOp{
		Action: "create-volume", PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Locator: "mon.example/rbd", VolumeID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		Class: ClassVMDisk, SizeBytes: 1 << 30, CephxKey: "AQBfakekeyvalue0123456789abcd==",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == StatusAvailable {
		t.Fatalf("SkipHostCmds must not claim live map available: %+v", res)
	}
	if res.Status != StatusUnavailable || res.BackendType != BackendDistributed || res.Kind != KindBlock {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Reason, "rbd map was not run") && !strings.Contains(res.Reason, "Fake RBD") {
		t.Fatalf("reason must say map was not run: %q", res.Reason)
	}
	if err := ValidateRBDPath(res.BackendRef); err != nil {
		t.Fatal(err)
	}
	if ArgvContainsSecret(res.Argv, "AQBfakekeyvalue0123456789abcd==") {
		t.Fatalf("key leaked %v", res.Argv)
	}
	down := false
	e.ClusterUp = &down
	obs, err := e.Apply(t.Context(), DistributedOp{
		Action: "observe", PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Locator: "mon.example/rbd",
		CephxKey: "AQBfakekeyvalue0123456789abcd==",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != StatusUnavailable || !strings.Contains(obs.Reason, "unavailable") && obs.Reason != ClusterDownMsg {
		t.Fatalf("%+v", obs)
	}
	vol, err := e.Apply(t.Context(), DistributedOp{
		Action: "create-volume", PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Locator: "mon.example/rbd", VolumeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		Class: ClassVMDisk, SizeBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if vol.Status != StatusUnavailable {
		t.Fatalf("cluster down must leave volumes unavailable %+v", vol)
	}
}

func TestOSDBringUpRefusesRootAndDoesNotStartByDefault(t *testing.T) {
	if _, err := ParseOSDDisk("/", ""); err == nil {
		t.Fatal("root")
	}
	if _, err := ParseOSDDisk("/dev/sda", "/dev/sda"); err == nil {
		t.Fatal("root disk")
	}
	disk, err := ParseOSDDisk("/dev/disk/by-id/wwn-0x5000", "")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := OSDCreateArgv(disk)
	if err != nil || argv[0] != CephVolumeBin || containsArg(argv, "bash") || containsArg(argv, "kubelet") {
		t.Fatalf("%v %v", argv, err)
	}
	e := DistributedEngine{SkipHostCmds: true}
	res, err := e.Apply(t.Context(), DistributedOp{Action: "osd-create", Disk: disk})
	if err != nil {
		t.Fatal(err)
	}
	if res.OSDStarted || res.Status == StatusAvailable {
		t.Fatalf("skip host cmds must not claim OSD started %+v", res)
	}
	obs, err := e.Apply(t.Context(), DistributedOp{Action: "osd-observe"})
	if err != nil || obs.OSDStarted {
		t.Fatalf("%+v %v", obs, err)
	}
}

func TestHostVolumePathAndQEMUFormatDistributed(t *testing.T) {
	p, err := HostVolumePath(BackendDistributed, "", "/dev/rbd/rbd/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil || p != "/dev/rbd/rbd/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatal(p, err)
	}
	nbd, err := HostVolumePath(BackendDistributed, "", "/dev/nbd0")
	if err != nil || nbd != "/dev/nbd0" {
		t.Fatal(nbd, err)
	}
	if _, err := HostVolumePath(BackendDistributed, "", "/dev/sda"); err == nil {
		t.Fatal("sda")
	}
	if QEMUFormat(BackendDistributed, FormatRBD) != FormatRaw {
		t.Fatal(QEMUFormat(BackendDistributed, FormatRBD))
	}
}

func TestObserveOSDDefaultAbsent(t *testing.T) {
	running, names := ObserveOSD(func() []string { return []string{"ndl-control", "qemu-system-x86_64"} })
	if running || len(names) != 0 {
		t.Fatalf("%v %v", running, names)
	}
	running, _ = ObserveOSD(func() []string { return []string{"ceph-osd"} })
	if !running {
		t.Fatal("detected")
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
