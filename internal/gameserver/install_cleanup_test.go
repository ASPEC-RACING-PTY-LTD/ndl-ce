package gameserver

import (
	"context"
	"strings"
	"testing"
)

// An install container left behind by a control restart must not block the
// next install with "name already in use".
func TestInstallRemovesALeftoverInstallContainer(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	var calls []string
	rt.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	if err := rt.runInstallContainer(context.Background(), Server{ID: "abc"}, t.TempDir(), "img", "/mnt/server/x.sh"); err != nil {
		t.Fatal(err)
	}
	name := containerName("abc") + "-install"
	if len(calls) != 2 || calls[0] != "rm -f "+name || !strings.HasPrefix(calls[1], "run --rm --name "+name) {
		t.Fatalf("install must clear a leftover %s first: %q", name, calls)
	}
}
