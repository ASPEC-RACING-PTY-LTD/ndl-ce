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
	if strings.Count(oneof, "=") != 1 {
		t.Fatalf("Phase 0 Execute oneof must only contain Ping:\n%s", oneof)
	}
	for _, need := range []string{"rpc Hello(", "rpc Observe(", "rpc Execute(", "rpc OpenSession("} {
		if !strings.Contains(text, need) {
			t.Fatalf("proto missing %s", need)
		}
	}
}
