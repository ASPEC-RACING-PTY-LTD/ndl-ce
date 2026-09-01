package rpc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentProtoHasNoHostExec(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	proto := filepath.Join(filepath.Dir(file), "..", "..", "proto", "nodal", "agent", "v1", "agent.proto")
	b, err := os.ReadFile(proto)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	banned := []string{
		"rpc HostExec(",
		"rpc ExecHost(",
		"rpc Exec(",
		"string command =",
		"bytes command =",
		"string shell =",
		"rpc Shell(",
	}
	for _, needle := range banned {
		if strings.Contains(text, needle) {
			t.Fatalf("proto contains banned %q", needle)
		}
	}
	start := strings.Index(text, "oneof method {")
	end := strings.Index(text[start:], "}")
	if start < 0 || end < 0 {
		t.Fatal("ExecuteRequest oneof missing")
	}
	oneof := text[start : start+end]
	if !strings.Contains(oneof, "Ping ping =") {
		t.Fatal("Execute oneof must include Ping")
	}
	allowed := []string{
		"Ping ping =",
		"CreateDirectoryPool create_directory_pool =",
		"CreateDirectoryVolume create_directory_volume =",
		"NetDryRun net_dry_run =",
		"NetApply net_apply =",
		"CTCreate ct_create =",
		"CTLifecycle ct_lifecycle =",
		"QemuProtoStart qemu_proto_start =",
		"QemuProtoStop qemu_proto_stop =",
		"QemuProtoStatus qemu_proto_status =",
		"VMPrepare vm_prepare =",
		"VMLifecycle vm_lifecycle =",
		"VMQueryPCI vm_query_pci =",
	}
	for _, name := range allowed {
		if !strings.Contains(oneof, name) {
			t.Fatalf("Execute oneof missing %s", name)
		}
	}
	if strings.Count(oneof, "=") != len(allowed) {
		t.Fatalf("Execute oneof must only contain typed methods:\n%s", oneof)
	}
	for _, need := range []string{
		"rpc Hello(",
		"rpc Observe(",
		"rpc Execute(",
		"rpc Enroll(",
		"rpc OpenSession(",
		"rpc GetInventory(",
		"rpc GetMetrics(",
		"rpc GetStorage(",
		"rpc GetNetworks(",
		"rpc GetWorkloads(",
		"rpc UploadLibrary(",
		"rpc FilesOp(",
		"rpc FilesPut(",
		"rpc FilesGet(",
		"rpc AttachTerminal(",
	} {
		if !strings.Contains(text, need) {
			t.Fatalf("proto missing %s", need)
		}
	}
}
