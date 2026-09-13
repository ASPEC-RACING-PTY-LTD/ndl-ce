package vmspec

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFATVolumeLabelVisibleToBlkID(t *testing.T) {
	img, err := BuildCIDATA(map[string][]byte{
		"user-data": []byte("#cloud-config\n"),
		"meta-data": []byte("instance-id: x\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rootOff := fatReserved*fatBytesPerSector + fatCount*fatSectorsPerFAT*fatBytesPerSector
	if string(img[rootOff:rootOff+11]) != "CIDATA     " || img[rootOff+11] != 0x08 {
		t.Fatalf("missing volume-label directory entry: %q attr=%#x", img[rootOff:rootOff+11], img[rootOff+11])
	}
	blkid, err := exec.LookPath("blkid")
	if err != nil {
		t.Skip("blkid not installed")
	}
	path := filepath.Join(t.TempDir(), "cidata.fat")
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(blkid, "-o", "export", path).CombinedOutput()
	if err != nil {
		t.Fatalf("blkid: %v: %s", err, out)
	}
	if !bytes.Contains(bytes.ToUpper(out), []byte("LABEL=CIDATA")) {
		t.Fatalf("cloud-init needs LABEL=cidata, blkid said:\n%s", out)
	}
}

func TestFATMountListsNoCloudFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to loop-mount")
	}
	img, err := BuildCIDATA(map[string][]byte{
		"user-data": []byte("#cloud-config\nhostname: cert\n"),
		"meta-data": []byte("instance-id: cert\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cidata.fat")
	mnt := filepath.Join(dir, "mnt")
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", "-o", "loop,ro", path, mnt).CombinedOutput(); err != nil {
		t.Fatalf("mount: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", mnt).Run() })
	ents, err := os.ReadDir(mnt)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range ents {
		got[e.Name()] = true
	}
	for _, name := range []string{"user-data", "meta-data"} {
		if !got[name] {
			t.Fatalf("mounted cidata missing %s: %v", name, got)
		}
	}
	body, err := os.ReadFile(filepath.Join(mnt, "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hostname: cert") {
		t.Fatalf("user-data %q", body)
	}
}
