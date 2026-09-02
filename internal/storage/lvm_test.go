package storage

import (
	"context"
	"strings"
	"testing"
)

func TestLVMCapabilitiesNoIncrementalSend(t *testing.T) {
	c := LVMCapabilities()
	if !c.Snapshots || c.IncrementalSend || !c.VolumeCreate {
		t.Fatalf("%+v", c)
	}
	if ZFSCapabilities().IncrementalSend == c.IncrementalSend {
		t.Fatal("zfs incremental send must stay distinct from lvm")
	}
}

func TestLVMArgvNeverExportsVG(t *testing.T) {
	disk, err := ParseLVMDisk("/dev/disk/by-id/wwn-0x5000", "")
	if err != nil {
		t.Fatal(err)
	}
	builders := [][]string{}
	pv, err := PVCreateArgv(disk)
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, pv)
	vg, err := VGCreateArgv("ndlvg", []string{disk})
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, vg)
	pool, err := LVCreateThinPoolArgv("ndlvg")
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, pool)
	thin, err := LVCreateThinArgv("ndlvg", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, thin)
	snap, err := LVSnapshotArgv("ndlvg", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "ndl-user-s1")
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, snap)
	merge, err := LVMergeArgv("ndlvg", "ndl-user-s1")
	if err != nil {
		t.Fatal(err)
	}
	builders = append(builders, merge)
	vgs, _ := VGSReportArgv("ndlvg")
	lvs, _ := LVSReportArgv("ndlvg")
	pvs, _ := PVSReportArgv("ndlvg")
	builders = append(builders, vgs, lvs, pvs)
	for _, argv := range builders {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "vgexport") || strings.Contains(joined, "--export") {
			t.Fatal(joined)
		}
		if strings.Contains(joined, "bash") || strings.Contains(joined, "/bin/sh") {
			t.Fatal(joined)
		}
		if err := refuseExportArgv(argv); err != nil {
			t.Fatal(argv, err)
		}
	}
}

func TestLVMCreateRefusesRootDisk(t *testing.T) {
	if _, err := ParseLVMDisk("/dev/sda", "/dev/sda"); err == nil {
		t.Fatal("root")
	}
	if _, err := ParseLVMDisk("/", ""); err == nil {
		t.Fatal("slash")
	}
	if _, err := VGCreateArgv("ndlvg", nil); err == nil {
		t.Fatal("empty disks")
	}
}

func TestLVMEngineRefusesSend(t *testing.T) {
	e := LVMEngine{SkipHostCmds: true}
	if _, err := e.Apply(t.Context(), LVMOp{Action: "send", Name: "ndlvg"}); err == nil || !strings.Contains(err.Error(), "incremental send") {
		t.Fatalf("send: %v", err)
	}
}

