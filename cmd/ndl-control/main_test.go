package main

import "testing"

func TestMainCompiles(t *testing.T) {
	if len(osArgsStub()) == 0 {
		t.Fatal("args")
	}
}

func osArgsStub() []string {
	return []string{"ndl-control"}
}
