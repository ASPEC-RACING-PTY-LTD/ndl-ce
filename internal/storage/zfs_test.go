package storage

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/hostos"
)

func TestZFSImportRefusesForceAndAcceptsGUID(t *testing.T) {
	if err := RefuseForceImport(true); err == nil {
		t.Fatal("force")
	}
	if _, err := ParseZPoolGUID("force"); err == nil {
		t.Fatal("force guid")
	}
	if _, err := ZFSImportArgv("abc"); err == nil {
		t.Fatal("name is not a guid")
	}
	argv, err := ZFSImportArgv("1234567890123456789")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != ZPoolBin || strings.Contains(joined, "-f") || strings.Contains(joined, "bash") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "-N") || !strings.Contains(joined, "-R") || !strings.Contains(joined, ZFSMountRoot) {
		t.Fatal(joined)
	}
}

func TestZFSCreateRefusesRootDisk(t *testing.T) {
	if _, err := ParseDiskLocator("/dev/sda", "/dev/sda"); err == nil {
		t.Fatal("root")
	}
	if _, err := ParseDiskLocator("/", ""); err == nil {
		t.Fatal("slash")
	}
	d, err := ParseDiskLocator("/dev/disk/by-id/wwn-0x5000", "")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := ZFSCreatePoolArgv("tank", []string{d})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "-f") {
		t.Fatal(argv)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-m") || !strings.Contains(joined, ZFSMountRoot) {
		t.Fatal(joined)
	}
}

func TestZFSCapabilitiesIncrementalSend(t *testing.T) {
	c := ZFSCapabilities()
	if !c.Snapshots || !c.IncrementalSend || !c.VolumeCreate {
		t.Fatalf("%+v", c)
	}
	d := DirectoryCapabilities(true, false)
	if d.IncrementalSend {
		t.Fatal("directory incremental send must stay false")
	}
}

func TestZFSVolumeArgv(t *testing.T) {
	ds, err := DatasetName("tank", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	zvol, err := ZFSCreateZVolArgv(ds, 8<<30)
	if err != nil || !strings.Contains(strings.Join(zvol, " "), "volmode=dev") {
		t.Fatalf("%v %v", zvol, err)
	}
	mount := ZFSMountRoot + "/pool/volumes/container-root/" + "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	dsArgv, err := ZFSCreateDatasetArgv(ds, mount, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dsArgv[0], ZFSBin) {
		t.Fatal(dsArgv)
	}
	joined := strings.Join(dsArgv, " ")
	if !strings.Contains(joined, "refquota=8589934592") || !strings.Contains(joined, "quota=8589934592") {
		t.Fatalf("dataset must set quota: %v", dsArgv)
	}
	grow, err := ZFSSetQuotaArgv(ds, 16<<30)
	joinedGrow := strings.Join(grow, " ")
	if err != nil || !strings.Contains(joinedGrow, "refquota=17179869184") {
		t.Fatalf("grow %v %v", grow, err)
	}
	if strings.Contains(joinedGrow, " quota=") {
		t.Fatalf("set should use refquota only: %v", grow)
	}
	snap, err := ZFSSnapshotArgv(ds, "s1")
	if err != nil {
		t.Fatal(err)
	}
	rb, err := ZFSRollbackArgv(ds, "s1")
	if err != nil || strings.Contains(strings.Join(rb, " "), "-f") || strings.Contains(strings.Join(rb, " "), "-R") {
		t.Fatal(rb, err)
	}
	send, err := ZFSSendArgv(ds, "s1", "")
	if err != nil || strings.Contains(strings.Join(send, " "), "-f") {
		t.Fatal(send, err)
	}
	inc, err := ZFSSendArgv(ds, "s2", ds+"@s1")
	if err != nil || !strings.Contains(strings.Join(inc, " "), "-i") {
		t.Fatal(inc, err)
	}
	_ = snap
}

func TestHostVolumePathAndSendDest(t *testing.T) {
	zvol := "/dev/zvol/tank/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	p, err := HostVolumePath(BackendZFS, ZFSMountRoot+"/1", zvol)
	if err != nil || p != zvol {
		t.Fatal(p, err)
	}
	if _, err := HostVolumePath(BackendZFS, ZFSMountRoot+"/1", "/dev/sda"); err == nil {
		t.Fatal("generic /dev")
	}
	if _, err := ParseSendDest("/etc/passwd.zfs"); err == nil {
		t.Fatal("etc")
	}
	if _, err := ParseSendDest("/var/lib/ndl/backups/a.zfs"); err != nil {
		t.Fatal(err)
	}
	if QEMUFormat(BackendZFS, FormatZvol) != "raw" {
		t.Fatal("qemu format")
	}
}

func TestEvaluateZFSRuntimeDetectsUserland(t *testing.T) {
	unsupported := EvaluateZFSRuntime(hostos.Platform{ID: "ubuntu", VersionID: "24.04", Architecture: "amd64"}, true)
	if unsupported.HostSupported || unsupported.Status != ZFSRuntimeUnsupported {
		t.Fatalf("%+v", unsupported)
	}
	missing := EvaluateZFSRuntime(hostos.Platform{ID: "debian", VersionID: "13", Architecture: "amd64"}, false)
	if !missing.HostSupported || missing.Status != ZFSRuntimeNotInstalled || missing.Reason != ZFSMissing {
		t.Fatalf("%+v", missing)
	}
	ready := EvaluateZFSRuntime(hostos.Platform{ID: "debian", VersionID: "13", Architecture: "amd64"}, true)
	if !ready.HostSupported || ready.Status != ZFSRuntimeInstalled {
		t.Fatalf("%+v", ready)
	}
}