func TestLVMObserveMissingPVStaysUnavailable(t *testing.T) {
	e := LVMEngine{
		SkipHostCmds: false,
		Installed:    boolPtr(true),
		Run: func(_ context.Context, argv []string) (string, error) {
			joined := strings.Join(argv, " ")
			if strings.Contains(joined, "vgs") && strings.Contains(joined, "reportformat") {
				return `{"report":[{"vg":[{"vg_name":"ndlvg","vg_uuid":"AbCdEfGh0123","vg_size":"1000000000","vg_free":"500000000","vg_attr":"wz-pn-"}]}]}`, nil
			}
			if strings.Contains(joined, "pvs") {
				return `{"report":[{"pv":[{"pv_name":"/dev/sdb","vg_name":"ndlvg","pv_missing":"missing"}]}]}`, nil
			}
			return `{"report":[{"lv":[]}]}`, nil
		},
	}
	obs := e.ObserveHints(t.Context(), []PoolHint{{
		PoolID: "11111111-1111-4111-8111-111111111111", BackendType: BackendLVM,
		RootPath: LVMMountRoot + "/ndlvg", Backing: BackingIdentity{FSUUID: "AbCdEfGh0123", Device: "ndlvg", FSType: BackendLVM},
	}})
	if len(obs) != 1 || obs[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs[0].Capacity.UsableBytes != nil || obs[0].Capacity.TotalBytes != nil {
		t.Fatal("unavailable must not report zero capacity")
	}
	if !strings.Contains(obs[0].Reason, "missing") {
		t.Fatal(obs[0].Reason)
	}
}

func TestLVMObserveNotInstalledDoesNotInventAvailable(t *testing.T) {
	e := LVMEngine{SkipHostCmds: true, Installed: boolPtr(false)}
	obs := e.ObserveHints(t.Context(), []PoolHint{{
		PoolID: "11111111-1111-4111-8111-111111111111", BackendType: BackendLVM,
		RootPath: LVMMountRoot + "/ndlvg", Backing: BackingIdentity{Device: "ndlvg", FSType: BackendLVM},
	}})
	if len(obs) != 1 || obs[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs[0].Capacity.UsableBytes != nil {
		t.Fatal("unavailable must not report zero capacity")
	}
	if !strings.Contains(obs[0].Reason, "Directory storage remains first-class") {
		t.Fatal(obs[0].Reason)
	}
}

func TestLVMObserveMetadataPercentWarning(t *testing.T) {
	e := LVMEngine{
		SkipHostCmds: false,
		Installed:    boolPtr(true),
		Run: func(_ context.Context, argv []string) (string, error) {
			joined := strings.Join(argv, " ")
			if strings.Contains(joined, "vgs") && strings.Contains(joined, "reportformat") {
				return `{"report":[{"vg":[{"vg_name":"ndlvg","vg_uuid":"AbCdEfGh0123","vg_size":"1000000000","vg_free":"400000000","vg_attr":"wz--n-"}]}]}`, nil
			}
			if strings.Contains(joined, "lvs") && strings.Contains(joined, "reportformat") {
				return `{"report":[{"lv":[{"lv_name":"thinpool","lv_uuid":"lv1","lv_size":"900000000","lv_attr":"twi-aotz--","data_percent":"10.00","metadata_percent":"81.50","pool_lv":""}]}]}`, nil
			}
			return `{"report":[{"pv":[]}]}`, nil
		},
	}
	res, err := e.Apply(t.Context(), LVMOp{Action: "observe", Name: "ndlvg", PoolID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusWarning || res.MetadataPercent == nil || *res.MetadataPercent < 80 {
		t.Fatalf("%+v", res)
	}
	if res.Incremental {
		t.Fatal("incremental send must stay false")
	}
	found := false
	for _, w := range res.Warnings {
		if w == WarnLVMMetadata {
			found = true
		}
	}
	if !found {
		t.Fatal(res.Warnings)
	}
}

func TestLVMHostVolumePathAndQEMUFormat(t *testing.T) {
	dev := "/dev/ndlvg/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	p, err := HostVolumePath(BackendLVM, LVMMountRoot+"/ndlvg", dev)
	if err != nil || p != dev {
		t.Fatal(p, err)
	}
	if _, err := HostVolumePath(BackendLVM, LVMMountRoot+"/ndlvg", "/dev/sda"); err == nil {
		t.Fatal("generic /dev")
	}
	if QEMUFormat(BackendLVM, FormatThinLV) != "raw" {
		t.Fatal("qemu format")
	}
}

func TestLVMCreatePoolSkipHostCmdsDoesNotExport(t *testing.T) {
	e := LVMEngine{SkipHostCmds: true}
	res, err := e.Apply(t.Context(), LVMOp{
		Action: "create-pool", PoolID: "p1", Name: "ndlvg", Disks: []string{"/dev/disk/by-id/wwn-0x5000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Argv, " ")
	if strings.Contains(joined, "vgexport") {
		t.Fatal(joined)
	}
	if res.Status == StatusAvailable {
		t.Fatalf("SkipHostCmds must not claim available: %+v", res)
	}
	if res.Status != StatusUnavailable {
		t.Fatalf("%+v", res)
	}
	if res.VGUUID != "" {
		t.Fatalf("must not invent vg_uuid: %+v", res)
	}
	if res.Incremental {
		t.Fatal("send")
	}
	vol, err := e.Apply(t.Context(), LVMOp{
		Action: "create-volume", PoolID: "p1", Name: "ndlvg",
		VolumeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Class: ClassVMDisk, SizeBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if vol.Status == StatusAvailable || vol.VGUUID != "" {
		t.Fatalf("create-volume SkipHostCmds must stay unavailable: %+v", vol)
	}
}
