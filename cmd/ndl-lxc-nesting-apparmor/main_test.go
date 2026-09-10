package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestRunSkipsNonStartHostHooks(t *testing.T) {
	if err := run([]string{"ct", "/tmp/config", "lxc", "pre-start"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunTwoArgNoopWhenProfileCurrent(t *testing.T) {
	dir := t.TempDir()
	id := uuid.NewString()
	aa := filepath.Join(dir, "apparmor")
	if err := os.MkdirAll(aa, 0o750); err != nil {
		t.Fatal(err)
	}
	body := "profile x {\n  ### Configuration: nesting\n  mount,\n}\n"
	path := filepath.Join(aa, "lxc-"+id+"_<-var-lib-ndl-runtime-lxc>")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{dir, id}); err != nil {
		t.Fatal(err)
	}
}

func TestRunUsage(t *testing.T) {
	t.Setenv("LXC_HOOK_TYPE", "")
	t.Setenv("LXC_NAME", "")
	t.Setenv("LXC_CONFIG_FILE", "")
	if err := run(nil); err == nil {
		t.Fatal("want usage error")
	}
}
