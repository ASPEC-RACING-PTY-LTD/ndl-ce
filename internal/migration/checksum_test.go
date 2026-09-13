package migration

import (
	"strings"
	"testing"
)

func TestRelJailAllowsDotDotInFilename(t *testing.T) {
	root := t.TempDir()
	got, err := RelJail(root, "etc/foo..bar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "foo..bar") {
		t.Fatalf("%s", got)
	}
}

func TestRelJailRefusesParentSegment(t *testing.T) {
	root := t.TempDir()
	if _, err := RelJail(root, "../etc/passwd"); err == nil {
		t.Fatal("parent segment must be refused")
	}
	if _, err := RelJail(root, "a/../../etc"); err == nil {
		t.Fatal("cleaned parent segment must be refused")
	}
}
