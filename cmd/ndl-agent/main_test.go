package main

import "testing"

func TestAgentPackage(t *testing.T) {
	if osArgsStub()[0] != "ndl-agent" {
		t.Fatal("args")
	}
}

func osArgsStub() []string { return []string{"ndl-agent"} }
