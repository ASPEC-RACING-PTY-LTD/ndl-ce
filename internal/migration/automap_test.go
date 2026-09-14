package migration

import "testing"

func TestAutoMapSingleDestIsUnambiguous(t *testing.T) {
	t.Parallel()
	m, findings := AutoMap(
		[]string{"local", "local-lvm"},
		[]string{"vmbr0"},
		[]DestResource{{ID: "pool-1", Name: "Fast-ZFS"}},
		[]DestResource{{ID: "net-1", Name: "LAN", BridgeName: "ndl0"}},
		Mapping{},
	)
	if len(findings) != 0 {
		t.Fatalf("findings %+v", findings)
	}
	if m.Storage["local"] != "pool-1" || m.Storage["local-lvm"] != "pool-1" {
		t.Fatalf("storage %+v", m.Storage)
	}
	if m.Network["vmbr0"] != "net-1" {
		t.Fatalf("network %+v", m.Network)
	}
}

func TestAutoMapNameMatchAmongMany(t *testing.T) {
	t.Parallel()
	m, findings := AutoMap(
		[]string{"local"},
		[]string{"vmbr0"},
		[]DestResource{{ID: "pool-a", Name: "other"}, {ID: "pool-b", Name: "local"}},
		[]DestResource{{ID: "net-a", Name: "WAN"}, {ID: "net-b", Name: "LAN", BridgeName: "vmbr0"}},
		Mapping{},
	)
	if len(findings) != 0 || m.Storage["local"] != "pool-b" || m.Network["vmbr0"] != "net-b" {
		t.Fatalf("%+v %+v", m, findings)
	}
}

func TestAutoMapAmbiguousRequiresMapping(t *testing.T) {
	t.Parallel()
	_, findings := AutoMap(
		[]string{"nas01"},
		[]string{"vmbr2"},
		[]DestResource{{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}},
		[]DestResource{{ID: "n1", Name: "X"}, {ID: "n2", Name: "Y"}},
		Mapping{},
	)
	if len(findings) != 2 {
		t.Fatalf("findings %+v", findings)
	}
	if findings[0].Level != CompatRequiresMapping || findings[1].Level != CompatRequiresMapping {
		t.Fatalf("levels %+v", findings)
	}
}

func TestAutoMapKeepsOperatorOverride(t *testing.T) {
	t.Parallel()
	m, findings := AutoMap(
		[]string{"local"},
		[]string{"vmbr0"},
		[]DestResource{{ID: "pool-1", Name: "local"}},
		[]DestResource{{ID: "net-1", Name: "vmbr0"}},
		Mapping{Storage: map[string]string{"local": "chosen-pool"}, Network: map[string]string{"vmbr0": "chosen-net"}},
	)
	if len(findings) != 0 || m.Storage["local"] != "chosen-pool" || m.Network["vmbr0"] != "chosen-net" {
		t.Fatalf("%+v %+v", m, findings)
	}
}

func TestSuggestMode(t *testing.T) {
	t.Parallel()
	mode, f := SuggestMode(DiscoveredWorkload{Name: "web", Caps: []string{ModeOffline}, Running: false})
	if mode != ModeOffline || f != nil {
		t.Fatalf("stopped offline %s %+v", mode, f)
	}
	mode, f = SuggestMode(DiscoveredWorkload{Name: "web", Caps: []string{ModeOffline, ModeDisk}, Running: true})
	if mode != ModeOffline || f == nil || f.Code != "source-must-stop" {
		t.Fatalf("running %s %+v", mode, f)
	}
	mode, f = SuggestMode(DiscoveredWorkload{Name: "ct", Caps: []string{ModeBackup, ModeDisk}})
	if mode != ModeBackup || f != nil {
		t.Fatalf("backup %s %+v", mode, f)
	}
	mode, f = SuggestMode(DiscoveredWorkload{Name: "win", Caps: []string{ModeDisk}})
	if mode != ModeDisk || f == nil || f.Code != "disk-import" {
		t.Fatalf("disk %s %+v", mode, f)
	}
}

func TestSuggestModeForStrategy(t *testing.T) {
	t.Parallel()
	running := DiscoveredWorkload{Name: "web", Caps: []string{ModeOffline, ModeBackup}, Running: true}
	mode, f := SuggestModeForStrategy(DiscoveredWorkload{Name: "ct", Caps: []string{ModeLocal, ModeBackup}, Running: false}, StrategyConsistent)
	if mode != ModeLocal || f != nil {
		t.Fatalf("local %s %+v", mode, f)
	}
	mode, f = SuggestModeForStrategy(running, StrategyLeaveRunning)
	if mode != ModeBackup || f != nil {
		t.Fatalf("leave-running %s %+v", mode, f)
	}
	mode, f = SuggestModeForStrategy(running, StrategyConsistent)
	if mode != ModeBackup || f != nil {
		t.Fatalf("consistent running %s %+v", mode, f)
	}
	mode, f = SuggestModeForStrategy(DiscoveredWorkload{Name: "ct", Caps: []string{ModeOffline}}, StrategyBackup)
	if mode != "" || f == nil || f.Level != CompatBlocked {
		t.Fatalf("backup blocked %s %+v", mode, f)
	}
	reason := "LXC rootfs on local-lvm (lvmthin) is not HTTP-downloadable. Add a directory, NFS, or CIFS storage with backup content so No-dal can create a temporary vzdump."
	mode, f = SuggestModeForStrategy(DiscoveredWorkload{Name: "ct", Caps: []string{ModeDisk}, BlockReason: reason}, StrategyBackup)
	if mode != "" || f == nil || f.Level != CompatBlocked || f.Message != reason {
		t.Fatalf("block reason %s %+v", mode, f)
	}
	if _, err := NormalizeStrategy(StrategyMinimalInterruption); err == nil {
		t.Fatal("minimal interruption must be unavailable")
	}
	got, err := NormalizeStrategy("")
	if err != nil || got != StrategyConsistent {
		t.Fatalf("default %s %v", got, err)
	}
}
