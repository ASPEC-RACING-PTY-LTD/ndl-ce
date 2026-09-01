package main

import "testing"

func TestHelpListsPhase1Commands(t *testing.T) {
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
}

func TestVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
}
