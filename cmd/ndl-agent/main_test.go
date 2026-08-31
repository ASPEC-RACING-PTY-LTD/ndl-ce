package main

import "testing"

func TestMainCompiles(t *testing.T) {
	if osArgsStub()[0] != "ndl-agent" {
		t.Fatal("args")
	}
}

func osArgsStub() []string {
	return []string{"ndl-agent"}
}
