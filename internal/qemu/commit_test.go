package qemu

import "testing"

func TestCommitNodesPicksFormatNodes(t *testing.T) {
	nodes := []qmpBlockNode{
		{NodeName: "#block123", File: "/p/overlay.qcow2", Drv: "file"},
		{NodeName: "disk0-overlay", File: "/p/overlay.qcow2", Drv: "qcow2"},
		{NodeName: "disk0-file", File: "/p/base.qcow2", Drv: "file"},
		{NodeName: "disk0", File: "/p/base.qcow2", Drv: "qcow2"},
	}
	top, base := commitNodes(nodes, "/p/overlay.qcow2", "/p/base.qcow2")
	if top != "disk0-overlay" || base != "disk0" {
		t.Fatalf("top=%q base=%q", top, base)
	}
	if top, base := commitNodes(nodes, "/p/x.qcow2", "/p/base.qcow2"); top != "" || base != "disk0" {
		t.Fatalf("missing overlay must not resolve, got %q %q", top, base)
	}
}