func TestZFSUserlandInstalledRequiresTypedBins(t *testing.T) {
	seen := map[string]bool{}
	ok := zfsBinsPresent(func(name string) (os.FileInfo, error) {
		seen[name] = true
		if name == ZPoolBin || name == ZFSBin {
			return fakeFileInfo{}, nil
		}
		return nil, os.ErrNotExist
	})
	if !ok || !seen[ZPoolBin] || !seen[ZFSBin] {
		t.Fatalf("installed=%v seen=%v", ok, seen)
	}
	if zfsBinsPresent(func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }) {
		t.Fatal("missing bins")
	}
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "zpool" }
func (fakeFileInfo) Size() int64        { return 1 }
func (fakeFileInfo) Mode() os.FileMode  { return 0o755 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

func TestParseZPoolCapacity(t *testing.T) {
	props := ParseZPoolGet("size\t3985729650688\nallocated\t1007616\nfree\t3985728643072\nguid\t2835117654106017634\nhealth\tONLINE\n")
	if props["guid"] != "2835117654106017634" || props["health"] != "ONLINE" {
		t.Fatalf("%+v", props)
	}
	cap := ZPoolCapacityFromProps(props)
	if cap.TotalBytes == nil || *cap.TotalBytes != 3985729650688 {
		t.Fatalf("total %+v", cap)
	}
	if cap.UsableBytes == nil || *cap.UsableBytes != 3985728643072 {
		t.Fatalf("usable %+v", cap)
	}
	if cap.AllocatedBytes == nil || *cap.AllocatedBytes != 1007616 {
		t.Fatalf("alloc %+v", cap)
	}
	argv, err := ZFSCapacityArgv("storage")
	if err != nil || argv[0] != ZPoolBin || strings.Contains(strings.Join(argv, " "), "-f") {
		t.Fatal(argv, err)
	}
}

func TestZFSObserveReportsCapacityForHealthyPool(t *testing.T) {
	e := ZFSEngine{Run: func(_ context.Context, argv []string) (string, error) {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "status") {
			return "  pool: storage\n state: ONLINE\nerrors: No known data errors\n", nil
		}
		if strings.Contains(joined, "get") {
			return "size\t1000\nallocated\t100\nfree\t900\nguid\t1234567890\nhealth\tONLINE\n", nil
		}
		return "", fmt.Errorf("unexpected %v", argv)
	}}
	obs := e.ObserveHints(t.Context(), []PoolHint{{
		PoolID: "11111111-1111-4111-8111-111111111111", BackendType: BackendZFS,
		RootPath: ZFSMountRoot + "/1", Backing: BackingIdentity{FSUUID: "1234567890", Device: "storage", FSType: BackendZFS},
	}})
	if len(obs) != 1 || obs[0].Status != StatusAvailable {
		t.Fatalf("%+v", obs)
	}
	if obs[0].Capacity.TotalBytes == nil || *obs[0].Capacity.TotalBytes != 1000 {
		t.Fatalf("capacity %+v", obs[0].Capacity)
	}
	if obs[0].Capacity.UsableBytes == nil || *obs[0].Capacity.UsableBytes != 900 {
		t.Fatalf("usable %+v", obs[0].Capacity)
	}
}

func TestZFSObserveDeviceMissingIsHonest(t *testing.T) {
	e := ZFSEngine{Run: func(_ context.Context, argv []string) (string, error) {
		return "/dev/zfs and /proc/self/mounts are required.", fmt.Errorf("exit 1")
	}}
	res, err := e.observeOne(t.Context(), ZFSOp{Action: "observe", Name: "storage", GUID: "1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnavailable || res.Reason != ZFSDeviceMissing {
		t.Fatalf("%+v", res)
	}
	if res.Capacity.UsableBytes != nil {
		t.Fatal("unavailable must not report capacity")
	}
}

func TestZFSObservePulledDiskStaysUnavailable(t *testing.T) {
	e := ZFSEngine{SkipHostCmds: true, Installed: boolPtr(false)}
	obs := e.ObserveHints(t.Context(), []PoolHint{{
		PoolID: "11111111-1111-4111-8111-111111111111", BackendType: BackendZFS,
		RootPath: ZFSMountRoot + "/1", Backing: BackingIdentity{FSUUID: "1", Device: "tank", FSType: BackendZFS},
	}})
	if len(obs) != 1 || obs[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs[0].Capacity.UsableBytes != nil {
		t.Fatal("unavailable must not report zero capacity")
	}
	if !strings.Contains(obs[0].Reason, "Directory storage remains first-class") && !strings.Contains(obs[0].Reason, "not installed") {
		t.Fatal(obs[0].Reason)
	}
}

func TestZFSSkipHostCmdsExecAndSendDoNotSucceed(t *testing.T) {
	e := ZFSEngine{SkipHostCmds: true}
	res, err := e.Apply(t.Context(), ZFSOp{
		Action: "create-pool", PoolID: "p1", Name: "tank", Disks: []string{"/dev/disk/by-id/wwn-0x5000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == StatusAvailable {
		t.Fatalf("SkipHostCmds must not claim available: %+v", res)
	}
	send, err := e.Apply(t.Context(), ZFSOp{
		Action: "send", Name: "tank", VolumeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Snapshot: "s1", DestPath: "/var/lib/ndl/backups/a.zfs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if send.Status == StatusAvailable {
		t.Fatalf("send SkipHostCmds must not succeed: %+v", send)
	}
	if err := e.sendTo(t.Context(), []string{ZFSBin, "send", "tank/ds@s1"}, "/var/lib/ndl/backups/a.zfs"); err == nil {
		t.Fatal("sendTo must error under SkipHostCmds")
	}
}

func boolPtr(v bool) *bool { return &v }
