package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestFindOrphanUnits(t *testing.T) {
	unitDir := t.TempDir()
	data := t.TempDir()
	wants := filepath.Join(unitDir, "multi-user.target.wants")
	wlWants := filepath.Join(unitDir, "nodal-workloads.target.wants")
	for _, d := range []string{wants, wlWants, filepath.Join(data, "net", "dnsmasq"), filepath.Join(data, "workloads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	liveNet, goneNet := uuid.NewString(), uuid.NewString()
	liveCT, goneCT, goneOCI := uuid.NewString(), uuid.NewString(), uuid.NewString()
	touch := func(p string) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch(filepath.Join(data, "net", "dnsmasq", liveNet+".conf"))
	touch(filepath.Join(data, "workloads", liveCT, "last-applied.json"))
	for _, u := range []string{"ndl-dnsmasq@" + liveNet, "ndl-dnsmasq@" + goneNet, "ndl-dnsmasq@not-a-uuid", "ssh"} {
		touch(filepath.Join(wants, u+".service"))
	}
	for _, u := range []string{"nodal-ct@" + liveCT, "nodal-ct@" + goneCT, "nodal-oci@" + goneOCI} {
		touch(filepath.Join(wlWants, u+".service"))
	}
	got := findOrphanUnits(unitDir, data)
	want := map[string]bool{
		"ndl-dnsmasq@" + goneNet + ".service": true,
		"nodal-ct@" + goneCT + ".service":     false,
		"nodal-oci@" + goneOCI + ".service":   false,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for u, stop := range want {
		if s, ok := got[u]; !ok || s != stop {
			t.Fatalf("%s: got %v %v", u, s, ok)
		}
	}
}

func TestFindOrphanUnitsSkipsWithoutDataDir(t *testing.T) {
	unitDir := t.TempDir()
	wants := filepath.Join(unitDir, "nodal-workloads.target.wants")
	_ = os.MkdirAll(wants, 0o755)
	_ = os.WriteFile(filepath.Join(wants, "nodal-ct@"+uuid.NewString()+".service"), nil, 0o644)
	if got := findOrphanUnits(unitDir, filepath.Join(t.TempDir(), "missing")); len(got) != 0 {
		t.Fatalf("a missing data dir must not disable workloads: %v", got)
	}
}
