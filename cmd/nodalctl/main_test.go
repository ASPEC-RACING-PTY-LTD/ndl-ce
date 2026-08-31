package main

import "testing"

func TestVersionFlag(t *testing.T) {
	if versionLine() != "nodalctl 0.0.0" {
		t.Fatal(versionLine())
	}
}
