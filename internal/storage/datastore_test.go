package storage

import (
	"strings"
	"testing"
)

func TestDatastoreCapabilitiesNoIncrementalSend(t *testing.T) {
	if NFSCapabilities().IncrementalSend || SMBCapabilities().IncrementalSend || ISCSICapabilities().IncrementalSend {
		t.Fatal("network datastores must not claim incremental send")
	}
	if ISCSICapabilities().Snapshots {
		t.Fatal("iscsi snapshots must stay false")
	}
	if !NFSCapabilities().Snapshots || !NFSCapabilities().SharedWarning {
		t.Fatal("nfs should allow overlay snaps with a shared warning")
	}
}

func TestNFSLocatorAndMountArgv(t *testing.T) {
	if _, _, err := ParseNFSLocator("/export"); err == nil {
		t.Fatal("bare path")
	}
	if _, _, err := ParseNFSLocator("nas.example:/etc/passwd"); err != nil {
		t.Fatal("export on the NAS is a locator, not a local path")
	}
	server, export, err := ParseNFSLocator("nas.example:/export/iso")
	if err != nil {
		t.Fatal(err)
	}
	mount := NFSMountRoot + "/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	argv, err := NFSMountArgv(server, export, mount)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != MountBin || strings.Contains(joined, "password=") || strings.Contains(joined, "bash") {
		t.Fatal(joined)
	}
	if _, err := NFSMountArgv(server, export, "/mnt/nfs"); err == nil {
		t.Fatal("outside root")
	}
}

func TestSMBMountUsesCredentialsFileNotArgvPassword(t *testing.T) {
	server, share, err := ParseSMBLocator("//files.example/iso")
	if err != nil || server != "files.example" || share != "iso" {
		t.Fatal(server, share, err)
	}
	pool := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	cred, err := CredPath(pool)
	if err != nil {
		t.Fatal(err)
	}
	mount, _ := DatastoreMountPath(BackendSMB, pool)
	argv, err := SMBMountArgv(server, share, cred, mount)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "password=") || !strings.Contains(joined, "credentials=") {
		t.Fatal(joined)
	}
	if err := refuseDatastoreArgv([]string{MountBin, "-t", "cifs", "-o", "password=secret", "//x/y", mount}); err == nil {
		t.Fatal("password on argv")
	}
}

func TestISCSILoginArgvNoPassword(t *testing.T) {
	argv, err := ISCSILoginArgv("iqn.2020-01.com.example:target1", "10.0.0.8:3260")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != ISCSIAdmBin || !strings.Contains(joined, "--login") || strings.Contains(joined, "password") {
		t.Fatal(joined)
	}
	if _, err := ParseIQN("not-an-iqn"); err == nil {
		t.Fatal("iqn")
	}
}

func TestDatastoreShareDownStaysUnavailable(t *testing.T) {
	e := DatastoreEngine{SkipHostCmds: true, Installed: boolPtr(true), Mounted: map[string]bool{}}
	obs := e.ObserveHints(t.Context(), []PoolHint{{
		PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", BackendType: BackendNFS,
		RootPath: NFSMountRoot + "/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Backing:  BackingIdentity{Device: "nas.example:/export", FSType: BackendNFS},
	}})
	if len(obs) != 1 || obs[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs[0].Capacity.UsableBytes != nil {
		t.Fatal("unavailable must not report zero capacity")
	}
	if !strings.Contains(obs[0].Reason, "Desired rows remain") {
		t.Fatal(obs[0].Reason)
	}
}

func TestDatastoreSkipHostCmdsMountDoesNotInventLiveShare(t *testing.T) {
	e := DatastoreEngine{SkipHostCmds: true, Installed: boolPtr(false)}
	res, err := e.Apply(t.Context(), DatastoreOp{
		Action: "observe", Kind: BackendNFS, PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Locator: "nas.example:/export",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnavailable || res.Incremental {
		t.Fatalf("%+v", res)
	}
}

func TestDatastoreFakeMountIsAvailable(t *testing.T) {
	pool := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mount := NFSMountRoot + "/" + pool
	e := DatastoreEngine{SkipHostCmds: true, Installed: boolPtr(true), Mounted: map[string]bool{mount: true}}
	res, err := e.Apply(t.Context(), DatastoreOp{Action: "observe", Kind: BackendNFS, PoolID: pool})
	if err != nil || res.Status != StatusAvailable {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDatastoreSkipHostCmdsMountDoesNotInventAvailable(t *testing.T) {
	e := DatastoreEngine{SkipHostCmds: true, Installed: boolPtr(true)}
	res, err := e.Apply(t.Context(), DatastoreOp{
		Action: "mount", Kind: BackendNFS, PoolID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Locator: "nas.example:/export",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnavailable || res.Incremental {
		t.Fatalf("%+v", res)
	}
}

func TestSMBEngineMountArgvOmitsPassword(t *testing.T) {
	pool := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mount, _ := DatastoreMountPath(BackendSMB, pool)
	e := DatastoreEngine{SkipHostCmds: true, Installed: boolPtr(true), Mounted: map[string]bool{mount: true}}
	res, err := e.Apply(t.Context(), DatastoreOp{
		Action: "mount", Kind: BackendSMB, PoolID: pool, Locator: "//files.example/iso",
		Username: "u", Password: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Argv, " ")
	if strings.Contains(joined, "s3cret") || strings.Contains(joined, "password=") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "credentials=") {
		t.Fatal(joined)
	}
}
