package migrate

import (
	"testing"
)

func TestLiveSuccessMovesOwnershipWithoutSecondCopy(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	res, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b", Epoch: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateOK || res.Epoch != 4 || res.SourceRunning || !res.DestRunning {
		t.Fatalf("%+v", res)
	}
	if rt.SourceRunning(t.Context(), "wl") {
		t.Fatal("source must be stopped after successful live migrate")
	}
	if rt.DestIncoming("wl") {
		t.Fatal("dest incoming must be cleared")
	}
	if !rt.DestRunning("wl") {
		t.Fatal("dest must be running")
	}
}

func TestFailedLiveLeavesSourceRunningAndAbortsDest(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	rt.FailLive = true
	res, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b",
	})
	if err == nil {
		t.Fatal("live failure")
	}
	if res.State != StateFail || !res.SourceRunning || res.DestRunning {
		t.Fatalf("source must keep running: %+v", res)
	}
	if !rt.SourceRunning(t.Context(), "wl") {
		t.Fatal("source unit must still be running")
	}
	if rt.DestRunning("wl") || rt.DestIncoming("wl") {
		t.Fatal("failed dest must be aborted so a second copy is not left running")
	}
}

func TestCTLiveIsRefused(t *testing.T) {
	rt := NewFake()
	_, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindCT, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b",
	})
	if err == nil {
		t.Fatal("CT live must be refused")
	}
}

func TestOCILiveIsRefused(t *testing.T) {
	rt := NewFake()
	_, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindOCI, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b",
	})
	if err == nil {
		t.Fatal("OCI live must be refused")
	}
}

func TestCPUHostLiveIsRefused(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	_, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeLive, CPUHost: true,
		SourceNodeID: "a", DestNodeID: "b",
	})
	if err == nil {
		t.Fatal("cpu host live must be refused")
	}
	if !rt.SourceRunning(t.Context(), "wl") {
		t.Fatal("refused live must not stop source")
	}
}

func TestSameNodeIsRefused(t *testing.T) {
	rt := NewFake()
	_, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeOffline,
		SourceNodeID: "a", DestNodeID: "a",
	})
	if err == nil {
		t.Fatal("same node")
	}
}

func TestOfflineCTCopiesThenStartsDest(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	res, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindCT, Mode: ModeOffline,
		SourceNodeID: "a", DestNodeID: "b",
		Disks: []VolumeCopy{{VolumeID: "vol", SourcePath: "/var/lib/ndl/storage/p/a", DestPath: "/var/lib/ndl/storage/p/b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateOK || res.SourceRunning || !res.DestRunning {
		t.Fatalf("%+v", res)
	}
	if len(rt.Copies) != 1 {
		t.Fatalf("copies=%d", len(rt.Copies))
	}
}

func TestOfflineSharedStorageSkipsCopy(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	_, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeOffline, SharedStorage: true,
		SourceNodeID: "a", DestNodeID: "b",
		Disks: []VolumeCopy{{VolumeID: "vol"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Copies) != 0 {
		t.Fatal("shared storage must not copy")
	}
}

func TestLiveABIMismatchLeavesSourceRunning(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	res, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b",
		SourceArgv: []string{"/usr/bin/qemu-system-x86_64", "-smp", "1"},
		DestArgv:   []string{"/usr/bin/qemu-system-x86_64", "-smp", "2"},
	})
	if err == nil {
		t.Fatal("ABI mismatch must fail")
	}
	if res.State != StateFail || !res.SourceRunning || res.DestRunning {
		t.Fatalf("source must keep running: %+v", res)
	}
	if !rt.SourceRunning(t.Context(), "wl") {
		t.Fatal("source unit must still be running")
	}
	if rt.DestRunning("wl") || rt.DestIncoming("wl") {
		t.Fatal("dest must be aborted")
	}
}

func TestPrepareFailureLeavesSourceRunning(t *testing.T) {
	rt := NewFake()
	rt.SetSourceRunning("wl", true)
	rt.FailPrepare = true
	res, err := Run(t.Context(), rt, Request{
		WorkloadID: "wl", Kind: KindVM, Mode: ModeLive,
		SourceNodeID: "a", DestNodeID: "b",
	})
	if err == nil {
		t.Fatal("prepare")
	}
	if !res.SourceRunning {
		t.Fatal("source must remain running when dest cannot be prepared")
	}
}
