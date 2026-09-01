package storage

import (
	"strings"
	"testing"
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
	dsArgv, err := ZFSCreateDatasetArgv(ds, mount)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dsArgv[0], ZFSBin) {
		t.Fatal(dsArgv)
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

func boolPtr(v bool) *bool { return &v }
